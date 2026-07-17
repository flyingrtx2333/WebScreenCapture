package app

import (
	"crypto/hmac"
	"crypto/sha1" // TURN REST authentication requires HMAC-SHA1.
	"encoding/base64"
	"fmt"
	"time"
)

type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

func makeICEServers(cfg Config, role Role, now time.Time) []ICEServer {
	expires := now.Add(10 * time.Minute).Unix()
	username := fmt.Sprintf("%d:%s", expires, role)
	mac := hmac.New(sha1.New, []byte(cfg.TURNSharedSecret))
	_, _ = mac.Write([]byte(username))
	credential := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	servers := []ICEServer{
		{URLs: []string{fmt.Sprintf("stun:%s:%d", cfg.TURNHost, cfg.TURNPort)}},
		{
			URLs: []string{
				fmt.Sprintf("turn:%s:%d?transport=udp", cfg.TURNHost, cfg.TURNPort),
				fmt.Sprintf("turn:%s:%d?transport=tcp", cfg.TURNHost, cfg.TURNPort),
			},
			Username: username, Credential: credential,
		},
	}
	if cfg.TURNSTLSPort > 0 {
		servers = append(servers, ICEServer{
			URLs:       []string{fmt.Sprintf("turns:%s:%d?transport=tcp", cfg.TURNHost, cfg.TURNSTLSPort)},
			Username:   username,
			Credential: credential,
		})
	}
	return servers
}
