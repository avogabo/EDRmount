package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/fusefs"
	"github.com/gaby/EDRmount/internal/importer"
	"github.com/gaby/EDRmount/internal/jobs"
	"github.com/gaby/EDRmount/internal/library"
	"github.com/gaby/EDRmount/internal/nzb"
	"github.com/gaby/EDRmount/internal/plex"
)

var rePercent = regexp.MustCompile(`\b(\d{1,3})%\b`)
var reSeasonNum = regexp.MustCompile(`(?i)(?:season|temporada|s)\s*0*(\d{1,2})`)
var reEpisodeNum = regexp.MustCompile(`(?i)\b(?:s\d{1,2}e\d{1,2}|\d{1,2}x\d{1,2})\b`)

type Runner struct {
	jobs *jobs.Store

	UploadConcurrency int
	PollInterval      time.Duration
	Mode              string // "stub" or "exec" (dev)

	NgPostPath string // default: /usr/local/bin/ngpost
	NyuuPath   string // default: /usr/local/bin/nyuu
	Par2Path   string // default: /usr/local/bin/par2j64

	GetConfig func() config.Config // optional live config provider
}

func New(j *jobs.Store) *Runner {
	return &Runner{jobs: j, UploadConcurrency: 1, PollInterval: 1 * time.Second, Mode: "stub", NgPostPath: "/usr/local/bin/ngpost", NyuuPath: "/usr/local/bin/nyuu", Par2Path: "/usr/local/bin/par2j64"}
}

func (r *Runner) Run(ctx context.Context) {
	semUpload := make(chan struct{}, r.UploadConcurrency)
	t := time.NewTicker(r.PollInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			job, err := r.jobs.ClaimNext(ctx)
			if err != nil {
				if err == jobs.ErrNoQueuedJobs {
					continue
				}
				continue
			}

			switch job.Type {
			case jobs.TypeUpload:
				semUpload <- struct{}{}
				go func(j *jobs.Job) {
					defer func() { <-semUpload }()
					r.runUpload(ctx, j)
				}(job)
			case jobs.TypeHealthRepair:
				go r.runHealth(ctx, job)
			case jobs.TypeHealthScan:
				go r.runHealthScan(ctx, job)
			default:
				go r.runImport(ctx, job)
			}
		}
	}
}

func (r *Runner) runImport(ctx context.Context, j *jobs.Job) {
	_ = r.jobs.AppendLog(ctx, j.ID, "starting import job")
	var p struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(j.Payload, &p)

	cfg := config.Default()
	if r.GetConfig != nil {
		cfg = r.GetConfig()
	}
	imp := importer.New(r.jobs)
	files, bytes, err := imp.ImportNZB(ctx, j.ID, p.Path)
	if err != nil {
		msg := err.Error()
		_ = r.jobs.AppendLog(ctx, j.ID, "ERROR: "+msg)
		_ = r.jobs.SetFailed(ctx, j.ID, msg)
		return
	}
	_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("imported NZB: files=%d total_bytes=%d", files, bytes))
	enrichCtx, cancelEnrich := context.WithTimeout(ctx, 120*time.Second)
	if err := imp.EnrichLibraryResolved(enrichCtx, cfg, j.ID); err != nil {
		_ = r.jobs.AppendLog(ctx, j.ID, "library_resolved: WARN: "+err.Error())
	}
	cancelEnrich()

	// Optional: ask Plex to refresh only the new item(s) in library-auto.
	if r.GetConfig != nil {
		cfg := r.GetConfig()
		if cfg.Plex.Enabled && cfg.Plex.RefreshOnImport {
			pc := plex.New(cfg.Plex.BaseURL, cfg.Plex.Token)
			if pc.Enabled() {
				paths, perr := fusefs.AutoVirtualPathsForImport(ctx, cfg, r.jobs, j.ID)
				if perr != nil {
					_ = r.jobs.AppendLog(ctx, j.ID, "plex: cannot build auto paths: "+perr.Error())
				} else {
					refreshed := 0
					for _, pth := range paths {
						plexPath := filepath.Join(cfg.Plex.PlexRoot, pth)
						// try directory first, then file path
						if err := pc.RefreshPath(ctx, plexPath, true); err != nil {
							_ = r.jobs.AppendLog(ctx, j.ID, "plex: refresh failed: "+err.Error())
						} else {
							refreshed++
						}
					}
					if refreshed > 0 {
						_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("plex: refresh ok (%d path(s))", refreshed))
					}
				}
			}
		}
	}

	_ = r.jobs.SetDone(ctx, j.ID)
}

