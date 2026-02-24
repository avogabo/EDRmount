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
	for i := range doc.Files {
		subj := strings.ToLower(doc.Files[i].Subject)
		if strings.Contains(subj, ".mkv") && mkvIdx < 0 {
			mkvIdx = i
		}
	}
	if mkvIdx < 0 {
		return errors.New("health: no mkv file found in nzb")
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

	outRoot := strings.TrimSpace(cfg.NgPost.OutputDir)
	if outRoot == "" {
		outRoot = "/host/inbox/nzb"
	}
	parRoot := strings.TrimSpace(cfg.Upload.Par.Dir)
	if parRoot == "" {
		parRoot = "/host/inbox/par2"
	}
	relDir, _ := filepath.Rel(outRoot, filepath.Dir(nzbPath))
	if strings.HasPrefix(relDir, "..") {
		relDir = ""
	}
	parDir := filepath.Join(parRoot, relDir)
	stem := strings.TrimSuffix(filepath.Base(nzbPath), filepath.Ext(nzbPath))
	localUsedPar := make([]string, 0, 16)
	_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: using local PAR2 from %s", parDir))
	entries, err := os.ReadDir(parDir)
	if err != nil {
		return fmt.Errorf("health: read par2 dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".par2") {
			continue
		}
		name := strings.ToLower(e.Name())
		if !strings.HasPrefix(name, strings.ToLower(stem)) {
			continue
		}
		src := filepath.Join(parDir, e.Name())
		dst := filepath.Join(workDir, e.Name())
		if err := copyFilePerm(src, dst, 0o644); err != nil {
			return fmt.Errorf("health: copy local par2 %s: %w", e.Name(), err)
		}
		localUsedPar = append(localUsedPar, src)
	}
	if len(localUsedPar) == 0 {
		return fmt.Errorf("health: no local par2 found in %s for stem %s", parDir, stem)
	}
	_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: local par2 files loaded=%d", len(localUsedPar)))

	parMain := ""
	entries, _ = os.ReadDir(workDir)
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

	repairBin := r.par2RepairBinary()
	_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: par2 repair: %s r %s %s", repairBin, filepath.Base(parMain), filepath.Base(outFile)))

	mapTarget := func(rel string) {
		abs := filepath.Join(workDir, strings.TrimPrefix(rel, "/"))
		_ = os.MkdirAll(filepath.Dir(abs), 0o755)
		_ = os.Remove(abs)
		if err := os.Symlink(outFile, abs); err != nil {
			_ = copyFilePerm(outFile, abs, 0o644)
		}
		_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: par2 target mapped: %s -> %s", rel, filepath.Base(outFile)))
	}
	resolvePar2Target := func() string {
		if r.jobs == nil || r.jobs.DB() == nil || r.jobs.DB().SQL == nil {
			return ""
		}
		rows, _ := r.jobs.DB().SQL.QueryContext(ctx, `SELECT line FROM job_logs WHERE job_id=? ORDER BY ts DESC LIMIT 500`, jobID)
		if rows == nil {
			return ""
		}
		defer rows.Close()
		re := regexp.MustCompile(`Target:\s*"([^"]+)"\s*-\s*found\.`)
		for rows.Next() {
			var ln string
			if e := rows.Scan(&ln); e != nil {
				continue
			}
			m := re.FindStringSubmatch(ln)
			if len(m) != 2 {
				continue
			}
			cand := filepath.Clean(filepath.Join(workDir, strings.TrimPrefix(m[1], "/")))
			if st, err := os.Stat(cand); err == nil && st.Mode().IsRegular() {
				return cand
			}
		}
		return ""
	}
	runPar2 := func() error {
		cmd := exec.CommandContext(ctx, repairBin, "r", parMain, outFile)
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
		return cmd.Wait()
	}

	mapTarget(filepath.Join("host", "inbox", "media", filepath.Base(outFile)))
	if err := runPar2(); err != nil {
		missingRel := ""
		if r.jobs != nil && r.jobs.DB() != nil && r.jobs.DB().SQL != nil {
			rows, _ := r.jobs.DB().SQL.QueryContext(ctx, `SELECT line FROM job_logs WHERE job_id=? ORDER BY ts DESC LIMIT 200`, jobID)
			if rows != nil {
				defer rows.Close()
				re := regexp.MustCompile(`Target:\s*"([^"]+)"\s*-\s*missing\.`)
				for rows.Next() {
					var ln string
					if e := rows.Scan(&ln); e != nil {
						continue
					}
					if m := re.FindStringSubmatch(ln); len(m) == 2 {
						missingRel = strings.TrimPrefix(filepath.Clean(m[1]), "/")
						break
					}
				}
			}
		}
		if strings.TrimSpace(missingRel) != "" {
			mapTarget(missingRel)
			_ = r.jobs.AppendLog(ctx, jobID, "health: par2 retry after dynamic target map: "+missingRel)
			if err2 := runPar2(); err2 != nil {
				return fmt.Errorf("health: par2 repair failed after retry: %w", err2)
			}
		} else {
			return fmt.Errorf("health: par2 repair failed: %w", err)
		}
	}

	if found := resolvePar2Target(); strings.TrimSpace(found) != "" && found != outFile {
		if err := copyFilePerm(found, outFile, 0o644); err == nil {
			_ = r.jobs.AppendLog(ctx, jobID, fmt.Sprintf("health: normalized repaired target from %s -> %s", found, outFile))
		}
	}

	mediaOutDir := strings.TrimSpace(cfg.Watch.Media.Dir)
	if mediaOutDir == "" {
		mediaOutDir = "/host/inbox/media"
	}
	if err := os.MkdirAll(mediaOutDir, 0o755); err != nil {
		return fmt.Errorf("health: ensure media out dir: %w", err)
	}
	repairedOut := filepath.Join(mediaOutDir, filepath.Base(outFile))
	tmpOut := repairedOut + ".health.tmp"
	_ = os.Remove(tmpOut)
	if err := copyFilePerm(outFile, tmpOut, 0o644); err != nil {
		return fmt.Errorf("health: copy repaired mkv to tmp: %w", err)
	}
	if err := os.Rename(tmpOut, repairedOut); err != nil {
		_ = os.Remove(tmpOut)
		return fmt.Errorf("health: move repaired mkv to inbox media: %w", err)
	}
	_ = os.Chtimes(repairedOut, time.Now(), time.Now())
	_ = r.jobs.AppendLog(ctx, jobID, "health: repaired mkv moved to "+repairedOut)

	if err := r.healthRefreshImportDB(ctx, cfg, jobID, nzbPath); err != nil {
		_ = r.jobs.AppendLog(ctx, jobID, "health: db refresh WARN: "+err.Error())
	}
	if err := os.Remove(nzbPath); err == nil {
		_ = r.jobs.AppendLog(ctx, jobID, "health: removed corrupt nzb "+nzbPath)
	} else {
		_ = r.jobs.AppendLog(ctx, jobID, "health: WARN remove corrupt nzb: "+err.Error())
	}
	for _, p := range localUsedPar {
		if err := os.Remove(p); err == nil {
			_ = r.jobs.AppendLog(ctx, jobID, "health: removed used par2 "+p)
		} else {
			_ = r.jobs.AppendLog(ctx, jobID, "health: WARN remove par2 "+p+": "+err.Error())
		}
	}
	if err := os.RemoveAll(workDir); err == nil {
		_ = r.jobs.AppendLog(ctx, jobID, "health: cleaned workdir")
	}
	_ = r.jobs.AppendLog(ctx, jobID, "health: repaired OK")
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
