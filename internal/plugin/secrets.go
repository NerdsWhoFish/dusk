package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/pkg/secret"
	"github.com/NerdsWhoFish/dusk/pkg/vault"
)

// SecretsFile is the sealed half of a plugin's configuration, written beside
// its record so uninstalling takes the credential with it.
const SecretsFile = "secrets.enc"

// Secrets is the sensitive half of one plugin's configuration, mirroring the
// record's own Config and Instances. A value here is set and replaced, never
// read back (ADR-0023); secret.String is what keeps it out of a log line.
type Secrets struct {
	Config    map[string]secret.String
	Instances map[string]map[string]secret.String
}

// For returns the sensitive fields of one configuration, empty naming the
// plugin's own rather than a named instance.
func (s *Secrets) For(instance string) map[string]secret.String {
	if s == nil {
		return nil
	}
	if instance == "" {
		return s.Config
	}
	return s.Instances[instance]
}

// Names lists which fields of a configuration are set, so the UI can say a
// credential is present without being told what it is.
func (s *Secrets) Names(instance string) []string {
	return slices.Sorted(maps.Keys(s.For(instance)))
}

func (s *Store) secrets(id string) string { return filepath.Join(s.dir(id), SecretsFile) }

// ReadSecrets opens a plugin's sealed configuration. A plugin with none is not
// an error: it is one that has never been given a credential.
func (s *Store) ReadSecrets(id string) (*Secrets, error) {
	if len(s.Master) != vault.KeySize {
		return nil, fmt.Errorf("plugin: reading %s needs the master key, which is %d bytes", id, vault.KeySize)
	}

	sealed, err := os.ReadFile(s.secrets(id))
	if errors.Is(err, os.ErrNotExist) {
		return &Secrets{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("plugin: read the secrets for %s: %w", id, err)
	}

	plaintext, err := vault.Open(s.Master, sealed)
	if err != nil {
		return nil, fmt.Errorf("plugin: open the secrets for %s: %w", id, err)
	}

	var raw plainSecrets
	if err := json.Unmarshal(plaintext, &raw); err != nil {
		return nil, fmt.Errorf("plugin: decode the secrets for %s: %w", id, err)
	}
	return raw.sealedForm(), nil
}

// WriteSecrets seals a plugin's sensitive configuration to disk atomically. A
// torn write reads as corruption, and retyping every credential is the only
// recovery from that.
func (s *Store) WriteSecrets(id string, secrets *Secrets) error {
	if len(s.Master) != vault.KeySize {
		return fmt.Errorf("plugin: sealing %s needs the master key, which is %d bytes", id, vault.KeySize)
	}
	if err := os.MkdirAll(s.dir(id), 0o700); err != nil {
		return fmt.Errorf("plugin: make the directory for %s: %w", id, err)
	}

	plaintext, err := json.Marshal(revealed(secrets))
	if err != nil {
		return fmt.Errorf("plugin: encode the secrets for %s: %w", id, err)
	}
	sealed, err := vault.Seal(s.Master, plaintext)
	if err != nil {
		return fmt.Errorf("plugin: seal the secrets for %s: %w", id, err)
	}

	return atomicWrite(s.dir(id), s.secrets(id), sealed)
}

func atomicWrite(dir, path string, body []byte) error {
	return atomicWriteMode(dir, path, body, 0o600)
}

func atomicWriteMode(dir, path string, body []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("plugin: create a temporary file: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("plugin: chmod: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		return fmt.Errorf("plugin: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("plugin: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("plugin: rename into place: %w", err)
	}
	return nil
}

// plainSecrets is the on-disk shape inside the sealed envelope. Values are
// plain here because the whole blob is encrypted; secret.String would marshal
// them as [REDACTED] and silently destroy what it was asked to keep.
type plainSecrets struct {
	Config    map[string]string            `json:"config,omitempty"`
	Instances map[string]map[string]string `json:"instances,omitempty"`
}

func revealed(s *Secrets) plainSecrets {
	plain := plainSecrets{Config: reveal(s.Config)}
	if len(s.Instances) > 0 {
		plain.Instances = make(map[string]map[string]string, len(s.Instances))
		for instance, fields := range s.Instances {
			plain.Instances[instance] = reveal(fields)
		}
	}
	return plain
}

func reveal(fields map[string]secret.String) map[string]string {
	if len(fields) == 0 {
		return nil
	}

	plain := make(map[string]string, len(fields))
	for name, value := range fields {
		plain[name] = value.Reveal()
	}
	return plain
}

func (p plainSecrets) sealedForm() *Secrets {
	secrets := &Secrets{Config: conceal(p.Config)}
	if len(p.Instances) > 0 {
		secrets.Instances = make(map[string]map[string]secret.String, len(p.Instances))
		for instance, fields := range p.Instances {
			secrets.Instances[instance] = conceal(fields)
		}
	}
	return secrets
}

func conceal(fields map[string]string) map[string]secret.String {
	if len(fields) == 0 {
		return nil
	}

	hidden := make(map[string]secret.String, len(fields))
	for name, value := range fields {
		hidden[name] = secret.New(value)
	}
	return hidden
}

// sensitiveOf is which of a plugin's fields must never be written to the
// record, returned by any read, or accepted from an agent.
func sensitiveOf(described *duskv1alpha1.DescribeResponse) map[string]bool {
	sensitive := map[string]bool{}
	for _, field := range described.GetConfigFields() {
		if field.GetSensitive() {
			sensitive[field.GetName()] = true
		}
	}
	return sensitive
}

// split separates a submitted configuration by sensitivity.
//
// An absent or empty sensitive value keeps what is stored, because a write-only
// field submits empty when it was not retyped and treating that as "clear it"
// would erase a credential on every unrelated edit. An explicit JSON null is
// how one is deliberately forgotten.
func split(submitted map[string]any, sensitive map[string]bool, stored map[string]secret.String) (map[string]any, map[string]secret.String) {
	plain := map[string]any{}
	secrets := map[string]secret.String{}
	maps.Copy(secrets, stored)

	for name, value := range submitted {
		if !sensitive[name] {
			plain[name] = value
			continue
		}

		text, _ := value.(string)
		switch {
		case value == nil:
			delete(secrets, name)
		case text != "":
			secrets[name] = secret.New(text)
		}
	}

	for name := range secrets {
		if !sensitive[name] {
			delete(secrets, name)
		}
	}
	return plain, secrets
}
