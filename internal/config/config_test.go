package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/config"
	"github.com/NerdsWhoFish/dusk/pkg/secret"
	"github.com/NerdsWhoFish/dusk/pkg/vault"
)

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func validKey(t *testing.T) string {
	t.Helper()
	k, err := vault.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return k
}

func TestLoad(t *testing.T) {
	key := validKey(t)

	tests := []struct {
		name        string
		env         map[string]string
		wantErrs    []string
		wantAddr    string
		wantDataDir string
		wantURL     string
	}{
		{
			name:        "a complete environment loads",
			env:         map[string]string{"DUSK_PRIVATE_HOST": "https://dusk.example.com", "DUSK_ENCRYPTION_KEY": key},
			wantAddr:    config.DefaultAddr,
			wantDataDir: config.DefaultDataDir,
			wantURL:     "https://dusk.example.com",
		},
		{
			name: "addr and data dir override their defaults",
			env: map[string]string{
				"DUSK_PRIVATE_HOST": "https://dusk.example.com", "DUSK_ENCRYPTION_KEY": key,
				"DUSK_ADDR": "127.0.0.1:9000", "DUSK_DATA_DIR": "/data",
			},
			wantAddr: "127.0.0.1:9000", wantDataDir: "/data", wantURL: "https://dusk.example.com",
		},
		{
			name:     "a trailing slash is trimmed so callback URLs never double up",
			env:      map[string]string{"DUSK_PRIVATE_HOST": "https://dusk.example.com/", "DUSK_ENCRYPTION_KEY": key},
			wantAddr: config.DefaultAddr, wantDataDir: config.DefaultDataDir,
			wantURL: "https://dusk.example.com",
		},
		{
			name:     "an empty environment reports both required variables at once",
			env:      map[string]string{},
			wantErrs: []string{"DUSK_PRIVATE_HOST", "DUSK_ENCRYPTION_KEY"},
		},
		{
			name:     "a non-http external URL is rejected",
			env:      map[string]string{"DUSK_PRIVATE_HOST": "ftp://dusk.example.com", "DUSK_ENCRYPTION_KEY": key},
			wantErrs: []string{"must be http or https"},
		},
		{
			name:     "an external URL with no host is rejected",
			env:      map[string]string{"DUSK_PRIVATE_HOST": "https://", "DUSK_ENCRYPTION_KEY": key},
			wantErrs: []string{"no host"},
		},
		{
			name:     "a short encryption key is rejected",
			env:      map[string]string{"DUSK_PRIVATE_HOST": "https://dusk.example.com", "DUSK_ENCRYPTION_KEY": "c2hvcnQ="},
			wantErrs: []string{"DUSK_ENCRYPTION_KEY is invalid"},
		},
		{
			name:     "a non-base64 encryption key is rejected",
			env:      map[string]string{"DUSK_PRIVATE_HOST": "https://dusk.example.com", "DUSK_ENCRYPTION_KEY": "not base64!!"},
			wantErrs: []string{"DUSK_ENCRYPTION_KEY is invalid"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := config.Load(env(tt.env))

			if len(tt.wantErrs) > 0 {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				for _, want := range tt.wantErrs {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("want error mentioning %q, got:\n%v", want, err)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Addr != tt.wantAddr {
				t.Errorf("Addr = %q, want %q", got.Addr, tt.wantAddr)
			}
			if got.DataDir != tt.wantDataDir {
				t.Errorf("DataDir = %q, want %q", got.DataDir, tt.wantDataDir)
			}
			if got.PrivateHost != tt.wantURL {
				t.Errorf("PrivateHost = %q, want %q", got.PrivateHost, tt.wantURL)
			}
		})
	}
}

// ADR-0022 has no unencrypted mode, so a missing key must stop the process
// rather than degrade to plaintext with a warning nobody reads.
func TestADR0022_MissingEncryptionKeyIsFatal(t *testing.T) {
	_, err := config.Load(env(map[string]string{"DUSK_PRIVATE_HOST": "https://dusk.example.com"}))
	if err == nil {
		t.Fatal("Dusk started without an encryption key")
	}
	if !strings.Contains(err.Error(), "DUSK_ENCRYPTION_KEY is required") {
		t.Errorf("error should name the variable, got: %v", err)
	}
}

func TestConfigNeverRendersTheKey(t *testing.T) {
	key := validKey(t)
	c, err := config.Load(env(map[string]string{
		"DUSK_PRIVATE_HOST": "https://dusk.example.com", "DUSK_ENCRYPTION_KEY": key,
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, format := range []string{"%v", "%+v", "%#v"} {
		rendered := fmt.Sprintf(format, *c)
		if strings.Contains(rendered, key) {
			t.Errorf("%s leaked the encryption key: %s", format, rendered)
		}
		if !strings.Contains(rendered, secret.Redacted) {
			t.Errorf("%s should show %q, got %s", format, secret.Redacted, rendered)
		}
	}
}

// ADR-0012 allows an unauthenticated agent surface and requires it to be
// explicit. Two answers to "who may read the catalog" is an unanswered
// question, not a stricter setting.
func TestMCPAuthConfiguration(t *testing.T) {
	base := map[string]string{
		"DUSK_PRIVATE_HOST":   "https://dusk.example.com",
		"DUSK_ENCRYPTION_KEY": validKey(t),
	}

	with := func(extra map[string]string) map[string]string {
		merged := map[string]string{}
		for k, v := range base {
			merged[k] = v
		}
		for k, v := range extra {
			merged[k] = v
		}
		return merged
	}

	t.Run("a token is read", func(t *testing.T) {
		cfg, err := config.Load(env(with(map[string]string{"DUSK_MCP_TOKEN": "s3cret"})))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.MCPToken.Reveal() != "s3cret" {
			t.Errorf("MCPToken = %q, want it read", cfg.MCPToken.Reveal())
		}
		if cfg.TrustedNetwork {
			t.Error("TrustedNetwork = true, want false")
		}
	})

	t.Run("a trusted network is opt in", func(t *testing.T) {
		cfg, err := config.Load(env(with(map[string]string{"DUSK_TRUSTED_NETWORK": "true"})))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.TrustedNetwork {
			t.Error("TrustedNetwork = false, want true")
		}
	})

	t.Run("neither is the default, and neither is an error", func(t *testing.T) {
		cfg, err := config.Load(env(base))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.MCPToken.IsZero() || cfg.TrustedNetwork {
			t.Error("an unconfigured deployment should choose neither")
		}
	})

	t.Run("both together is rejected", func(t *testing.T) {
		_, err := config.Load(env(with(map[string]string{
			"DUSK_MCP_TOKEN": "s3cret", "DUSK_TRUSTED_NETWORK": "true",
		})))
		if err == nil {
			t.Fatal("Load accepted both, want an error naming the conflict")
		}
		if !strings.Contains(err.Error(), "pick one") {
			t.Errorf("error = %q, want it to say which to pick", err)
		}
	})
}
