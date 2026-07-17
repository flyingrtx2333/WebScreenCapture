package app

import (
	"crypto/sha256"
	"path/filepath"
	"testing"
)

func TestAccessTokenPersistsAndReloads(t *testing.T) {
	const initialToken = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	path := filepath.Join(t.TempDir(), "access-token.sha256")
	store, err := newAccessTokenStore(sha256.Sum256([]byte(initialToken)), path)
	if err != nil {
		t.Fatal(err)
	}
	if !store.verify(initialToken) {
		t.Fatal("initial token was rejected")
	}

	rotatedToken, err := store.rotate()
	if err != nil {
		t.Fatal(err)
	}
	if len(rotatedToken) != 64 || !store.verify(rotatedToken) || store.verify(initialToken) {
		t.Fatal("rotation did not replace the token")
	}

	reloaded, err := newAccessTokenStore(sha256.Sum256([]byte("ignored fallback")), path)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.verify(rotatedToken) || reloaded.verify(initialToken) {
		t.Fatal("persisted token was not reloaded")
	}
}
