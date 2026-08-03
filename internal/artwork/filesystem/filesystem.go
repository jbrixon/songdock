package filesystem

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type Storage struct{ dir string }

func New(dir string) *Storage { return &Storage{dir: dir} }

func (s *Storage) Put(key string, data []byte) error {
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

func (s *Storage) Delete(key string) error {
	err := os.Remove(filepath.Join(s.dir, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Storage) Open(key string) (io.ReadSeekCloser, error) {
	return os.Open(filepath.Join(s.dir, key))
}

func (s *Storage) ModTime(key string) (t time.Time, err error) {
	info, err := os.Stat(filepath.Join(s.dir, key))
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

func fmtError(operation string, err error) error {
	return fmt.Errorf("%s: %w", operation, err)
}
