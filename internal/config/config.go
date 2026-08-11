// Package config loads and validates Dusk's boot configuration.
//
// Boot configuration is not catalog content: it is how the process starts, so
// it comes from the environment rather than the config repository.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/FetchHQ/dusk/pkg/secret"
	"github.com/FetchHQ/dusk/pkg/vault"
)

// Defaults applied when the corresponding variable is unset.
const (
	DefaultAddr    = ":8080"
	DefaultDataDir = "/var/lib/dusk"
)

// Config is the validated boot configuration.
type Config struct {
	Addr    string
	DataDir string

	// PrivateHost is where people reach Dusk: the UI, the API, and the setup
	// callback. It does not need to be reachable from the internet.
	PrivateHost string

	// PublicHost is where GitHub reaches Dusk to deliver webhooks. It defaults
	// to PrivateHost, and differs when a forwarder exposes only the webhook
	// path while everything else stays on a private hostname.
	PublicHost string

	// EncryptionKey is the decoded master key. Required: ADR-0022 has no
	// unencrypted mode, so an insecure deployment cannot exist.
	EncryptionKey secret.String
}

// WebhookURL is the delivery URL baked into the GitHub App registration.
func (c *Config) WebhookURL() string { return c.PublicHost + "/webhooks" }

// SetupCallbackURL is where GitHub returns the browser during onboarding.
func (c *Config) SetupCallbackURL() string { return c.PrivateHost + "/setup/callback" }

// AuthCallbackURL is where GitHub returns the browser after sign-in.
func (c *Config) AuthCallbackURL() string { return c.PrivateHost + "/auth/callback" }

// SplitHosts reports whether public and private differ, which is worth saying
// out loud at boot because it is the setup most likely to be misconfigured.
func (c *Config) SplitHosts() bool { return c.PublicHost != c.PrivateHost }

// Load reads and validates configuration, reporting every problem at once
// rather than one variable per attempt.
func Load(getenv func(string) string) (*Config, error) {
	c := &Config{
		Addr:        orDefault(getenv("DUSK_ADDR"), DefaultAddr),
		DataDir:     orDefault(getenv("DUSK_DATA_DIR"), DefaultDataDir),
		PrivateHost: normalizeHost(getenv("DUSK_PRIVATE_HOST")),
		PublicHost:  normalizeHost(getenv("DUSK_PUBLIC_HOST")),
	}

	var problems []error

	if c.PrivateHost == "" {
		problems = append(problems, errors.New("DUSK_PRIVATE_HOST is required: it is where you reach the UI and where GitHub returns your browser during setup, for example https://dusk.example.com"))
	} else if err := validateHost("DUSK_PRIVATE_HOST", c.PrivateHost); err != nil {
		problems = append(problems, err)
	}

	if c.PublicHost == "" {
		c.PublicHost = c.PrivateHost
	} else if err := validateHost("DUSK_PUBLIC_HOST", c.PublicHost); err != nil {
		problems = append(problems, err)
	}

	switch rawKey := getenv("DUSK_ENCRYPTION_KEY"); rawKey {
	case "":
		problems = append(problems, errors.New("DUSK_ENCRYPTION_KEY is required: credentials are always encrypted at rest. Generate one with `dusk genkey`"))
	default:
		if _, err := vault.ParseKey(rawKey); err != nil {
			problems = append(problems, fmt.Errorf("DUSK_ENCRYPTION_KEY is invalid: %w", err))
		} else {
			c.EncryptionKey = secret.New(rawKey)
		}
	}

	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	return c, nil
}

// LoadFromEnv is Load against the process environment.
func LoadFromEnv() (*Config, error) { return Load(os.Getenv) }

// MasterKey decodes the master key for use with the vault package.
func (c *Config) MasterKey() ([]byte, error) {
	return vault.ParseKey(c.EncryptionKey.Reveal())
}

// normalizeHost accepts a bare hostname as well as a full URL, since the
// variable is named for a host and people will supply one.
func normalizeHost(raw string) string {
	h := strings.TrimSpace(raw)
	if h == "" {
		return ""
	}
	if !strings.Contains(h, "://") {
		h = "https://" + h
	}
	return strings.TrimSuffix(h, "/")
}

func validateHost(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL: %w", name, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s must be http or https, got %q", name, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("%s has no host", name)
	}
	if u.Path != "" {
		return fmt.Errorf("%s must be a host, not a path: drop %q", name, u.Path)
	}
	return nil
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
