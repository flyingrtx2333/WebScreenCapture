package app

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoadConfigWithoutPreRegisteredToken(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://screen.example.com")
	t.Setenv("SESSION_SECRET", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("TURN_SHARED_SECRET", strings.Repeat("a", 32))

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicHost != "screen.example.com" {
		t.Fatalf("unexpected decoded configuration: %#v", cfg)
	}
}
