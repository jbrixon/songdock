package filesystem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jbrixon/songdock/internal/artwork"
)

type Storage struct{ dir string }

func New(dir string) *Storage { return &Storage{dir: dir} }

func (s *Storage) Put(ctx context.Context, key string, data []byte, _ string) error {
	if !artwork.ValidKey(key) {
		return fmt.Errorf("invalid artwork key %q", key)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmtError("create artwork directory", err)
	}
	tmp, err := os.CreateTemp(s.dir, ".upload-*")
	if err != nil {
		return fmtError("create artwork temp file", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmtError("write artwork", err)
	}
	if err := tmp.Close(); err != nil {
		return fmtError("close artwork", err)
	}
	if err := os.Rename(tmpName, filepath.Join(s.dir, key)); err != nil {
		return fmtError("store artwork", err)
	}
	return nil
}

func (s *Storage) Delete(ctx context.Context, key string) error {
	if !artwork.ValidKey(key) {
		return fmt.Errorf("invalid artwork key %q", key)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(s.dir, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Storage) Open(ctx context.Context, key string) (artwork.Object, error) {
	if !artwork.ValidKey(key) {
		return artwork.Object{}, fmt.Errorf("invalid artwork key %q", key)
	}
	if err := ctx.Err(); err != nil {
		return artwork.Object{}, err
	}
	f, err := os.Open(filepath.Join(s.dir, key))
	if err != nil {
		return artwork.Object{}, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return artwork.Object{}, err
	}
	return artwork.Object{ReadSeekCloser: f, ModTime: info.ModTime()}, nil
}

func (s *Storage) PublicURL(string) string { return "" }

func fmtError(operation string, err error) error {
	return fmt.Errorf("%s: %w", operation, err)
}
