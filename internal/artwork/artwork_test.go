package artwork

import (
	"bytes"
	"context"
	_ "embed"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

//go:embed testdata/single-color-test-image.png
var singleColorTestImage []byte

func TestStoreSaveReplaceAndServe(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(fileStorage{dir: dir})
	first := singleColorTestImage
	second := append([]byte(nil), singleColorTestImage...)

	key1 := saveBytes(t, s, first)
	key2 := saveBytes(t, s, second)
	if key1 == key2 || key1 == "" || key2 == "" {
		t.Fatal("expected unique artwork keys")
	}
	if err := s.Remove(context.Background(), key1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, key1)); !os.IsNotExist(err) {
		t.Fatalf("old artwork still exists: %v", err)
	}

	req := httptest.NewRequest("GET", "/media/artwork/"+key2, nil)
	rec := httptest.NewRecorder()
	s.Serve(rec, req, key2)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") == "" || len(rec.Body.Bytes()) == 0 {
		t.Fatalf("serve artwork: %d, %v", rec.Code, rec.Header())
	}
}

func TestStoreRejectsInvalidAndOversizedArtwork(t *testing.T) {
	s := NewStore(fileStorage{dir: t.TempDir()})
	if _, err := s.Save(context.Background(), file(t, []byte("not an image"))); err != ErrInvalid {
		t.Fatalf("invalid artwork error = %v", err)
	}
	if _, err := s.Save(context.Background(), file(t, bytes.Repeat([]byte("x"), MaxSize+1))); err != ErrInvalid {
		t.Fatalf("oversized artwork error = %v", err)
	}
}

type fileStorage struct{ dir string }

func (s fileStorage) Put(_ context.Context, key string, data []byte, _ string) error {
	return os.WriteFile(filepath.Join(s.dir, key), data, 0o600)
}
func (s fileStorage) Delete(_ context.Context, key string) error {
	return os.Remove(filepath.Join(s.dir, key))
}
func (s fileStorage) Open(_ context.Context, key string) (Object, error) {
	f, err := os.Open(filepath.Join(s.dir, key))
	return Object{ReadSeekCloser: f}, err
}
func (s fileStorage) PublicURL(string) string { return "" }

func saveBytes(t *testing.T, s *Store, data []byte) string {
	t.Helper()
	key, err := s.Save(context.Background(), file(t, data))
	if err != nil {
		t.Fatal(err)
	}
	return key
}
func file(t *testing.T, data []byte) io.Reader {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "upload")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	return f
}
