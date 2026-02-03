package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"
)

type Runner struct {
	jobs *jobs.Store

	UploadConcurrency int
	PollInterval      time.Duration
	Mode              string // "stub" or "exec" (dev)

	NgPostPath string // default: /usr/local/bin/ngpost
	NgPost     config.NgPost
}

func New(j *jobs.Store) *Runner {
	return &Runner{jobs: j, UploadConcurrency: 2, PollInterval: 1 * time.Second, Mode: "stub", NgPostPath: "/usr/local/bin/ngpost"}
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

	if r.Mode == "exec" {
		_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("exec import (dev): %s", p.Path))
		err := runCommand(ctx, func(line string) {
			_ = r.jobs.AppendLog(ctx, j.ID, line)
		}, "bash", "-lc", fmt.Sprintf("echo importing '%s'; sleep 1; echo done import", p.Path))
		if err != nil {
			msg := err.Error()
			_ = r.jobs.AppendLog(ctx, j.ID, "ERROR: "+msg)
			_ = r.jobs.SetFailed(ctx, j.ID, msg)
			return
		}
		_ = r.jobs.SetDone(ctx, j.ID)
		return
	}

	_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("(stub) would import NZB: %s", p.Path))
	_ = r.jobs.SetDone(ctx, j.ID)
}

func (r *Runner) runUpload(ctx context.Context, j *jobs.Job) {
	_ = r.jobs.AppendLog(ctx, j.ID, "starting upload job")
	var p struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(j.Payload, &p)

	if r.Mode == "exec" {
		// If ngpost is enabled and configured, run it; otherwise run a dev dummy command.
		if r.NgPost.Enabled && r.NgPost.Host != "" && r.NgPost.User != "" && r.NgPost.Pass != "" {
			outDir := r.NgPost.OutputDir
			if outDir == "" {
				outDir = "/host/inbox/nzb"
			}
			base := strings.TrimSuffix(filepath.Base(p.Path), filepath.Ext(p.Path))
			outNZB := filepath.Join(outDir, base+".nzb")

			args := []string{"-i", p.Path, "-o", outNZB, "-h", r.NgPost.Host, "-P", fmt.Sprintf("%d", r.NgPost.Port)}
			if r.NgPost.SSL {
				args = append(args, "-s")
			}
			if r.NgPost.Connections > 0 {
				args = append(args, "-n", fmt.Sprintf("%d", r.NgPost.Connections))
			}
			if r.NgPost.Threads > 0 {
				args = append(args, "-t", fmt.Sprintf("%d", r.NgPost.Threads))
			}
			if r.NgPost.Groups != "" {
				args = append(args, "-g", r.NgPost.Groups)
			}
			if r.NgPost.Obfuscate {
				args = append(args, "-x")
			}
			if r.NgPost.TmpDir != "" {
				args = append(args, "--tmp_dir", r.NgPost.TmpDir)
			}
			args = append(args, "-u", r.NgPost.User, "-p", r.NgPost.Pass, "--disp_progress", "files")

			_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("ngpost: %s %s", r.NgPostPath, strings.Join(args[:min(10, len(args))], " ")))
			err := runCommand(ctx, func(line string) {
				_ = r.jobs.AppendLog(ctx, j.ID, sanitizeLine(line, r.NgPost.Pass))
			}, r.NgPostPath, args...)
			if err != nil {
				msg := err.Error()
				_ = r.jobs.AppendLog(ctx, j.ID, "ERROR: "+msg)
				_ = r.jobs.SetFailed(ctx, j.ID, msg)
				return
			}
			_ = r.jobs.SetDone(ctx, j.ID)
			// Chain import
			_, _ = r.jobs.Enqueue(ctx, jobs.TypeImport, map[string]string{"path": outNZB})
			return
		}

		_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("exec upload (dev dummy): %s", p.Path))
		err := runCommand(ctx, func(line string) {
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
