package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStaticAssetsSetsLongLivedCache(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/static/logo.png", nil)
	rec := httptest.NewRecorder()
	staticAssets(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if got, want := rec.Header().Get("Cache-Control"), "public, max-age=31536000, immutable"; got != want {
		t.Fatalf("Cache-Control = %q, want %q", got, want)
	}
}
