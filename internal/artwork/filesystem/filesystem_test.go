package filesystem_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/jbrixon/songdock/internal/artwork"
	"github.com/jbrixon/songdock/internal/artwork/filesystem"
)

func TestStorageRoundTripAndRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	storage := filesystem.New(dir)
	key := "artwork.png"
	if err := storage.Put(context.Background(), key, []byte("image"), "image/png"); err != nil {
		t.Fatal(err)
	}

	object, err := storage.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(object)
	object.Close()
	if err != nil || string(data) != "image" {
		t.Fatalf("read stored artwork = %q, err = %v", data, err)
	}
	if err := storage.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, key)); !os.IsNotExist(err) {
		t.Fatalf("artwork still exists: %v", err)
	}
	if err := storage.Put(context.Background(), "../outside", []byte("bad"), "image/png"); err == nil {
		t.Fatal("traversal key was accepted")
	}
	if !artwork.ValidKey(key) {
		t.Fatal("valid key rejected")
	}
}
