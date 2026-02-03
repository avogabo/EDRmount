package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gaby/EDRmount/internal/jobs"
)

type Runner struct {
	jobs *jobs.Store

	UploadConcurrency int
	PollInterval      time.Duration
	Mode              string // "stub" or "exec" (dev)
}

func New(j *jobs.Store) *Runner {
	return &Runner{jobs: j, UploadConcurrency: 2, PollInterval: 1 * time.Second, Mode: "stub"}
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
		_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("exec upload (dev): %s", p.Path))
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
