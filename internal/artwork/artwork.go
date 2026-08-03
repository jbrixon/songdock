package artwork

import (
	"bytes"
	"context"
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
	"net/url"
	"strings"
	"time"
)

const MaxSize = 10 << 20

var ErrInvalid = errors.New("artwork must be a valid JPEG, PNG, or WebP image no larger than 10 MB")

// Storage is the port used by the artwork application service. Adapters can
// implement it for local files, object storage, or any other backing store.
type Storage interface {
	Put(context.Context, string, []byte, string) error
	Delete(context.Context, string) error
	Open(context.Context, string) (Object, error)
	PublicURL(string) string
}

type Object struct {
	io.ReadSeekCloser
	ModTime     time.Time
	ContentType string
}

type Store struct{ storage Storage }

func NewStore(storage Storage) *Store { return &Store{storage: storage} }

func (s *Store) Save(ctx context.Context, file io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(file, MaxSize+1))
	if err != nil || len(data) > MaxSize || !valid(data) {
		return "", ErrInvalid
	}

	var nameBytes [16]byte
	if _, err := rand.Read(nameBytes[:]); err != nil {
		return "", fmt.Errorf("generate artwork name: %w", err)
	}
	ext := ".webp"
	contentType := "image/webp"
	if len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) {
		ext = ".png"
		contentType = "image/png"
	} else if len(data) >= 2 && bytes.Equal(data[:2], []byte("\xff\xd8")) {
		ext = ".jpg"
		contentType = "image/jpeg"
	}
	name := hex.EncodeToString(nameBytes[:]) + ext
	if err := s.storage.Put(ctx, name, data, contentType); err != nil {
		return "", fmt.Errorf("store artwork: %w", err)
	}
	return name, nil
}

func (s *Store) Remove(ctx context.Context, key string) error {
	if !safeKey(key) {
		return nil
	}
	return s.storage.Delete(ctx, key)
}

func (s *Store) URL(key string) string {
	if key == "" {
		return "/static/song_artwork_placeholder.png"
	}
	if publicURL := s.storage.PublicURL(key); publicURL != "" {
		return publicURL
	}
	return "/media/artwork/" + url.PathEscape(key)
}

func (s *Store) Serve(w http.ResponseWriter, r *http.Request, key string) {
	if !safeKey(key) {
		http.NotFound(w, r)
		return
	}
	object, err := s.storage.Open(r.Context(), key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer object.Close()
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if object.ContentType != "" {
		w.Header().Set("Content-Type", object.ContentType)
	}
	http.ServeContent(w, r, key, object.ModTime, object)
}

func safeKey(key string) bool {
	return ValidKey(key)
}

func ValidKey(key string) bool {
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
