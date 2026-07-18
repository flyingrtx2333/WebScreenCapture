package app

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Address          string
	PublicURL        string
	PublicHost       string
	FlyingRTXAuthURL string
	SessionSecret    []byte
	TURNSharedSecret string
	TURNHost         string
	TURNPort         int
	TURNSTLSPort     int
	SecureCookies    bool
}

func LoadConfigFromEnv() (Config, error) {
	publicURL := envOr("PUBLIC_URL", "http://localhost:8080")
	parsedURL, err := url.Parse(publicURL)
	if err != nil || parsedURL.Hostname() == "" {
		return Config{}, fmt.Errorf("PUBLIC_URL must be an absolute URL")
	}

	sessionSecret, err := decodeSecret(os.Getenv("SESSION_SECRET"))
	if err != nil || len(sessionSecret) < 32 {
		return Config{}, errors.New("SESSION_SECRET must be base64/hex and decode to at least 32 bytes")
	}

	turnSecret := strings.TrimSpace(os.Getenv("TURN_SHARED_SECRET"))
	if len(turnSecret) < 32 {
		return Config{}, errors.New("TURN_SHARED_SECRET must be at least 32 characters")
	}

	turnHost := envOr("TURN_HOST", parsedURL.Hostname())
	turnPort, err := envInt("TURN_PORT", 3479)
	if err != nil {
		return Config{}, err
	}
	turnTLSPort, err := envInt("TURNS_PORT", 5349)
	if err != nil {
		return Config{}, err
	}
	secureCookies, err := strconv.ParseBool(envOr("SECURE_COOKIES", "true"))
	if err != nil {
		return Config{}, fmt.Errorf("SECURE_COOKIES: %w", err)
	}
	authURL := envOr("FLYINGRTX_AUTH_URL", "http://127.0.0.1:59888/api/v1/auth/login")
	parsedAuthURL, err := url.Parse(authURL)
	if err != nil || parsedAuthURL.Host == "" || (parsedAuthURL.Scheme != "http" && parsedAuthURL.Scheme != "https") {
		return Config{}, errors.New("FLYINGRTX_AUTH_URL must be an absolute HTTP(S) URL")
	}

	return Config{
		Address:          envOr("APP_ADDR", ":8080"),
		PublicURL:        strings.TrimRight(publicURL, "/"),
		PublicHost:       parsedURL.Host,
		FlyingRTXAuthURL: authURL,
		SessionSecret:    sessionSecret,
		TURNSharedSecret: turnSecret,
		TURNHost:         turnHost,
		TURNPort:         turnPort,
		TURNSTLSPort:     turnTLSPort,
		SecureCookies:    secureCookies,
	}, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) (int, error) {
	value := envOr(name, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > 65535 {
		return 0, fmt.Errorf("%s must be a valid TCP/UDP port", name)
	}
	return parsed, nil
}

func decodeSecret(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return hex.DecodeString(value)
}
