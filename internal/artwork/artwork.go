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
	"net/http"
	"strings"
	"time"
)

const MaxSize = 10 << 20

var ErrInvalid = errors.New("artwork must be a valid JPEG, PNG, or WebP image no larger than 10 MB")

// Storage is the port used by the artwork application service. Adapters can
// implement it for local files, object storage, or any other backing store.
type Storage interface {
	Put(key string, data []byte) error
	Delete(key string) error
	Open(key string) (io.ReadSeekCloser, error)
	ModTime(key string) (time.Time, error)
}

type Store struct{ storage Storage }

func NewStore(storage Storage) *Store { return &Store{storage: storage} }

func (s *Store) Save(file io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(file, MaxSize+1))
	if err != nil || len(data) > MaxSize || !valid(data) {
		return "", ErrInvalid
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
	if err := s.storage.Put(name, data); err != nil {
		return "", fmt.Errorf("store artwork: %w", err)
	}
	return name, nil
}

func (s *Store) Remove(key string) error {
	if !safeKey(key) {
		return nil
	}
	return s.storage.Delete(key)
}

func (s *Store) Serve(w http.ResponseWriter, r *http.Request, key string) {
	if !safeKey(key) {
		http.NotFound(w, r)
		return
	}
	f, err := s.storage.Open(key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	modTime, _ := s.storage.ModTime(key)
	http.ServeContent(w, r, key, modTime, f)
}

func safeKey(key string) bool {
	return key != "" && !strings.ContainsAny(key, `/\\`) && key != "." && key != ".."
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
