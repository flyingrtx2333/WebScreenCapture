package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const accessTokenBytes = 32

type accessTokenStore struct {
	mu     sync.RWMutex
	digest [sha256.Size]byte
	path   string
}

func newAccessTokenStore(initial [sha256.Size]byte, path string) (*accessTokenStore, error) {
	store := &accessTokenStore{digest: initial, path: strings.TrimSpace(path)}
	if store.path == "" {
		return store, nil
	}

	data, err := os.ReadFile(store.path)
	if err == nil {
		digest, parseErr := decodeAccessTokenHash(string(data))
		if parseErr != nil {
			return nil, parseErr
		}
		store.digest = digest
		return store, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := store.persist(initial); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *accessTokenStore) verify(token string) bool {
	if len(token) < 32 {
		return false
	}
	digest := sha256.Sum256([]byte(token))
	s.mu.RLock()
	defer s.mu.RUnlock()
	return subtle.ConstantTimeCompare(digest[:], s.digest[:]) == 1
}

func (s *accessTokenStore) rotate() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw := make([]byte, accessTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	if err := s.persist(digest); err != nil {
		return "", err
	}

	s.digest = digest
	return token, nil
}

func (s *accessTokenStore) persist(digest [sha256.Size]byte) error {
	if s.path == "" {
		return nil
	}
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".access-token-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(hex.EncodeToString(digest[:]) + "\n"); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		if removeErr := os.Remove(s.path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		if retryErr := os.Rename(temporaryPath, s.path); retryErr != nil {
			return retryErr
		}
	}
	return nil
}

func decodeAccessTokenHash(value string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != sha256.Size {
		return digest, errors.New("access token hash must be a 64-character SHA-256 hex digest")
	}
	copy(digest[:], decoded)
	return digest, nil
}
