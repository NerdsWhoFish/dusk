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
	Addr        string
	DataDir     string
	ExternalURL string

	// EncryptionKey is the decoded master key. Required: ADR-0022 has no
	// unencrypted mode, so an insecure deployment cannot exist.
	EncryptionKey secret.String
}

// Load reads and validates configuration, reporting every problem at once
// rather than one variable per attempt.
func Load(getenv func(string) string) (*Config, error) {
	c := &Config{
		Addr:        orDefault(getenv("DUSK_ADDR"), DefaultAddr),
		DataDir:     orDefault(getenv("DUSK_DATA_DIR"), DefaultDataDir),
		ExternalURL: strings.TrimSuffix(strings.TrimSpace(getenv("DUSK_EXTERNAL_URL")), "/"),
	}

	var problems []error

	if c.ExternalURL == "" {
		problems = append(problems, errors.New("DUSK_EXTERNAL_URL is required: it is the base URL browsers and GitHub webhooks reach Dusk at, and it is baked into the GitHub App registration"))
	} else if err := validateExternalURL(c.ExternalURL); err != nil {
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

func validateExternalURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("DUSK_EXTERNAL_URL is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("DUSK_EXTERNAL_URL must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("DUSK_EXTERNAL_URL has no host")
	}
	return nil
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