func (r *Runner) runUpload(ctx context.Context, j *jobs.Job) {
	_ = r.jobs.AppendLog(ctx, j.ID, "starting upload job")
	_ = r.jobs.AppendLog(ctx, j.ID, "PHASE: Preparando (Preparing)")
	_ = r.jobs.AppendLog(ctx, j.ID, "PROGRESS: 0")
	var p struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(j.Payload, &p)

	if r.Mode == "exec" {
		cfg := config.Default()
		if r.GetConfig != nil {
			cfg = r.GetConfig()
		}
		ng := cfg.NgPost
		provider := "nyuu"
		if strings.ToLower(strings.TrimSpace(cfg.Upload.Provider)) != "nyuu" {
			_ = r.jobs.AppendLog(ctx, j.ID, "upload: forcing provider=nyuu")
		}

		// If upload path is a directory with subdirectories, treat each subdirectory as an independent season pack.
		if st, err := os.Stat(p.Path); err == nil && st.IsDir() {
			entries, _ := os.ReadDir(p.Path)
			hasSubDir := false
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					hasSubDir = true
					break
				}
			}
			if hasSubDir {
				enq := 0
				for _, e := range entries {
					if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
						continue
					}
					sub := filepath.Join(p.Path, e.Name())
					if _, err := r.jobs.Enqueue(ctx, jobs.TypeUpload, map[string]string{"path": sub}); err == nil {
						enq++
					}
				}
				_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("directory pack detected; enqueued %d season subfolder job(s)", enq))
				_ = r.jobs.SetDone(ctx, j.ID)
				return
			}
		}

		outDir := ng.OutputDir
		if outDir == "" {
			outDir = "/host/inbox/nzb"
		}
		sourceGuess := library.GuessFromFilename(filepath.Base(p.Path))
		normalizedInputPath := p.Path
		if np, changed, nerr := maybeNormalizeWithFileBot(ctx, cfg, p.Path, func(line string) {
			_ = r.jobs.AppendLog(ctx, j.ID, line)
		}); nerr != nil {
			_ = r.jobs.AppendLog(ctx, j.ID, "filebot: WARN: "+nerr.Error())
		} else if changed {
			normalizedInputPath = np
			_ = r.jobs.AppendLog(ctx, j.ID, "filebot: normalized for naming -> "+filepath.Base(np))
		}
		base := strings.TrimSuffix(filepath.Base(normalizedInputPath), filepath.Ext(normalizedInputPath))

		// IMPORTANT: write NZB to staging first so the import watcher never sees an incomplete NZB.
		cacheDir := cfg.Paths.CacheDir
		if strings.TrimSpace(cacheDir) == "" {
			cacheDir = "/cache"
		}
		stagingDir := filepath.Join(cacheDir, "nzb-staging")
		_ = os.MkdirAll(stagingDir, 0o755)
		stagingNZB := filepath.Join(stagingDir, fmt.Sprintf("%s-%s.nzb", base, j.ID))

		finalNZB := buildRawNZBPath(cfg, normalizedInputPath, outDir, sourceGuess.Quality)
		if st, err := os.Stat(finalNZB); err == nil && st.Size() > 0 {
			_ = r.jobs.AppendLog(ctx, j.ID, "nzb already exists at target path; skipping new upload to avoid duplicates: "+finalNZB)
			_ = r.jobs.SetDone(ctx, j.ID)
			return
		}

		sourceFiles, err := collectUploadFiles(p.Path)
		if err != nil || len(sourceFiles) == 0 {
			msg := "no files found to upload"
			if err != nil {
				msg = err.Error()
			}
			_ = r.jobs.AppendLog(ctx, j.ID, "ERROR: "+msg)
			_ = r.jobs.SetFailed(ctx, j.ID, msg)
			return
		}

		lastProgress := -1
		emitProgress := func(p int) {
			if p < 0 {
				p = 0
			}
			if p > 100 {
				p = 100
			}
			if p == lastProgress {
				return
			}
			lastProgress = p
			_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("PROGRESS: %d", p))
		}
		lastPhase := ""
		emitPhase := func(p string) {
			p = strings.TrimSpace(p)
			if p == "" || p == lastPhase {
				return
			}
			lastPhase = p
			_ = r.jobs.AppendLog(ctx, j.ID, "PHASE: "+p)
		}

		// Optional PAR2 generation (staged in /cache, then optionally persisted under /host/inbox/par2)
		parEnabled := cfg.Upload.Par.Enabled && cfg.Upload.Par.RedundancyPercent > 0
		parKeep := cfg.Upload.Par.KeepParityFiles && strings.TrimSpace(cfg.Upload.Par.Dir) != ""
		parStagingDir := filepath.Join(cacheDir, "par-staging", j.ID)
		var parDir string // where par2 files are generated (staging)
		if parEnabled {
			emitPhase("Generando PAR (Generating PAR)")
			emitProgress(5)
			_ = os.MkdirAll(parStagingDir, 0o755)

			parBase := filepath.Join(parStagingDir, base)
			args := []string{"c", fmt.Sprintf("-r%d", cfg.Upload.Par.RedundancyPercent), "-B/", parBase + ".par2"}
			args = append(args, sourceFiles...)
			if parEnabled {
				_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("par2: generating parity"))
			}
			// If par2create does not emit percentages, keep UI alive by ticking progress
			// (avoid looking stuck at 5% for large files).
			tickDone := make(chan struct{})
			stopTick := func() {
				select {
				case <-tickDone:
					// already closed
				default:
					close(tickDone)
				}
			}
			go func() {
				t := time.NewTicker(10 * time.Second)
				defer t.Stop()
				p := 5
				for {
					select {
					case <-tickDone:
						return
					case <-ctx.Done():
						return
					case <-t.C:
						// creep up to 19 while PAR is running
						if p < 19 {
							p++
							emitProgress(p)
						}
					}
				}
			}()

			err := error(nil)
			if parEnabled {
				err = runCommand(ctx, func(line string) {
					clean := strings.TrimSpace(line)
					if m := rePercent.FindStringSubmatch(clean); len(m) == 2 {
						if n, e := strconv.Atoi(m[1]); e == nil && n >= 0 && n <= 100 {
							// Map PAR stage to early progress window (5..20)
							p2 := 5 + (n * 15 / 100)
							emitProgress(p2)
						}
						return
					}
					if clean != "" {
						_ = r.jobs.AppendLog(ctx, j.ID, clean)
					}
				}, r.par2Binary(), args...)
			}
			stopTick()
			if !parEnabled {
				// already logged
			} else if err != nil {
				_ = r.jobs.AppendLog(ctx, j.ID, "WARN: par2create failed (continuing without PAR): "+err.Error())
				parEnabled = false
			} else {
				emitProgress(20)
				parDir = parStagingDir
			}
		}

		// Provider implementation
		if provider == "nyuu" {
			if !ng.Enabled || ng.Host == "" || ng.User == "" || ng.Pass == "" || ng.Groups == "" {
				msg := "nyuu config incomplete (need ngpost.enabled host/user/pass/groups as server source)"
				_ = r.jobs.AppendLog(ctx, j.ID, "ERROR: "+msg)
				_ = r.jobs.SetFailed(ctx, j.ID, msg)
				return
			}

			mediaFiles := append([]string{}, sourceFiles...)
			if len(mediaFiles) == 0 {
				msg := "nyuu upload has no media files"
				_ = r.jobs.AppendLog(ctx, j.ID, "ERROR: "+msg)
				_ = r.jobs.SetFailed(ctx, j.ID, msg)
				return
			}

			nyuuCfg, err := writeNyuuClassicConfig(cacheDir, j.ID, ng)
			if err != nil {
				msg := "cannot write nyuu classic config: " + err.Error()
				_ = r.jobs.AppendLog(ctx, j.ID, "ERROR: "+msg)
				_ = r.jobs.SetFailed(ctx, j.ID, msg)
				return
			}

			runNyuu := func(outNZB string, files []string, label string) error {
				args := []string{"-C", nyuuCfg, "-o", outNZB, "--overwrite", "--"}
				args = append(args, files...)
				_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("nyuu(classic:%s): %s -C %s -o %s --overwrite -- <files:%d>", label, r.NyuuPath, nyuuCfg, outNZB, len(files)))
				run := func(cmd string, argv ...string) error {
					return runCommand(ctx, func(line string) {
						clean := sanitizeLine(line, ng.Pass)
						_ = r.jobs.AppendLog(ctx, j.ID, clean)
						if label == "media" {
							if m := rePercent.FindStringSubmatch(clean); len(m) == 2 {
								if n, e := strconv.Atoi(m[1]); e == nil && n >= 0 && n <= 100 {
									emitProgress(n)
								}
							}
						}
					}, cmd, argv...)
				}
				if err := run(r.NyuuPath, args...); err != nil {
					if strings.Contains(strings.ToLower(err.Error()), "illegal instruction") {
						_ = r.jobs.AppendLog(ctx, j.ID, "WARN: nyuu illegal instruction; retrying with node --jitless")
						jitArgs := append([]string{"--jitless", r.NyuuPath}, args...)
						return run("node", jitArgs...)
					}
					return err
				}
				return nil
			}

			emitPhase("Subiendo a Usenet (Uploading media)")
			emitProgress(1)
			err = runNyuu(stagingNZB, mediaFiles, "media")
			if err != nil {
				msg := err.Error()
				_ = r.jobs.AppendLog(ctx, j.ID, "ERROR: "+msg)
				_ = r.jobs.SetFailed(ctx, j.ID, msg)
				return
			}
			if err := nzb.NormalizeCanonical(stagingNZB); err != nil {
				msg := "classic nzb normalize failed: " + err.Error()
				_ = r.jobs.AppendLog(ctx, j.ID, "ERROR: "+msg)
				_ = r.jobs.SetFailed(ctx, j.ID, msg)
				return
			}
			_ = r.jobs.AppendLog(ctx, j.ID, "nyuu: classic NZB normalization OK")

			// Upload PAR2 set as its own NZB and keep only that NZB (no raw .par2 files in inbox/par2).
			if parEnabled && parKeep && strings.TrimSpace(parDir) != "" {
				parFiles := make([]string, 0, 16)
				if entries, e := os.ReadDir(parDir); e == nil {
					for _, pe := range entries {
						if pe.IsDir() || !strings.HasSuffix(strings.ToLower(pe.Name()), ".par2") {
							continue
						}
						parFiles = append(parFiles, filepath.Join(parDir, pe.Name()))
					}
				}
				if len(parFiles) > 0 {
					sort.Strings(parFiles)
					parStagingNZB := filepath.Join(cacheDir, base+".par2.nzb")
					emitPhase("Subiendo PAR2 NZB (Uploading PAR2 NZB)")
					if e := runNyuu(parStagingNZB, parFiles, "par2"); e != nil {
						_ = r.jobs.AppendLog(ctx, j.ID, "WARN: PAR2 NZB upload failed: "+e.Error())
					} else {
						if e := nzb.NormalizeCanonical(parStagingNZB); e != nil {
							_ = r.jobs.AppendLog(ctx, j.ID, "WARN: PAR2 NZB normalize failed: "+e.Error())
						}
						r.persistParNZB(ctx, j.ID, cfg, outDir, finalNZB, parStagingNZB)
					}
				}
			}

			emitPhase("Moviendo NZB a NZB inbox (Move to NZB inbox)")
			emitProgress(99)
			_, err = moveNZBStagingToFinal(stagingNZB, finalNZB)
			if err != nil {
				msg := err.Error()
				_ = r.jobs.AppendLog(ctx, j.ID, "ERROR: move nzb: "+msg)
				_ = r.jobs.SetFailed(ctx, j.ID, msg)
				return
			}
			emitProgress(100)
			_ = r.jobs.SetDone(ctx, j.ID)
			return
		}

		_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("exec upload (dev dummy): %s", p.Path))
		err = runCommand(ctx, func(line string) {
			_ = r.jobs.AppendLog(ctx, j.ID, line)
		}, "bash", "-lc", fmt.Sprintf("echo uploading '%s'; sleep 2; echo done upload", p.Path))
		if err != nil {
			msg := err.Error()
			_ = r.jobs.AppendLog(ctx, j.ID, "ERROR: "+msg)
			_ = r.jobs.SetFailed(ctx, j.ID, msg)
			return
		}
		_ = r.jobs.SetDone(ctx, j.ID)
		return
	}

	_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("(stub) would upload media via ngpost: %s", p.Path))
	_ = r.jobs.SetDone(ctx, j.ID)
}

