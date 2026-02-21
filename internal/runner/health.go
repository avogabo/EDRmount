package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gaby/EDRmount/internal/config"
)

type healthRepairPayload struct {
	Path string `json:"path"`
}

func (r *Runner) runHealthRepair(ctx context.Context, jobID string, cfg config.Config, payload healthRepairPayload) (retErr error) {
	if !cfg.Health.Enabled {
		return errors.New("health repair: disabled by config (health.enabled=false)")
	}

	nzbPath := strings.TrimSpace(payload.Path)
	if nzbPath == "" {
		return errors.New("health repair: payload.path required")
	}
	_ = r.upsertHealthState(ctx, nzbPath, "repairing", time.Now().Unix(), 0, "", jobID)
	defer func() {
		if retErr != nil {
			_ = r.upsertHealthState(ctx, nzbPath, "error", 0, 0, retErr.Error(), jobID)
			return
		}
		now := time.Now().Unix()
		_ = r.upsertHealthState(ctx, nzbPath, "repaired", now, now, "", jobID)
	}()

	// Cross-node coordination: lock file next to NZB (sidecar), so shared RAW trees don't double-repair.
	lockPath := nzbPath + ".health.lock"
	ttl := time.Duration(cfg.Health.Lock.LockTTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 6 * time.Hour
	}
	if err := acquireHealthLock(lockPath, ttl); err != nil {
		return fmt.Errorf("health repair: %w", err)
	}
	defer func() { _ = os.Remove(lockPath) }()

	workDir := filepath.Join("/cache", "health", jobID)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}

	baseName := filepath.Base(nzbPath)
	if !strings.HasSuffix(strings.ToLower(baseName), ".nzb") {
		baseName += ".nzb"
	}

	_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: workdir=%s", workDir))
	_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: nzb=%s", nzbPath))

	// Copy NZB into workdir (avoid symlink surprises when we later replace it)
	workNZB := filepath.Join(workDir, baseName)
	_ = os.Remove(workNZB)
	if b, err := os.ReadFile(nzbPath); err == nil {
		_ = os.WriteFile(workNZB, b, 0o644)
	} else {
		return fmt.Errorf("read nzb: %w", err)
	}
	// nzb-repair parser is strict with XML namespaces in some NZB variants.
	// Normalize temp NZB to namespace-free classic tags for maximum compatibility.
	if err := normalizeNZBForRepair(workNZB); err != nil {
		_ = r.jobs.AppendLog(ctx, jobID, "health: WARN nzb normalize for repair failed: "+err.Error())
	}

	stem := strings.TrimSuffix(baseName, filepath.Ext(baseName))
	repairedNZBTmp := filepath.Join(workDir, stem+".repaired.nzb")
	_ = os.Remove(repairedNZBTmp)

	repairCfg := filepath.Join(workDir, "nzb-repair.yaml")
	if err := writeNZBRepairConfig(repairCfg, cfg); err != nil {
		return fmt.Errorf("health: nzb-repair config: %w", err)
	}
	repairTmpDir := filepath.Join(workDir, "tmp")
	_ = os.MkdirAll(repairTmpDir, 0o755)
	_ = r.jobs.AppendLog(ctx, jobID, "health: nzb-repair starting")
	if err := runCommand(ctx, func(line string) {
		clean := sanitizeLine(line, cfg.NgPost.Pass)
		_ = r.jobs.AppendLog(ctx, jobID, clean)
	}, "nzb-repair", "-c", repairCfg, "-o", repairedNZBTmp, "--tmp-dir", repairTmpDir, workNZB); err != nil {
		return fmt.Errorf("health: nzb-repair failed: %w", err)
	}
	if st, err := os.Stat(repairedNZBTmp); err != nil || st.Size() == 0 {
		return errors.New("health: nzb-repair did not produce repaired NZB")
	}
	_ = r.jobs.AppendLog(ctx, jobID, "health: nzb-repair output ready")

	// Replace original NZB (backup original first)
	bakRoot := strings.TrimSpace(cfg.Health.BackupDir)
	if bakRoot == "" {
		bakRoot = "/cache/health-bak"
	}
	outRoot := strings.TrimSpace(cfg.NgPost.OutputDir)
	if outRoot == "" {
		outRoot = "/host/inbox/nzb"
	}
	rel, _ := filepath.Rel(outRoot, nzbPath)
	if strings.HasPrefix(rel, "..") {
		rel = filepath.Base(nzbPath)
	}
	bakPath := filepath.Join(bakRoot, rel)
	if err := os.MkdirAll(filepath.Dir(bakPath), 0o755); err != nil {
		return err
	}

	destTmp := nzbPath + ".health.tmp"
	if err := copyFilePerm(repairedNZBTmp, destTmp, 0o644); err != nil {
		return fmt.Errorf("copy repaired nzb: %w", err)
	}

	_ = os.Remove(bakPath)
	if err := copyFilePerm(nzbPath, bakPath, 0o644); err != nil {
		_ = os.Remove(destTmp)
		return fmt.Errorf("backup original: %w", err)
	}
	if err := os.Remove(nzbPath); err != nil {
		_ = os.Remove(destTmp)
		return fmt.Errorf("remove original after backup: %w", err)
	}
	if rerr := os.Rename(destTmp, nzbPath); rerr != nil {
		_ = copyFilePerm(bakPath, nzbPath, 0o644)
		_ = os.Remove(destTmp)
		return fmt.Errorf("replace nzb: %w", rerr)
	}

	if err := r.healthRefreshImportDB(ctx, cfg, jobID, nzbPath); err != nil {
		_ = r.jobs.AppendLog(ctx, jobID, "health: db refresh WARN: "+err.Error())
	}

	if err := os.RemoveAll(workDir); err == nil {
		_ = r.jobs.AppendLog(ctx, jobID, "health: cleaned workdir")
	}
	_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: repaired OK (backup=%s)", bakPath))
	return nil
}

