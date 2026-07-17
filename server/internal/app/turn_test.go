package app

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"testing"
	"time"
)

func TestTURNCredentials(t *testing.T) {
	cfg := Config{TURNHost: "screen.example.com", TURNPort: 3479, TURNSTLSPort: 5349, TURNSharedSecret: "01234567890123456789012345678901"}
	now := time.Unix(1_700_000_000, 0)
	servers := makeICEServers(cfg, RoleAgent, now)
	if len(servers) != 3 {
		t.Fatalf("expected 3 ICE server entries, got %d", len(servers))
	}
	username := servers[1].Username
	mac := hmac.New(sha1.New, []byte(cfg.TURNSharedSecret))
	_, _ = mac.Write([]byte(username))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if servers[1].Credential != expected || servers[2].Credential != expected {
		t.Fatal("TURN credential does not match coturn REST calculation")
	}
}
