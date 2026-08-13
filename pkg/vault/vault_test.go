package vault_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/NerdsWhoFish/dusk/pkg/vault"
)

func newKey(t *testing.T) []byte {
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

func TestSealOpenRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		plaintext string
	}{
		{name: "key-shaped material", plaintext: "BEGIN TEST KEY\nnot-a-real-key\nEND TEST KEY"},
		{name: "an empty payload", plaintext: ""},
		{name: "unicode survives", plaintext: "sécret 🦉"},
		{name: "a large payload", plaintext: strings.Repeat("x", 100_000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := newKey(t)

			blob, err := vault.Seal(key, []byte(tt.plaintext))
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			if bytes.Contains(blob, []byte(tt.plaintext)) && tt.plaintext != "" {
				t.Fatal("plaintext is visible in the sealed blob")
			}

			got, err := vault.Open(key, blob)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if string(got) != tt.plaintext {
				t.Errorf("round trip = %q, want %q", got, tt.plaintext)
			}
		})
	}
}

func TestSealIsNonDeterministic(t *testing.T) {
	key := newKey(t)

	first, err := vault.Seal(key, []byte("same input"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := vault.Seal(key, []byte("same input"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Error("sealing the same plaintext twice produced identical blobs")
	}
}

func TestOpenRejectsBadInput(t *testing.T) {
	key := newKey(t)
	sealed, err := vault.Seal(key, []byte("secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	tests := []struct {
		name         string
		key          []byte
		blob         []byte
		wantWrongKey bool
	}{
		{name: "a different key cannot open it", key: newKey(t), blob: sealed, wantWrongKey: true},
		{name: "a truncated blob is rejected", key: key, blob: sealed[:len(sealed)/2]},
		{name: "garbage is rejected", key: key, blob: []byte("not an envelope")},
		{name: "an empty blob is rejected", key: key, blob: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := vault.Open(tt.key, tt.blob); err == nil {
				t.Fatal("want error, got nil")
			} else if tt.wantWrongKey && !errors.Is(err, vault.ErrWrongKey) {
				t.Errorf("want ErrWrongKey, got %v", err)
			}
		})
	}
}

// ADR-0022 requires the master key to be rotatable. Rewrap re-encrypts only
// the data key, so rotation must not disturb the payload.
func TestADR0022_MasterKeyCanRotateWithoutRewritingPayload(t *testing.T) {
	oldKey, newKey := newKey(t), newKey(t)
	const plaintext = "BEGIN TEST KEY"

	sealed, err := vault.Seal(oldKey, []byte(plaintext))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	rotated, err := vault.Rewrap(oldKey, newKey, sealed)
	if err != nil {
		t.Fatalf("Rewrap: %v", err)
	}

	got, err := vault.Open(newKey, rotated)
	if err != nil {
		t.Fatalf("Open with new key: %v", err)
	}
	if string(got) != plaintext {
		t.Errorf("after rotation = %q, want %q", got, plaintext)
	}

	if _, err := vault.Open(oldKey, rotated); !errors.Is(err, vault.ErrWrongKey) {
		t.Errorf("old key still opens the rotated blob, got %v", err)
	}
}

func TestParseKey(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString(make([]byte, vault.KeySize))

	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{name: "a correctly sized base64 key is accepted", in: valid},
		{name: "an empty key is rejected", wantErr: true},
		{name: "a non-base64 key is rejected", in: "not base64!!", wantErr: true},
		{name: "a short key is rejected", in: base64.StdEncoding.EncodeToString(make([]byte, 16)), wantErr: true},
		{name: "an over-long key is rejected", in: base64.StdEncoding.EncodeToString(make([]byte, 64)), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := vault.ParseKey(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != vault.KeySize {
				t.Errorf("key length = %d, want %d", len(got), vault.KeySize)
			}
		})
	}
}