func (r *Runner) upsertHealthState(ctx context.Context, path, status string, lastCheckedAt, lastRepairedAt int64, lastError, repairJobID string) error {
	if r.jobs == nil || r.jobs.DB() == nil || r.jobs.DB().SQL == nil {
		return errors.New("jobs db not configured")
	}
	_, err := r.jobs.DB().SQL.ExecContext(ctx, `INSERT INTO health_nzb_state(path,status,last_checked_at,last_error,last_repair_job_id,last_repaired_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET
		status=excluded.status,
		last_checked_at=CASE WHEN excluded.last_checked_at>0 THEN excluded.last_checked_at ELSE health_nzb_state.last_checked_at END,
		last_error=excluded.last_error,
		last_repair_job_id=CASE WHEN excluded.last_repair_job_id<>'' THEN excluded.last_repair_job_id ELSE health_nzb_state.last_repair_job_id END,
		last_repaired_at=CASE WHEN excluded.last_repaired_at>0 THEN excluded.last_repaired_at ELSE health_nzb_state.last_repaired_at END`,
		path, status, lastCheckedAt, lastError, repairJobID, lastRepairedAt)
	return err
}

func (r *Runner) healthRefreshImportDB(ctx context.Context, cfg config.Config, jobID, nzbPath string) error {
	if r.jobs == nil || r.jobs.DB() == nil {
		return errors.New("jobs db not configured")
	}
	db := r.jobs.DB().SQL
	var importID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM nzb_imports WHERE path=? ORDER BY imported_at DESC LIMIT 1`, nzbPath).Scan(&importID); err == nil {
		tx, err := db.BeginTx(ctx, nil)
		if err == nil {
			stmts := []string{
				`DELETE FROM nzb_segments WHERE import_id=?`,
				`DELETE FROM nzb_files WHERE import_id=?`,
				`DELETE FROM library_overrides WHERE import_id=?`,
				`DELETE FROM library_review_dismissed WHERE import_id=?`,
				`DELETE FROM library_resolved WHERE import_id=?`,
				`DELETE FROM manual_items WHERE import_id=?`,
				`DELETE FROM nzb_imports WHERE id=?`,
			}
			for _, s := range stmts {
				if _, e := tx.ExecContext(ctx, s, importID); e != nil {
					_ = tx.Rollback()
					return e
				}
			}
			if e := tx.Commit(); e != nil {
				return e
			}
			_ = r.jobs.AppendLog(ctx, jobID, "health: db old import removed: "+importID)
		}
	}

	_ = r.jobs.AppendLog(ctx, jobID, "health: db old import removed; waiting watcher reimport")
	return nil
}

func (r *Runner) healthRegeneratePAR2(ctx context.Context, cfg config.Config, jobID, nzbPath, mediaPath string) error {
	if !cfg.Upload.Par.Enabled || cfg.Upload.Par.RedundancyPercent <= 0 {
		return errors.New("par2 disabled in config")
	}
	parRoot := strings.TrimSpace(cfg.Upload.Par.Dir)
	if parRoot == "" {
		parRoot = "/host/inbox/par2"
	}
	outRoot := strings.TrimSpace(cfg.NgPost.OutputDir)
	if outRoot == "" {
		outRoot = "/host/inbox/nzb"
	}

	norm := func(s string) string {
		s = strings.ToLower(s)
		b := make([]byte, 0, len(s))
		dash := false
		for i := 0; i < len(s); i++ {
			c := s[i]
			ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
			if ok {
				b = append(b, c)
				dash = false
				continue
			}
			if !dash {
				b = append(b, '-')
				dash = true
			}
		}
		return strings.Trim(string(b), "-")
	}

	stem := strings.TrimSuffix(filepath.Base(nzbPath), filepath.Ext(nzbPath))
	want := norm(stem)
	mediaStem := norm(strings.TrimSuffix(filepath.Base(mediaPath), filepath.Ext(mediaPath)))
	relDir, _ := filepath.Rel(outRoot, filepath.Dir(nzbPath))
	if strings.HasPrefix(relDir, "..") {
		relDir = ""
	}
	keepDir := filepath.Join(parRoot, relDir)
	_ = os.MkdirAll(keepDir, 0o755)

	stagingDir := filepath.Join("/cache", "health", jobID, "par-new")
	_ = os.RemoveAll(stagingDir)
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return err
	}
	parBase := filepath.Join(stagingDir, stem+".par2")

	// Generate PAR2 against a stable NON-media target path so future repairs do
	// not depend on original /host/inbox/media files (they may be deleted).
	stableDir := filepath.Join("/cache", "health-targets")
	if err := os.MkdirAll(stableDir, 0o755); err != nil {
		return err
	}
	stableMediaPath := filepath.Join(stableDir, stem+filepath.Ext(mediaPath))
	_ = os.Remove(stableMediaPath)
	if err := os.Link(mediaPath, stableMediaPath); err != nil {
		if err := copyFilePerm(mediaPath, stableMediaPath, 0o644); err != nil {
			return err
		}
	}

	args := []string{"c", fmt.Sprintf("-r%d", cfg.Upload.Par.RedundancyPercent), "-B/", parBase, stableMediaPath}
	_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: par2 regenerate: %s %s", r.par2Binary(), strings.Join(args, " ")))
	if err := runCommand(ctx, func(line string) {
		clean := strings.TrimSpace(line)
		if clean != "" {
			_ = r.jobs.AppendLog(ctx, jobID, clean)
		}
	}, r.par2Binary(), args...); err != nil {
		return err
	}

	removed := 0
	entries, _ := os.ReadDir(keepDir)
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if e.IsDir() || !strings.HasSuffix(n, ".par2") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		nb := norm(base)
		// Remove old set by either NZB stem or media stem (legacy naming).
		if !strings.HasPrefix(nb, want) && (mediaStem == "" || !strings.HasPrefix(nb, mediaStem)) {
			continue
		}
		if err := os.Remove(filepath.Join(keepDir, e.Name())); err == nil {
			removed++
		}
	}

	moved := 0
	newEntries, _ := os.ReadDir(stagingDir)
	for _, e := range newEntries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".par2") {
			continue
		}
		src := filepath.Join(stagingDir, e.Name())
		dst := filepath.Join(keepDir, e.Name())
		_ = os.Remove(dst)
		if err := os.Rename(src, dst); err == nil {
			moved++
			continue
		}
		if err := copyFilePerm(src, dst, 0o644); err == nil {
			_ = os.Remove(src)
			moved++
		}
	}
	_ = os.RemoveAll(stagingDir)
	_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: par2 refreshed (removed=%d new=%d dir=%s)", removed, moved, keepDir))
	return nil
}

func normalizeNZBForRepair(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s := string(b)
	// prefixed namespace style -> plain tags
	s = strings.ReplaceAll(s, "<ns0:", "<")
	s = strings.ReplaceAll(s, "</ns0:", "</")
	s = strings.ReplaceAll(s, "xmlns:ns0=\"http://www.newzbin.com/DTD/2003/nzb\"", "")
	// default namespace style -> no namespace
	s = strings.ReplaceAll(s, "xmlns=\"http://www.newzbin.com/DTD/2003/nzb\"", "")

	// Normalize subject attributes to classic quoted yEnc style so nzb-repair can
	// consistently detect filenames (.par2/.mkv/etc).
	reSubject := regexp.MustCompile(`subject="([^"]+)"`)
	reToken := regexp.MustCompile(`(?i)([^\s"<>]+\.(par2|mkv|mp4|avi|m4v|mov|ts|m2ts|wmv))`)
	s = reSubject.ReplaceAllStringFunc(s, func(attr string) string {
		m := reSubject.FindStringSubmatch(attr)
		if len(m) != 2 {
			return attr
		}
		raw := m[1]
		fname := ""
		if q := regexp.MustCompile(`"([^"]+\.[A-Za-z0-9]+)"`).FindStringSubmatch(raw); len(q) == 2 {
			fname = strings.TrimSpace(q[1])
		} else if t := reToken.FindStringSubmatch(raw); len(t) >= 2 {
			fname = strings.TrimSpace(t[1])
		} else {
			low := strings.ToLower(strings.TrimSpace(raw))
			if strings.HasSuffix(low, ".par2") || strings.HasSuffix(low, ".mkv") {
				fname = strings.TrimSpace(raw)
			}
		}
		if fname == "" {
			return attr
		}
		return `subject="&quot;` + fname + `&quot; yEnc (1/1)"`
	})

	return os.WriteFile(path, []byte(s), 0o644)
}

