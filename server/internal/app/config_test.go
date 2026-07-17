package app

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoadConfigWithBase64PasswordHash(t *testing.T) {
	passwordHash := "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA"
	t.Setenv("PUBLIC_URL", "https://screen.example.com")
	t.Setenv("DEVICE_TOKEN_SHA256", strings.Repeat("0", 64))
	t.Setenv("VIEWER_PASSWORD_HASH", "")
	t.Setenv("VIEWER_PASSWORD_HASH_B64", base64.StdEncoding.EncodeToString([]byte(passwordHash)))
	t.Setenv("SESSION_SECRET", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("TURN_SHARED_SECRET", strings.Repeat("a", 32))

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ViewerPasswordHash != passwordHash || cfg.PublicHost != "screen.example.com" {
		t.Fatalf("unexpected decoded configuration: %#v", cfg)
	}
}
