package artwork

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSaveReplaceAndServe(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	first := jpegBytes(t, color.RGBA{R: 255, A: 255})
	second := jpegBytes(t, color.RGBA{B: 255, A: 255})

	key1 := saveBytes(t, s, first)
	key2 := saveBytes(t, s, second)
	if key1 == key2 || key1 == "" || key2 == "" {
		t.Fatal("expected unique artwork keys")
	}
	if err := s.Remove(key1); err != nil {
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
	s := NewStore(t.TempDir())
	if _, err := s.Save(file(t, []byte("not an image"))); err != ErrInvalid {
		t.Fatalf("invalid artwork error = %v", err)
	}
	if _, err := s.Save(file(t, bytes.Repeat([]byte("x"), MaxSize+1))); err != ErrInvalid {
		t.Fatalf("oversized artwork error = %v", err)
	}
}

func saveBytes(t *testing.T, s *Store, data []byte) string {
	t.Helper()
	key, err := s.Save(file(t, data))
	if err != nil {
		t.Fatal(err)
	}
	return key
}
func file(t *testing.T, data []byte) multipart.File {
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
func jpegBytes(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewUniform(c), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
