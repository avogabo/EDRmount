package runner

import (
	"context"
	"encoding/json"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"
)

func (r *Runner) runHealth(ctx context.Context, j *jobs.Job) {
	_ = r.jobs.AppendLog(ctx, j.ID, "starting health repair job")

	cfg := config.Default()
	if r.GetConfig != nil {
		cfg = r.GetConfig()
	}

	var p healthRepairPayload
	_ = json.Unmarshal(j.Payload, &p)

	if err := r.runHealthRepair(ctx, j.ID, cfg, p); err != nil {
		msg := err.Error()
		_ = r.jobs.AppendLog(ctx, j.ID, "ERROR: "+msg)
		_ = r.jobs.SetFailed(ctx, j.ID, msg)
		return
	}

	_ = r.jobs.SetDone(ctx, j.ID)
}
