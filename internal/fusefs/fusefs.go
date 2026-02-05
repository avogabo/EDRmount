package fusefs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"
)

type MountOptions struct {
	Mountpoint string
	AllowOther bool
}

type Mount struct {
	conn *fuse.Conn
}

func (m *Mount) Close() error {
	if m.conn != nil {
		return m.conn.Close()
	}
	return nil
}

func Start(ctx context.Context, opts MountOptions, filesystem fs.FS) (*Mount, error) {
	if opts.Mountpoint == "" {
		return nil, fmt.Errorf("mountpoint required")
	}
	if err := os.MkdirAll(opts.Mountpoint, 0o755); err != nil {
		return nil, err
	}
	mountOpts := []fuse.MountOption{
		fuse.ReadOnly(),
		fuse.FSName("edrmount"),
		fuse.Subtype("edrmount"),
	}
	if opts.AllowOther {
		mountOpts = append(mountOpts, fuse.AllowOther())
	}
	c, err := fuse.Mount(opts.Mountpoint, mountOpts...)
	if err != nil {
		return nil, err
	}
	m := &Mount{conn: c}
	go func() {
		_ = fs.Serve(c, filesystem)
	}()
	go func() {
		<-ctx.Done()
		_ = c.Close()
	}()
	return m, nil
}

func MountRaw(ctx context.Context, cfg config.Config, jobs *jobs.Store) (*Mount, error) {
	mp := filepath.Join(cfg.Paths.MountPoint, "raw")
	rfs := &RawFS{Cfg: cfg, Jobs: jobs}
	return Start(ctx, MountOptions{Mountpoint: mp, AllowOther: true}, rfs)
}

func MountLibrary(ctx context.Context, cfg config.Config, jobs *jobs.Store) (*Mount, error) {
	mp := filepath.Join(cfg.Paths.MountPoint, "library")
	lfs := &LibraryFS{Cfg: cfg, Jobs: jobs}
	return Start(ctx, MountOptions{Mountpoint: mp, AllowOther: true}, lfs)
}
