package fusefs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/jobs"

	"golang.org/x/sys/unix"
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

	// On container restarts, FUSE mountpoints can be left behind in a disconnected state
	// ("Transport endpoint is not connected"). Best-effort detach any existing mount so
	// we can mount cleanly.
	detachStaleMount(opts.Mountpoint)

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

func MountLibraryManual(ctx context.Context, cfg config.Config, jobs *jobs.Store) (*Mount, error) {
	mp := filepath.Join(cfg.Paths.MountPoint, "library-manual")
	mfs := &ManualFS{Cfg: cfg, Jobs: jobs}
	return Start(ctx, MountOptions{Mountpoint: mp, AllowOther: true}, mfs)
}

func MountLibraryAuto(ctx context.Context, cfg config.Config, jobs *jobs.Store) (*Mount, error) {
	mp := filepath.Join(cfg.Paths.MountPoint, "library-auto")
	lfs := &LibraryFS{Cfg: cfg, Jobs: jobs}
	return Start(ctx, MountOptions{Mountpoint: mp, AllowOther: true}, lfs)
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
