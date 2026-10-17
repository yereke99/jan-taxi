package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// keyRe is the only storage key shape SaveImage and SavePDF produce. Keys
// read back from the database are checked against it before touching the
// filesystem.
var keyRe = regexp.MustCompile(`^[0-9]{4}/[0-9]{2}/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.(jpg|pdf)$`)

var errBadKey = errors.New("invalid storage key")

const tmpPrefix = ".tmp-"

// writeAtomic writes data to key through a temporary file in the same
// directory and renames it into place. All access goes through os.Root, which
// refuses paths (including symlinks) that escape Dir.
func (s *Store) writeAtomic(key string, data []byte) (err error) {
	if !keyRe.MatchString(key) {
		return errBadKey
	}
	root, err := os.OpenRoot(s.Dir)
	if err != nil {
		return err
	}
	defer root.Close()

	dir := filepath.FromSlash(path.Dir(key))
	if err := root.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(dir, tmpPrefix+uuid.NewString())
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = root.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = root.Rename(tmp, filepath.FromSlash(key)); err != nil {
		return err
	}
	// Persist the directory entry; not every platform supports syncing a
	// directory, so a failure here is not fatal.
	if d, derr := root.Open(dir); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func (s *Store) openKey(key string) (io.ReadCloser, error) {
	if !keyRe.MatchString(key) {
		return nil, errBadKey
	}
	root, err := os.OpenRoot(s.Dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(filepath.FromSlash(key))
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("not a regular file")
	}
	return f, nil
}

func (s *Store) remove(key string) error {
	if !keyRe.MatchString(key) {
		return errBadKey
	}
	root, err := os.OpenRoot(s.Dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Remove(filepath.FromSlash(key))
}

// RemoveOrphans deletes files older than minAge that have no files row
// (left behind when the inserting transaction rolled back) and stale
// temporary files from interrupted writes. It returns how many were removed.
func (s *Store) RemoveOrphans(ctx context.Context, q db.Querier, minAge time.Duration) (int, error) {
	cutoff := time.Now().Add(-minAge)
	var candidates []string
	var stale []string
	err := filepath.WalkDir(s.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || ctx.Err() != nil {
			return ctx.Err()
		}
		info, err := d.Info()
		if err != nil || info.ModTime().After(cutoff) {
			return nil
		}
		rel, err := filepath.Rel(s.Dir, p)
		if err != nil {
			return nil
		}
		key := filepath.ToSlash(rel)
		switch {
		case keyRe.MatchString(key):
			candidates = append(candidates, key)
		case strings.HasPrefix(d.Name(), tmpPrefix):
			stale = append(stale, key)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("storage: scan: %w", err)
	}

	root, err := os.OpenRoot(s.Dir)
	if err != nil {
		return 0, err
	}
	defer root.Close()

	removed := 0
	for _, key := range stale {
		if root.Remove(filepath.FromSlash(key)) == nil {
			removed++
		}
	}
	const batch = 500
	for start := 0; start < len(candidates); start += batch {
		end := min(start+batch, len(candidates))
		keys := candidates[start:end]
		known, err := existingKeys(ctx, q, keys)
		if err != nil {
			return removed, err
		}
		for _, key := range keys {
			if known[key] {
				continue
			}
			if root.Remove(filepath.FromSlash(key)) == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// existingKeys returns which keys have a files row (deleted or not: soft
// deleted files are kept for audit until a retention policy removes them).
func existingKeys(ctx context.Context, q db.Querier, keys []string) (map[string]bool, error) {
	rows, err := q.Query(ctx, `SELECT storage_key FROM files WHERE storage_key = ANY($1)`, keys)
	if err != nil {
		return nil, fmt.Errorf("storage: orphan lookup: %w", err)
	}
	defer rows.Close()
	out := make(map[string]bool, len(keys))
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}
