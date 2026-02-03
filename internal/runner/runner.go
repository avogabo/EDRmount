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
}

func New(j *jobs.Store) *Runner {
	return &Runner{jobs: j, UploadConcurrency: 2, PollInterval: 1 * time.Second}
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
	_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("(stub) would import NZB: %s", p.Path))
	_ = r.jobs.SetDone(ctx, j.ID)
}

func (r *Runner) runUpload(ctx context.Context, j *jobs.Job) {
	_ = r.jobs.AppendLog(ctx, j.ID, "starting upload job")
	var p struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(j.Payload, &p)
	_ = r.jobs.AppendLog(ctx, j.ID, fmt.Sprintf("(stub) would upload media via ngpost: %s", p.Path))
	_ = r.jobs.SetDone(ctx, j.ID)
}
