package backup

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Config struct {
	Enabled    bool
	Dir        string
	EveryMins  int
	Keep       int
	CompressGZ bool
}

type Item struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Time string `json:"time"`
}

func ensureDir(p string) error { return os.MkdirAll(p, 0o755) }

// RunOnce creates a consistent SQLite snapshot using VACUUM INTO.
// It returns the created filename (full path).
func RunOnce(ctx context.Context, dbPath string, backupDir string, compress bool) (string, error) {
	if backupDir == "" {
		backupDir = "/backups"
	}
	if err := ensureDir(backupDir); err != nil {
		return "", err
	}

	ts := time.Now().Format("20060102-150405")
	base := fmt.Sprintf("edrmount.db.%s.sqlite", ts)
	out := filepath.Join(backupDir, base)
	if compress {
		out += ".gz"
	}

	// Create temp uncompressed target (VACUUM INTO requires non-existing file).
	tmp := filepath.Join(backupDir, fmt.Sprintf(".tmp.%s", base))
	_ = os.Remove(tmp)

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(30000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", err
	}
	defer db.Close()

	// Best-effort checkpoint first.
	_, _ = db.ExecContext(ctx, `PRAGMA wal_checkpoint(FULL);`)

	// Snapshot.
	q := fmt.Sprintf("VACUUM INTO %s", quoteSQLString(tmp))
	if _, err := db.ExecContext(ctx, q); err != nil {
		return "", err
	}

	if !compress {
		if err := os.Rename(tmp, out); err != nil {
			return "", err
		}
		return out, nil
	}

	// gzip
	fIn, err := os.Open(tmp)
	if err != nil {
		return "", err
	}
	defer fIn.Close()
	fOut, err := os.Create(out)
	if err != nil {
		return "", err
	}
	gz := gzip.NewWriter(fOut)
	gz.Name = filepath.Base(tmp)
	_, err = io.Copy(gz, fIn)
	cerr := gz.Close()
	_ = fOut.Close()
	_ = os.Remove(tmp)
	if err != nil {
		return "", err
	}
	if cerr != nil {
		return "", cerr
	}
	return out, nil
}

func RestoreFrom(ctx context.Context, backupFile string, dbPath string) error {
	// Write into place atomically via temp.
	dir := filepath.Dir(dbPath)
	if err := ensureDir(dir); err != nil {
		return err
	}
	tmp := dbPath + ".restore.tmp"
	_ = os.Remove(tmp)

	in, err := os.Open(backupFile)
	if err != nil {
		return err
	}
	defer in.Close()

	var r io.Reader = in
	if strings.HasSuffix(backupFile, ".gz") {
		gz, err := gzip.NewReader(in)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	}

	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	// Remove WAL/SHM; restored DB will start fresh.
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")

	return os.Rename(tmp, dbPath)
}

func List(backupDir string) ([]Item, error) {
	if backupDir == "" {
		backupDir = "/backups"
	}
	ents, err := os.ReadDir(backupDir)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0)
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "edrmount.db.") {
			continue
		}
		if !(strings.HasSuffix(name, ".sqlite") || strings.HasSuffix(name, ".sqlite.gz")) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, Item{Name: name, Size: info.Size(), Time: info.ModTime().Format(time.RFC3339)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Time > items[j].Time })
	return items, nil
}

func Rotate(backupDir string, keep int) {
	if keep <= 0 {
		keep = 30
	}
	items, err := List(backupDir)
	if err != nil {
		return
	}
	for i := keep; i < len(items); i++ {
		_ = os.Remove(filepath.Join(backupDir, items[i].Name))
	}
}

func quoteSQLString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
