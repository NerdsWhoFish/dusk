// Package store persists Dusk's GitHub App credentials, encrypted at rest.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FetchHQ/dusk/pkg/githubapp"
	"github.com/FetchHQ/dusk/pkg/secret"
	"github.com/FetchHQ/dusk/pkg/vault"
)

// CredentialsFile is the name under the data directory.
const CredentialsFile = "credentials.enc"

// ErrNotConfigured means onboarding has not run. It is an expected state on
// first boot, not a failure.
var ErrNotConfigured = errors.New("store: no credentials, Dusk is not onboarded")

// AccessMode is how much Dusk may do to the repositories it watches.
type AccessMode string

// The three modes from ADR-0005. The App is registered with only the
// permissions its mode needs, so read-only genuinely means read-only.
const (
	ModeRead     AccessMode = "read"
	ModeProposal AccessMode = "proposal"
	ModeWrite    AccessMode = "write"
)

// Valid reports whether m is a known mode.
func (m AccessMode) Valid() bool {
	return m == ModeRead || m == ModeProposal || m == ModeWrite
}

// Credentials are the App's durable secrets. The sensitive fields are
// secret.String so they cannot reach a log line or an API response.
type Credentials struct {
	AppID    int64      `json:"app_id"`
	Slug     string     `json:"slug"`
	Name     string     `json:"name"`
	HTMLURL  string     `json:"html_url"`
	ClientID string     `json:"client_id"`
	Mode     AccessMode `json:"mode"`

	// Owner is the account the App belongs to. It is the default allowlist:
	// an installation on any other account is not reconciled.
	Owner string `json:"owner"`

	PrivateKey    secret.String `json:"private_key"`
	WebhookSecret secret.String `json:"webhook_secret"`
	ClientSecret  secret.String `json:"client_secret"`
}

// FromGitHub converts the manifest exchange result into stored form.
func FromGitHub(c *githubapp.Credentials, mode AccessMode) *Credentials {
	return &Credentials{
		Mode:          mode,
		AppID:         c.ID,
		Slug:          c.Slug,
		Name:          c.Name,
		HTMLURL:       c.HTMLURL,
		ClientID:      c.ClientID,
		Owner:         c.Owner.Login,
		PrivateKey:    secret.New(c.PEM),
		WebhookSecret: secret.New(c.WebhookSecret),
		ClientSecret:  secret.New(c.ClientSecret),
	}
}

// InstallURL is where the user installs the App onto repositories.
func (c *Credentials) InstallURL() string {
	return githubapp.Credentials{HTMLURL: c.HTMLURL}.InstallURL()
}

// Store reads and writes credentials sealed under a master key.
type Store struct {
	dir    string
	master []byte
}

// New returns a Store rooted at dir, sealing with master.
func New(dir string, master []byte) (*Store, error) {
	if len(master) != vault.KeySize {
		return nil, fmt.Errorf("store: master key must be %d bytes", vault.KeySize)
	}
	return &Store{dir: dir, master: master}, nil
}

func (s *Store) path() string { return filepath.Join(s.dir, CredentialsFile) }

// Save seals credentials to disk atomically. A torn write would read as
// corruption, and re-onboarding is the only recovery from that.
func (s *Store) Save(c *Credentials) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("store: create data dir: %w", err)
	}

	plaintext, err := json.Marshal(revealed(c))
	if err != nil {
		return fmt.Errorf("store: encode credentials: %w", err)
	}
	sealed, err := vault.Seal(s.master, plaintext)
	if err != nil {
		return fmt.Errorf("store: seal credentials: %w", err)
	}

	tmp, err := os.CreateTemp(s.dir, CredentialsFile+".*")
	if err != nil {
		return fmt.Errorf("store: create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("store: chmod: %w", err)
	}
	if _, err := tmp.Write(sealed); err != nil {
		return fmt.Errorf("store: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("store: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path()); err != nil {
		return fmt.Errorf("store: rename into place: %w", err)
	}
	return nil
}

// Load opens the stored credentials, returning ErrNotConfigured when absent.
func (s *Store) Load() (*Credentials, error) {
	sealed, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("store: read credentials: %w", err)
	}

	plaintext, err := vault.Open(s.master, sealed)
	if err != nil {
		return nil, fmt.Errorf("store: open credentials: %w", err)
	}

	var raw plainCredentials
	if err := json.Unmarshal(plaintext, &raw); err != nil {
		return nil, fmt.Errorf("store: decode credentials: %w", err)
	}
	return raw.sealedForm(), nil
}

// Configured reports whether onboarding has completed.
func (s *Store) Configured() bool {
	_, err := os.Stat(s.path())
	return err == nil
}

// plainCredentials is the on-disk shape, inside the sealed envelope. Secrets
// are plain here because the whole blob is encrypted; secret.String would
// serialise them as [REDACTED] and silently destroy them.
type plainCredentials struct {
	AppID         int64  `json:"app_id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	HTMLURL       string `json:"html_url"`
	ClientID      string `json:"client_id"`
	Mode          string `json:"mode"`
	Owner         string `json:"owner"`
	PrivateKey    string `json:"private_key"`
	WebhookSecret string `json:"webhook_secret"`
	ClientSecret  string `json:"client_secret"`
}

func revealed(c *Credentials) plainCredentials {
	return plainCredentials{
		AppID:         c.AppID,
		Slug:          c.Slug,
		Name:          c.Name,
		HTMLURL:       c.HTMLURL,
		ClientID:      c.ClientID,
		Mode:          string(c.Mode),
		Owner:         c.Owner,
		PrivateKey:    c.PrivateKey.Reveal(),
		WebhookSecret: c.WebhookSecret.Reveal(),
		ClientSecret:  c.ClientSecret.Reveal(),
	}
}

func (p plainCredentials) sealedForm() *Credentials {
	return &Credentials{
		AppID:         p.AppID,
		Slug:          p.Slug,
		Name:          p.Name,
		HTMLURL:       p.HTMLURL,
		Owner:         p.Owner,
		ClientID:      p.ClientID,
		Mode:          AccessMode(p.Mode),
		PrivateKey:    secret.New(p.PrivateKey),
		WebhookSecret: secret.New(p.WebhookSecret),
		ClientSecret:  secret.New(p.ClientSecret),
	}
}
