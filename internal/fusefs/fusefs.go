package fusefs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"

	"golang.org/x/sys/unix"
)

type MountOptions struct {
	Mountpoint string
	AllowOther bool
}

type Mount struct {
	server *fuse.Server
}

func (m *Mount) Close() error {
	if m.server != nil {
		return m.server.Unmount()
	}
	return nil
}

func Start(ctx context.Context, opts MountOptions, root fs.InodeEmbedder) (*Mount, error) {
	if opts.Mountpoint == "" {
		return nil, fmt.Errorf("mountpoint required")
	}

	detachStaleMount(opts.Mountpoint)

	if err := os.MkdirAll(opts.Mountpoint, 0o755); err != nil {
		return nil, err
	}
	
	options := &fs.Options{
		MountOptions: fuse.MountOptions{
			AllowOther: opts.AllowOther,
			Options: []string{"ro"},
			Name: "edrmount",
			FsName: "edrmount",
		},
	}
	
	server, err := fs.Mount(opts.Mountpoint, root, options)
	if err != nil {
		return nil, err
	}
	m := &Mount{server: server}
	go func() {
		<-ctx.Done()
		_ = server.Unmount()
	}()
	return m, nil
}

func MountRaw(ctx context.Context, cfg config.Config, jobs *jobs.Store) (*Mount, error) {
	mp := filepath.Join(cfg.Paths.MountPoint, "raw")
	rfs := &rawRoot{Cfg: cfg, Jobs: jobs}
	return Start(ctx, MountOptions{Mountpoint: mp, AllowOther: true}, rfs)
}

func MountLibraryManual(ctx context.Context, cfg config.Config, jobs *jobs.Store) (*Mount, error) {
	mp := filepath.Join(cfg.Paths.MountPoint, "library-manual")
	mfs := &manualRoot{Cfg: cfg, Jobs: jobs}
	return Start(ctx, MountOptions{Mountpoint: mp, AllowOther: true}, mfs)
}

func MountLibraryAuto(ctx context.Context, cfg config.Config, jobs *jobs.Store) (*Mount, error) {
	mp := filepath.Join(cfg.Paths.MountPoint, "library-auto")
	lfs := &LibraryFS{Cfg: cfg, Jobs: jobs}
	return Start(ctx, MountOptions{Mountpoint: mp, AllowOther: true}, &libDir{fs: lfs, rel: ""})
}

func detachStaleMount(mp string) {
	if strings.TrimSpace(mp) == "" {
		return
	}
	for i := 0; i < 3; i++ {
		_ = unix.Unmount(mp, unix.MNT_DETACH)
		_, _ = exec.Command("fusermount3", "-uz", mp).CombinedOutput()
		_, _ = exec.Command("umount", "-l", mp).CombinedOutput()
		time.Sleep(150 * time.Millisecond)
	}
}