func writeNZBRepairConfig(path string, cfg config.Config) error {
	downHost := strings.TrimSpace(cfg.Download.Host)
	downUser := strings.TrimSpace(cfg.Download.User)
	downPass := strings.TrimSpace(cfg.Download.Pass)
	downPort := cfg.Download.Port
	if downPort <= 0 {
		downPort = 563
	}
	downConn := cfg.Download.Connections
	if downConn <= 0 {
		downConn = 10
	}
	upHost := strings.TrimSpace(cfg.NgPost.Host)
	upUser := strings.TrimSpace(cfg.NgPost.User)
	upPass := strings.TrimSpace(cfg.NgPost.Pass)
	upPort := cfg.NgPost.Port
	if upPort <= 0 {
		upPort = 563
	}
	upConn := cfg.NgPost.Connections
	if upConn <= 0 {
		upConn = 5
	}

	if downHost == "" || downUser == "" || downPass == "" || upHost == "" || upUser == "" || upPass == "" {
		return errors.New("missing download/upload provider credentials for nzb-repair")
	}

	yaml := fmt.Sprintf("par2_exe: /usr/local/bin/par2\ndownload_providers:\n  - host: %s\n    port: %d\n    username: %s\n    password: %s\n    connections: %d\n    tls: %t\nupload_providers:\n  - host: %s\n    port: %d\n    username: %s\n    password: %s\n    connections: %d\n    tls: %t\n",
		downHost, downPort, downUser, downPass, downConn, cfg.Download.SSL,
		upHost, upPort, upUser, upPass, upConn, cfg.NgPost.SSL,
	)
	return os.WriteFile(path, []byte(yaml), 0o600)
}

