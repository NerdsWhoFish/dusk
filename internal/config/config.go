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
	"path/filepath"
	"strings"

	"github.com/NerdsWhoFish/dusk/pkg/secret"
	"github.com/NerdsWhoFish/dusk/pkg/vault"
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

	// AllowedAccounts are the GitHub accounts whose installations may be
	// reconciled. Empty means the account the App belongs to, and nothing
	// else, because anyone able to see an App can install it.
	AllowedAccounts []string

	// MCPToken is the bearer token the agent surface requires.
	MCPToken secret.String

	// TrustedNetwork serves the agent surface with no authentication at all.
	// ADR-0012 allows this for LAN and single operator deployments and requires
	// it to be explicit, so there is no way to arrive here by default.
	TrustedNetwork bool

	// ConfigRepository is "owner/name" of the repository notes are written to.
	// Empty means notes cannot be written; everything else still works, because
	// most of the catalog lives beside the code it describes (ADR-0031).
	ConfigRepository string

	// OAuthClientID and OAuthClientSecret enable signing in with GitHub, which
	// derives what a viewer may see from what GitHub says they can read
	// (ADR-0012). Unset means the shared token is the only way in.
	OAuthClientID     string
	OAuthClientSecret secret.String

	// ShowObservedToEveryone lets signed-in viewers see entities no repository
	// backs. ADR-0012 requires this to be a decision: those entities have no
	// natural access control, and silent over-sharing is the worse failure.
	ShowObservedToEveryone bool

	// PluginOrgs are the GitHub organisations whose `dusk-plugin-*`
	// repositories Dusk will offer and run. Adding one is the security
	// decision, because a plugin runs with Dusk's permissions (ADR-0042).
	PluginOrgs []string
}

// ConfigRepositoryParts splits the config repository into owner and name.
func (c *Config) ConfigRepositoryParts() (owner, name string, ok bool) {
	owner, name, ok = strings.Cut(c.ConfigRepository, "/")
	return owner, name, ok && owner != "" && name != ""
}

// IndexPath is where the materialized graph lives. It sits beside the
// credentials because both are derived from the same deployment, and unlike
// the credentials it can be deleted and rebuilt.
func (c *Config) IndexPath() string { return filepath.Join(c.DataDir, "index.db") }

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

		AllowedAccounts:  splitAccounts(getenv("DUSK_ALLOWED_ACCOUNTS")),
		TrustedNetwork:   strings.EqualFold(strings.TrimSpace(getenv("DUSK_TRUSTED_NETWORK")), "true"),
		ConfigRepository: strings.Trim(strings.TrimSpace(getenv("DUSK_CONFIG_REPOSITORY")), "/"),
		PluginOrgs:       splitAccounts(getenv("DUSK_PLUGIN_ORGS")),

		OAuthClientID: strings.TrimSpace(getenv("DUSK_GITHUB_CLIENT_ID")),
		ShowObservedToEveryone: strings.EqualFold(
			strings.TrimSpace(getenv("DUSK_OBSERVED_VISIBLE_TO_ALL")), "true"),
	}
	if s := strings.TrimSpace(getenv("DUSK_GITHUB_CLIENT_SECRET")); s != "" {
		c.OAuthClientSecret = secret.New(s)
	}
	if token := strings.TrimSpace(getenv("DUSK_MCP_TOKEN")); token != "" {
		c.MCPToken = secret.New(token)
	}

	problems := c.readHosts()
	problems = append(problems, c.readEncryptionKey(getenv)...)
	problems = append(problems, c.readAgentAccess()...)
	problems = append(problems, c.readConfigRepository()...)
	problems = append(problems, c.readOAuth()...)

	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	return c, nil
}

// readHosts validates the two hostnames, defaulting the public one to the
// private one because most deployments are reached at a single address.
func (c *Config) readHosts() []error {
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
	return problems
}

func (c *Config) readEncryptionKey(getenv func(string) string) []error {
	rawKey := getenv("DUSK_ENCRYPTION_KEY")
	if rawKey == "" {
		return []error{errors.New("DUSK_ENCRYPTION_KEY is required: credentials are always encrypted at rest. Generate one with `dusk genkey`")}
	}
	if _, err := vault.ParseKey(rawKey); err != nil {
		return []error{fmt.Errorf("DUSK_ENCRYPTION_KEY is invalid: %w", err)}
	}
	c.EncryptionKey = secret.New(rawKey)
	return nil
}

// SignInURL is where GitHub returns the browser after an OAuth sign-in.
func (c *Config) SignInURL() string { return c.PrivateHost + "/auth/callback" }

// OAuthConfigured reports whether signing in with GitHub is available.
func (c *Config) OAuthConfigured() bool {
	return c.OAuthClientID != "" && !c.OAuthClientSecret.IsZero()
}

// readOAuth refuses half a configuration. One of the pair without the other is
// a sign-in that fails at the moment somebody tries it rather than at boot.
func (c *Config) readOAuth() []error {
	if c.OAuthClientID == "" && c.OAuthClientSecret.IsZero() {
		return nil
	}
	if c.OAuthClientID == "" {
		return []error{errors.New("DUSK_GITHUB_CLIENT_SECRET is set without DUSK_GITHUB_CLIENT_ID")}
	}
	if c.OAuthClientSecret.IsZero() {
		return []error{errors.New("DUSK_GITHUB_CLIENT_ID is set without DUSK_GITHUB_CLIENT_SECRET")}
	}
	return nil
}

// readConfigRepository checks the shape only. Whether the repository exists and
// whether Dusk may write to it are answered at write time, because a typo here
// must not stop a catalog whose reads never touch it from starting.
func (c *Config) readConfigRepository() []error {
	if c.ConfigRepository == "" {
		return nil
	}
	if _, _, ok := c.ConfigRepositoryParts(); !ok {
		return []error{fmt.Errorf("DUSK_CONFIG_REPOSITORY is invalid: want owner/name, got %q", c.ConfigRepository)}
	}
	return nil
}

// readAgentAccess checks the answer to "who may read the catalog". Two answers
// is not a stricter setting, it is an unanswered question.
func (c *Config) readAgentAccess() []error {
	if !c.MCPToken.IsZero() && c.TrustedNetwork {
		return []error{errors.New("DUSK_MCP_TOKEN and DUSK_TRUSTED_NETWORK are both set: pick one, since a token means authenticate and a trusted network means do not")}
	}
	return nil
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

// splitAccounts parses the comma separated allowlist, ignoring blanks so a
// trailing comma cannot silently allow an empty account name.
func splitAccounts(raw string) []string {
	var accounts []string
	for account := range strings.SplitSeq(raw, ",") {
		if trimmed := strings.TrimSpace(account); trimmed != "" {
			accounts = append(accounts, trimmed)
		}
	}
	return accounts
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