// moveNZBStagingToFinal moves a staging NZB into the RAW directory only after it is complete.
// It tries to behave atomically at the destination by writing to a temp file then renaming.
func moveNZBStagingToFinal(stagingPath, finalPath string) (string, error) {
	if strings.TrimSpace(stagingPath) == "" || strings.TrimSpace(finalPath) == "" {
		return "", fmt.Errorf("staging and final paths required")
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return "", err
	}

	// Choose a unique final path if it already exists.
	dest := finalPath
	if _, err := os.Stat(dest); err == nil {
		ext := filepath.Ext(finalPath)
		base := strings.TrimSuffix(finalPath, ext)
		for i := 2; i < 1000; i++ {
			cand := fmt.Sprintf("%s_%d%s", base, i, ext)
			if _, err := os.Stat(cand); os.IsNotExist(err) {
				dest = cand
				break
			}
		}
	}

	// Best effort atomic move. If cross-device, do copy+rename.
	if err := os.Rename(stagingPath, dest); err == nil {
		return dest, nil
	} else {
		// Copy to tmp in destination dir, then rename.
		tmp := dest + ".tmp"
		_ = os.Remove(tmp)
		if err := copyFile(stagingPath, tmp); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, dest); err != nil {
			return "", err
		}
		_ = os.Remove(stagingPath)
		return dest, nil
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	return out.Close()
}