func copyFilePerm(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	return out.Close()
}

// healthUploadCleanNZB uploads a single media file and writes an NZB that contains ONLY the media.
// This intentionally does NOT upload PAR2.
func (r *Runner) healthUploadCleanNZB(ctx context.Context, jobID string, cfg config.Config, mediaPath string, outNZB string) error {
	// Force ngpost for health re-upload: nyuu-generated NZB segment bytes can differ from decoded part size
	// and break streaming range math (EOF). ngpost keeps compatible per-segment sizing for our pipeline.
	provider := "ngpost"
	ng := cfg.NgPost
	if !ng.Enabled {
		return errors.New("health: upload provider config missing (ngpost.enabled=false)")
	}
	if ng.Host == "" || ng.User == "" || ng.Pass == "" || ng.Groups == "" {
		return errors.New("health: upload config incomplete (need host/user/pass/groups)")
	}

	sanitize := func(s string) string {
		if ng.Pass == "" {
			return s
		}
		return strings.ReplaceAll(s, ng.Pass, "***")
	}

	if provider == "nyuu" {
		args := []string{"-h", ng.Host, "-P", fmt.Sprintf("%d", ng.Port)}
		if ng.SSL {
			args = append(args, "-S")
		}
		if ng.Connections > 0 {
			args = append(args, "-n", fmt.Sprintf("%d", ng.Connections))
		}
		args = append(args, "-g", ng.Groups)
		// Safe obfuscation: metadata only (same strategy as normal upload path).
		args = append(args,
			"--subject", "${rand(40)} yEnc ({part}/{parts})",
			"--nzb-subject", `"{filename}" yEnc ({part}/{parts})`,
			"--message-id", "${rand(24)}-${rand(12)}@nyuu",
			"--from", "poster <poster@example.com>",
		)
		args = append(args, "-o", outNZB, "-O")
		args = append(args, "-u", ng.User, "-p", ng.Pass)
		args = append(args, "-r", "keep")
		args = append(args, mediaPath)

		_ = r.jobs.AppendLog(ctx, jobID, "health: uploading repaired media (clean NZB, no PAR2)")
		_ = r.jobs.AppendLog(ctx, jobID, sanitize(fmt.Sprintf("health: nyuu: %s %s", r.NyuuPath, strings.Join(args[:min(10, len(args))], " "))))
		err := runCommand(ctx, func(line string) {
			_ = r.jobs.AppendLog(ctx, jobID, sanitize(line))
		}, r.NyuuPath, args...)
		if err == nil {
			return nil
		}
		if strings.Contains(strings.ToLower(err.Error()), "illegal instruction") {
			_ = r.jobs.AppendLog(ctx, jobID, "health: nyuu illegal instruction; fallback to ngpost")
		} else {
			return err
		}
	}

	// ngpost
	args := []string{"-i", mediaPath, "-o", outNZB, "-h", ng.Host, "-P", fmt.Sprintf("%d", ng.Port)}
	if ng.SSL {
		args = append(args, "-s")
	}
	if ng.Connections > 0 {
		args = append(args, "-n", fmt.Sprintf("%d", ng.Connections))
	}
	if ng.Threads > 0 {
		args = append(args, "-t", fmt.Sprintf("%d", ng.Threads))
	}
	args = append(args, "-g", ng.Groups)
	if ng.Obfuscate {
		args = append(args, "-x")
	}
	if ng.TmpDir != "" {
		args = append(args, "--tmp_dir", ng.TmpDir)
	}
	args = append(args, "-u", ng.User, "-p", ng.Pass, "--disp_progress", "files")

	_ = r.jobs.AppendLog(ctx, jobID, "health: uploading repaired media (clean NZB, no PAR2)")
	_ = r.jobs.AppendLog(ctx, jobID, sanitize(fmt.Sprintf("health: ngpost: %s %s", r.NgPostPath, strings.Join(args[:min(10, len(args))], " "))))
	return runCommand(ctx, func(line string) {
		_ = r.jobs.AppendLog(ctx, jobID, sanitize(line))
	}, r.NgPostPath, args...)
}
