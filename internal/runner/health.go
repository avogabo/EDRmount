package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/nntp"
	"github.com/gaby/EDRmount/internal/nzb"
	"github.com/gaby/EDRmount/internal/yenc"
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
	stem := strings.TrimSuffix(baseName, filepath.Ext(baseName))

	_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: workdir=%s", workDir))
	_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: nzb=%s", nzbPath))

	workNZB := filepath.Join(workDir, baseName)
	if b, err := os.ReadFile(nzbPath); err == nil {
		_ = os.WriteFile(workNZB, b, 0o644)
	} else {
		return fmt.Errorf("read nzb: %w", err)
	}

	f, err := os.Open(workNZB)
	if err != nil {
		return err
	}
	doc, err := nzb.Parse(f)
	_ = f.Close()
	if err != nil {
		return fmt.Errorf("parse nzb: %w", err)
	}

	mkvIdx := -1
	parIdx := make([]int, 0, 16)
	for i := range doc.Files {
		subj := strings.ToLower(doc.Files[i].Subject)
		if strings.Contains(subj, ".mkv") && mkvIdx < 0 {
			mkvIdx = i
		}
		if strings.Contains(subj, ".par2") {
			parIdx = append(parIdx, i)
		}
	}
	if mkvIdx < 0 {
		return errors.New("health: no mkv file found in nzb")
	}
	if len(parIdx) == 0 {
		return errors.New("health: no par2 files found in nzb")
	}

	mkvName := "recovered.mkv"
	if m := regexp.MustCompile(`"([^"]+\.mkv)"`).FindStringSubmatch(doc.Files[mkvIdx].Subject); len(m) == 2 {
		mkvName = filepath.Base(m[1])
	}
	outFile := filepath.Join(workDir, mkvName)
	wf, err := os.OpenFile(outFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	_ = r.jobs.AppendLog(ctx, jobID, "health: nntp acquire start")
	pool := nntp.NewPool(nntp.Config{Host: cfg.Download.Host, Port: cfg.Download.Port, SSL: cfg.Download.SSL, User: cfg.Download.User, Pass: cfg.Download.Pass, Timeout: 30 * time.Second}, cfg.Download.Connections)
	cl, err := pool.Acquire(ctx)
	if err != nil {
		_ = wf.Close()
		return fmt.Errorf("health: nntp acquire: %w", err)
	}
	defer pool.Release(cl)
	_ = r.jobs.AppendLog(ctx, jobID, "health: nntp acquire ok")

	segs := make([]nzb.Segment, 0, len(doc.Files[mkvIdx].Segments))
	segs = append(segs, doc.Files[mkvIdx].Segments...)
	sort.Slice(segs, func(i, j int) bool { return segs[i].Number < segs[j].Number })
	missing := 0
	for i, s := range segs {
		if i%300 == 0 {
			_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: mkv download %d/%d", i, len(segs)))
		}
		lines, err := cl.BodyByMessageID(strings.TrimSpace(s.ID))
		if err != nil {
			missing++
			_, _ = wf.Write(make([]byte, int(s.Bytes)))
			continue
		}
		data, _, _, _, err := yenc.DecodePart(lines)
		if err != nil {
			missing++
			_, _ = wf.Write(make([]byte, int(s.Bytes)))
			continue
		}
		_, _ = wf.Write(data)
	}
	_ = wf.Sync()
	_ = wf.Close()
	if missing == 0 {
		_ = r.jobs.AppendLog(ctx, jobID, "health: no missing segments detected")
		return nil
	}
	_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: missing segments=%d", missing))

	_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: downloading par2 files count=%d", len(parIdx)))
	for _, idx := range parIdx {
		pf := doc.Files[idx]
		parName := fmt.Sprintf("par_%d.par2", idx)
		if m := regexp.MustCompile(`"([^"]+\.par2)"`).FindStringSubmatch(pf.Subject); len(m) == 2 {
			parName = filepath.Base(m[1])
		}
		parPath := filepath.Join(workDir, parName)
		of, err := os.OpenFile(parPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		psegs := make([]nzb.Segment, 0, len(pf.Segments))
		psegs = append(psegs, pf.Segments...)
		sort.Slice(psegs, func(i, j int) bool { return psegs[i].Number < psegs[j].Number })
		for i, s := range psegs {
			if i%300 == 0 {
				_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: par2 %s %d/%d", parName, i, len(psegs)))
			}
			lines, err := cl.BodyByMessageID(strings.TrimSpace(s.ID))
			if err != nil {
				_ = of.Close()
				return fmt.Errorf("health: par2 segment download failed: %w", err)
			}
			data, _, _, _, err := yenc.DecodePart(lines)
			if err != nil {
				_ = of.Close()
				return fmt.Errorf("health: par2 decode failed: %w", err)
			}
			_, _ = of.Write(data)
		}
		_ = of.Sync()
		_ = of.Close()
	}

	parMain := ""
	entries, _ := os.ReadDir(workDir)
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if strings.HasSuffix(n, ".par2") && !strings.Contains(n, ".vol") {
			parMain = filepath.Join(workDir, e.Name())
			break
		}
	}
	if parMain == "" {
		for _, e := range entries {
			if strings.HasSuffix(strings.ToLower(e.Name()), ".par2") {
				parMain = filepath.Join(workDir, e.Name())
				break
			}
		}
	}
	if parMain == "" {
		return errors.New("health: no usable par2 file in workdir")
	}

	_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: par2 repair: %s r %s %s", r.par2Binary(), filepath.Base(parMain), filepath.Base(outFile)))
	cmd := exec.CommandContext(ctx, r.par2Binary(), "r", parMain, outFile)
	cmd.Dir = workDir
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return err
	}
	scanPipe := func(prefix string, rc io.ReadCloser) {
		defer func() { _ = rc.Close() }()
		s := bufio.NewScanner(rc)
		for s.Scan() {
			_ = r.jobs.AppendLog(ctx, jobID, prefix+s.Text())
		}
	}
	if stdout != nil {
		go scanPipe("", stdout)
	}
	if stderr != nil {
		go scanPipe("ERR: ", stderr)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("health: par2 repair failed: %w", err)
	}

	repairedNZBTmp := filepath.Join(workDir, stem+".repaired.nzb")
	_ = os.Remove(repairedNZBTmp)
	if err := r.healthUploadCleanNZB(ctx, jobID, cfg, outFile, repairedNZBTmp); err != nil {
		return err
	}
	if err := nzb.NormalizeCanonical(repairedNZBTmp); err != nil {
		return fmt.Errorf("health: canonical normalize repaired nzb: %w", err)
	}

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

	groups := strings.TrimSpace(cfg.NgPost.Groups)
	groupsYaml := ""
	if groups != "" {
		parts := strings.Split(groups, ",")
		clean := make([]string, 0, len(parts))
		for _, p := range parts {
			g := strings.TrimSpace(p)
			if g != "" {
				clean = append(clean, g)
			}
		}
		if len(clean) > 0 {
			groupsYaml = "    groups:\n"
			for _, g := range clean {
				groupsYaml += fmt.Sprintf("      - %s\n", g)
			}
		}
	}

	yaml := fmt.Sprintf("par2_exe: /usr/local/bin/par2\ndownload_providers:\n  - host: %s\n    port: %d\n    username: %s\n    password: %s\n    connections: %d\n    tls: %t\nupload_providers:\n  - host: %s\n    port: %d\n    username: %s\n    password: %s\n    connections: %d\n    tls: %t\n%s",
		downHost, downPort, downUser, downPass, downConn, cfg.Download.SSL,
		upHost, upPort, upUser, upPass, upConn, cfg.NgPost.SSL,
		groupsYaml,
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
