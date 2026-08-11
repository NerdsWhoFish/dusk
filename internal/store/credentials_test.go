package store_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FetchHQ/dusk/internal/store"
	"github.com/FetchHQ/dusk/pkg/githubapp"
	"github.com/FetchHQ/dusk/pkg/secret"
	"github.com/FetchHQ/dusk/pkg/vault"
)

const (
	testPEM     = "BEGIN TEST KEY\nnot-a-real-key\nEND TEST KEY"
	testHook    = "hook-secret-value"
	testClient  = "client-secret-value"
	testHTMLURL = "https://github.com/apps/dusk-example"
)

func newStore(t *testing.T) (*store.Store, []byte, string) {
	t.Helper()
	dir := t.TempDir()

	encoded, err := vault.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	master, err := vault.ParseKey(encoded)
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}

	s, err := store.New(dir, master)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, master, dir
}

func sampleCredentials() *store.Credentials {
	return store.FromGitHub(&githubapp.Credentials{
		ID: 12345, Slug: "dusk-example", Name: "Dusk", HTMLURL: testHTMLURL,
		ClientID: "Iv1.abc", PEM: testPEM, WebhookSecret: testHook, ClientSecret: testClient,
	})
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s, _, _ := newStore(t)
	want := sampleCredentials()

	if err := s.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	tests := []struct {
		name      string
		got, want string
	}{
		{"private key", got.PrivateKey.Reveal(), testPEM},
		{"webhook secret", got.WebhookSecret.Reveal(), testHook},
		{"client secret", got.ClientSecret.Reveal(), testClient},
		{"slug", got.Slug, "dusk-example"},
		{"html url", got.HTMLURL, testHTMLURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("= %q, want %q", tt.got, tt.want)
			}
		})
	}
	if got.AppID != 12345 {
		t.Errorf("AppID = %d, want 12345", got.AppID)
	}
}

func TestLoadBeforeOnboarding(t *testing.T) {
	s, _, _ := newStore(t)

	if s.Configured() {
		t.Error("Configured() is true before anything was saved")
	}
	if _, err := s.Load(); !errors.Is(err, store.ErrNotConfigured) {
		t.Errorf("want ErrNotConfigured, got %v", err)
	}
}

// ADR-0022: the durable file must be unreadable without the key, and readable
// with it. This is the only threat encryption at rest actually addresses.
func TestADR0022_CredentialsAreUnreadableOnDisk(t *testing.T) {
	s, _, dir := newStore(t)
	if err := s.Save(sampleCredentials()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(dir, store.CredentialsFile)
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	for _, leak := range []string{testPEM, testHook, testClient} {
		if strings.Contains(string(onDisk), leak) {
			t.Errorf("secret is plainly visible on disk: %q", leak)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}

	wrongKey, err := store.New(dir, mustKey(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := wrongKey.Load(); err == nil {
		t.Fatal("a different key opened the credentials")
	}
}

func TestCredentialsNeverRender(t *testing.T) {
	c := sampleCredentials()

	renders := []struct {
		name   string
		render func() string
	}{
		{"fmt %v", func() string { return fmt.Sprintf("%v", *c) }},
		{"fmt %+v", func() string { return fmt.Sprintf("%+v", *c) }},
		{"json.Marshal", func() string {
			b, err := json.Marshal(c)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			return string(b)
		}},
	}

	for _, tt := range renders {
		t.Run(tt.name, func(t *testing.T) {
			out := tt.render()
			for _, leak := range []string{testPEM, testHook, testClient} {
				if strings.Contains(out, leak) {
					t.Errorf("leaked %q in %s", leak, out)
				}
			}
			if !strings.Contains(out, secret.Redacted) {
				t.Errorf("want %q in output, got %s", secret.Redacted, out)
			}
		})
	}
}

func TestSaveReplacesExisting(t *testing.T) {
	s, _, dir := newStore(t)

	if err := s.Save(sampleCredentials()); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	updated := sampleCredentials()
	updated.PrivateKey = secret.New("BEGIN ROTATED KEY")
	if err := s.Save(updated); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.PrivateKey.Reveal() != "BEGIN ROTATED KEY" {
		t.Errorf("key was not replaced, got %q", got.PrivateKey.Reveal())
	}

	// The atomic write uses a temp file; it must not survive.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("want only the credentials file, got %v", names)
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	tests := []struct {
		name string
		key  []byte
	}{
		{name: "a nil key is rejected"},
		{name: "a short key is rejected", key: make([]byte, 16)},
		{name: "an over-long key is rejected", key: make([]byte, 64)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := store.New(t.TempDir(), tt.key); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestInstallURL(t *testing.T) {
	c := sampleCredentials()
	want := testHTMLURL + "/installations/new"
	if got := c.InstallURL(); got != want {
		t.Errorf("InstallURL() = %q, want %q", got, want)
	}
}

func mustKey(t *testing.T) []byte {
	t.Helper()
	encoded, err := vault.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	key, err := vault.ParseKey(encoded)
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	return key
}
