package artwork

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const MaxSize = 10 << 20

var ErrInvalid = errors.New("artwork must be a valid JPEG, PNG, or WebP image no larger than 10 MB")

type Store struct{ dir string }

func NewStore(dir string) *Store { return &Store{dir: dir} }

func (s *Store) Save(file multipart.File) (string, error) {
	data, err := io.ReadAll(io.LimitReader(file, MaxSize+1))
	if err != nil || len(data) > MaxSize || !valid(data) {
		return "", ErrInvalid
	}

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", fmt.Errorf("create artwork directory: %w", err)
	}
	var nameBytes [16]byte
	if _, err := rand.Read(nameBytes[:]); err != nil {
		return "", fmt.Errorf("generate artwork name: %w", err)
	}
	ext := ".webp"
	if len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) {
		ext = ".png"
	} else if len(data) >= 2 && bytes.Equal(data[:2], []byte("\xff\xd8")) {
		ext = ".jpg"
	}
	name := hex.EncodeToString(nameBytes[:]) + ext
	tmp, err := os.CreateTemp(s.dir, ".upload-*")
	if err != nil {
		return "", fmt.Errorf("create artwork temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write artwork: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close artwork: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(s.dir, name)); err != nil {
		return "", fmt.Errorf("store artwork: %w", err)
	}
	return name, nil
}

func (s *Store) Remove(key string) error {
	if !safeKey(key) {
		return nil
	}
	err := os.Remove(filepath.Join(s.dir, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) Serve(w http.ResponseWriter, r *http.Request, key string) {
	if !safeKey(key) {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(s.dir, key))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, key, fileModTime(f), f)
}

func fileModTime(f *os.File) (t time.Time) {
	info, _ := f.Stat()
	if info != nil {
		return info.ModTime()
	}
	return
}

func safeKey(key string) bool {
	return key != "" && filepath.Base(key) == key && !strings.Contains(key, "\\")
}

func valid(data []byte) bool {
	if _, format, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		return format == "jpeg" || format == "png"
	}
	return validWebP(data)
}

func validWebP(data []byte) bool {
	if len(data) < 20 || !bytes.Equal(data[:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WEBP")) {
		return false
	}
	declaredSize := int(binary.LittleEndian.Uint32(data[4:8]))
	if declaredSize+8 > len(data) || declaredSize < 12 {
		return false
	}
	chunk := data[12:16]
	if !bytes.Equal(chunk, []byte("VP8 ")) && !bytes.Equal(chunk, []byte("VP8L")) && !bytes.Equal(chunk, []byte("VP8X")) {
		return false
	}
	chunkSize := int(binary.LittleEndian.Uint32(data[16:20]))
	return chunkSize > 0 && 20+chunkSize <= len(data)
}