func (r *Runner) persistParNZB(ctx context.Context, jobID string, cfg config.Config, outDir, finalNZB, parStagingNZB string) {
	if strings.TrimSpace(parStagingNZB) == "" || strings.TrimSpace(cfg.Upload.Par.Dir) == "" {
		return
	}
	relDir, err := filepath.Rel(outDir, filepath.Dir(finalNZB))
	if err != nil {
		relDir = ""
	}
	keepDir := filepath.Join(strings.TrimSpace(cfg.Upload.Par.Dir), relDir)
	_ = os.MkdirAll(keepDir, 0o755)
	stem := strings.TrimSuffix(filepath.Base(finalNZB), filepath.Ext(finalNZB))
	finalParNZB := filepath.Join(keepDir, stem+".par2.nzb")
	if _, err := moveNZBStagingToFinal(parStagingNZB, finalParNZB); err != nil {
		_ = r.jobs.AppendLog(ctx, jobID, "WARN: move par2 nzb: "+err.Error())
		return
	}
	_ = r.jobs.AppendLog(ctx, jobID, "par: kept par2 nzb in "+finalParNZB)
}

func (r *Runner) par2Binary() string {
	candidates := []string{}
	if p := strings.TrimSpace(r.Par2Path); p != "" {
		candidates = append(candidates, p)
	}
	candidates = append(candidates, "/usr/local/bin/par2j64", "/usr/local/bin/par2", "par2")
	for _, c := range candidates {
		if strings.TrimSpace(c) == "" {
			continue
		}
		if strings.HasPrefix(c, "/") {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				return c
			}
			continue
		}
		if p, err := exec.LookPath(c); err == nil && strings.TrimSpace(p) != "" {
			return p
		}
	}
	return "par2"
}

