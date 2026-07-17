package app

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoadConfigWithSharedAccessToken(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://screen.example.com")
	t.Setenv("ACCESS_TOKEN_SHA256", strings.Repeat("0", 64))
	t.Setenv("ACCESS_TOKEN_FILE", "/data/access-token.sha256")
	t.Setenv("SESSION_SECRET", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("TURN_SHARED_SECRET", strings.Repeat("a", 32))

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccessTokenFile != "/data/access-token.sha256" || cfg.PublicHost != "screen.example.com" {
		t.Fatalf("unexpected decoded configuration: %#v", cfg)
	}
}
