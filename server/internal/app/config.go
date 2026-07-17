package app

import (
	"crypto/sha256"
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
	Address            string
	PublicURL          string
	PublicHost         string
	DeviceTokenHash    [sha256.Size]byte
	ViewerPasswordHash string
	SessionSecret      []byte
	TURNSharedSecret   string
	TURNHost           string
	TURNPort           int
	TURNSTLSPort       int
	SecureCookies      bool
}

func LoadConfigFromEnv() (Config, error) {
	publicURL := envOr("PUBLIC_URL", "http://localhost:8080")
	parsedURL, err := url.Parse(publicURL)
	if err != nil || parsedURL.Hostname() == "" {
		return Config{}, fmt.Errorf("PUBLIC_URL must be an absolute URL")
	}

	deviceHashHex := strings.TrimSpace(os.Getenv("DEVICE_TOKEN_SHA256"))
	decodedHash, err := hex.DecodeString(deviceHashHex)
	if err != nil || len(decodedHash) != sha256.Size {
		return Config{}, errors.New("DEVICE_TOKEN_SHA256 must be a 64-character SHA-256 hex digest")
	}
	var deviceHash [sha256.Size]byte
	copy(deviceHash[:], decodedHash)

	passwordHash := strings.TrimSpace(os.Getenv("VIEWER_PASSWORD_HASH"))
	if encoded := strings.TrimSpace(os.Getenv("VIEWER_PASSWORD_HASH_B64")); encoded != "" {
		decoded, decodeErr := base64.StdEncoding.DecodeString(encoded)
		if decodeErr != nil {
			return Config{}, errors.New("VIEWER_PASSWORD_HASH_B64 must be valid standard Base64")
		}
		passwordHash = string(decoded)
	}
	if !strings.HasPrefix(passwordHash, "$argon2id$") {
		return Config{}, errors.New("VIEWER_PASSWORD_HASH_B64 must decode to an Argon2id PHC string")
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

	return Config{
		Address:            envOr("APP_ADDR", ":8080"),
		PublicURL:          strings.TrimRight(publicURL, "/"),
		PublicHost:         parsedURL.Host,
		DeviceTokenHash:    deviceHash,
		ViewerPasswordHash: passwordHash,
		SessionSecret:      sessionSecret,
		TURNSharedSecret:   turnSecret,
		TURNHost:           turnHost,
		TURNPort:           turnPort,
		TURNSTLSPort:       turnTLSPort,
		SecureCookies:      secureCookies,
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