// par2RepairBinary prefers distro/classic par2 for repair tests.
// If unavailable, it falls back to the configured/default par2 binary.
func (r *Runner) par2RepairBinary() string {
	if st, err := os.Stat("/usr/bin/par2"); err == nil && !st.IsDir() {
		return "/usr/bin/par2"
	}
	if p, err := exec.LookPath("/usr/bin/par2"); err == nil && strings.TrimSpace(p) != "" {
		return p
	}
	return r.par2Binary()
}

func collectUploadFiles(inputPath string) ([]string, error) {
	st, err := os.Stat(inputPath)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return []string{inputPath}, nil
	}
	files := make([]string, 0, 64)
	err = filepath.WalkDir(inputPath, func(fp string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, fp)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func writeNyuuClassicConfig(cacheDir, jobID string, ng config.NgPost) (string, error) {
	dir := filepath.Join(cacheDir, "nyuu-config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s.json", jobID))
	cfg := map[string]any{
		"host":               ng.Host,
		"port":               ng.Port,
		"ssl":                ng.SSL,
		"user":               ng.User,
		"password":           ng.Pass,
		"connections":        ng.Connections,
		"groups":             ng.Groups,
		"nzb-subject":        "{filename}",
		"subdirs":            "include",
		"overwrite":          true,
		"obfuscate-articles": true,
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func detectSeasonFromName(name string) int {
	m := reSeasonNum.FindStringSubmatch(name)
	if len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func stripSeasonFromName(name string) string {
	clean := reSeasonNum.ReplaceAllString(name, "")
	clean = strings.ReplaceAll(clean, "()", "")
	clean = strings.Join(strings.Fields(clean), " ")
	clean = strings.Trim(clean, " -_.")
	return clean
}

func detectSeasonFromDir(path string) int {
	base := filepath.Base(path)
	if n := detectSeasonFromName(base); n > 0 {
		return n
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if e.IsDir() {
			if n := detectSeasonFromName(e.Name()); n > 0 {
				return n
			}
			continue
		}
		if n := detectSeasonFromName(e.Name()); n > 0 {
			return n
		}
		if m := reEpisodeNum.FindString(e.Name()); m != "" {
			if strings.Contains(strings.ToLower(m), "x") {
				parts := strings.Split(strings.ToLower(m), "x")
				if len(parts) == 2 {
					if n, err := strconv.Atoi(parts[0]); err == nil && n > 0 {
						return n
					}
				}
			} else if strings.HasPrefix(strings.ToLower(m), "s") {
				m = strings.TrimPrefix(strings.ToLower(m), "s")
				if idx := strings.Index(m, "e"); idx > 0 {
					if n, err := strconv.Atoi(m[:idx]); err == nil && n > 0 {
						return n
					}
				}
			}
		}
	}
	return 0
}

func buildRawNZBPath(cfg config.Config, inputPath, rawRoot, qualityHint string) string {
	if strings.TrimSpace(rawRoot) == "" {
		rawRoot = "/host/inbox/nzb"
	}
	base := filepath.Base(inputPath)
	g := library.GuessFromFilename(base)
	// normalize quality to 1080/2160
	q := strings.ToLower(strings.TrimSpace(qualityHint))
	if q == "" {
		q = strings.ToLower(strings.TrimSpace(g.Quality))
	}
	quality := "1080"
	if q == "4k" || strings.Contains(q, "2160") {
		quality = "2160"
	}

	// helpers
	safe := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.ReplaceAll(s, string(filepath.Separator), "-")
		s = strings.ReplaceAll(s, ":", "-")
		s = strings.Join(strings.Fields(s), " ")
		return s
	}

	l := cfg.Library.Defaults()

	// If inputPath is a directory, treat it as series content (season pack or series folder).
	isDir := false
	if st, err := os.Stat(inputPath); err == nil {
		isDir = st.IsDir()
	}
	if isDir {
		g.IsSeries = true
	}

	if g.IsSeries {
		seriesTitle := strings.TrimSpace(g.Title)
		if isDir {
			baseName := filepath.Base(inputPath)
			seriesTitle = strings.TrimSpace(stripSeasonFromName(baseName))
			if seriesTitle == "" {
				parent := filepath.Base(filepath.Dir(inputPath))
				seriesTitle = strings.TrimSpace(stripSeasonFromName(parent))
			}
		}
		if seriesTitle == "" {
			seriesTitle = g.Title
		}
		seriesName := safe(seriesTitle)
		year := g.Year
		if year <= 0 {
			res := library.NewResolver(cfg)
			if tv, ok := res.ResolveTV(context.Background(), seriesName, 0); ok {
				if y := tv.FirstAirYear(); y > 0 {
					year = y
				}
			}
		}
		yearPart := ""
		if year > 0 {
			if !strings.Contains(strings.ToLower(seriesName), fmt.Sprintf("(%d)", year)) {
				yearPart = fmt.Sprintf(" (%d)", year)
			}
		}
		initial := library.InitialFolder(seriesName)
		if strings.TrimSpace(initial) == "" || len([]rune(initial)) != 1 || (initial[0] < 'A' || initial[0] > 'Z') {
			initial = "#"
		}
		seriesFolder := safe(seriesName + yearPart)

		fileName := ""
		if isDir {
			season := detectSeasonFromDir(inputPath)
			if season <= 0 {
				season = detectSeasonFromName(filepath.Base(inputPath))
			}
			if season > 0 {
				fileName = fmt.Sprintf("%s%s - Temporada %d.nzb", safe(seriesName), yearPart, season)
			} else {
				fileName = fmt.Sprintf("%s%s.nzb", safe(seriesName), yearPart)
			}
		} else if g.Season > 0 && g.Episode > 0 {
			fileName = fmt.Sprintf("%s%s %02dx%02d.nzb", safe(seriesName), yearPart, g.Season, g.Episode)
		} else {
			fileName = fmt.Sprintf("%s%s.nzb", safe(seriesName), yearPart)
		}

		// NZB layout for series: SERIES/A/.../Serie (Año)/<file>.nzb
		rel := filepath.Join(l.SeriesRoot, initial, seriesFolder, fileName)
		if cfg.Library.UppercaseFolders {
			rel = library.ApplyUppercaseFolders(rel)
		}
		return filepath.Join(rawRoot, rel)
	}

	movieTitle := safe(g.Title)
	year := g.Year
	yearPart := ""
	if year > 0 {
		// Avoid duplicating year when title already includes "(YYYY)".
		if !strings.Contains(strings.ToLower(movieTitle), fmt.Sprintf("(%d)", year)) {
			yearPart = fmt.Sprintf(" (%d)", year)
		}
	}
	movieFolder := safe(movieTitle + yearPart)
	fileName := movieFolder + ".nzb"

	initial := library.InitialFolder(movieTitle)
	if strings.TrimSpace(initial) == "" || len([]rune(initial)) != 1 || (initial[0] < 'A' || initial[0] > 'Z') {
		initial = "#"
	}
	// NZB files: keep them directly under .../<Initial>/ (no extra movie folder).
	// The FUSE/library view can still expose movie folders for MKVs.
	rel := filepath.Join(l.MoviesRoot, quality, initial, fileName)
	if cfg.Library.UppercaseFolders {
		rel = library.ApplyUppercaseFolders(rel)
	}
	return filepath.Join(rawRoot, rel)
}
