package fusefs

import (
	"context"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

// This package will implement the raw/library mount.
// For now, it only validates that FUSE deps are wired correctly.

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
	_ = ctx
	_ = filesystem
	c, err := fuse.Mount(
		opts.Mountpoint,
		fuse.FSName("edrmount"),
		fuse.Subtype("edrmount"),
	)
	if err != nil {
		return nil, err
	}
	m := &Mount{conn: c}
	// NOTE: actual fs.Serve wiring comes next.
	return m, nil
}
