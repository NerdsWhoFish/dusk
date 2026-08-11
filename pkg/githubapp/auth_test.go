package githubapp_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FetchHQ/dusk/pkg/githubapp"
	"github.com/FetchHQ/dusk/pkg/secret"
)

var testKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
})

func pkcs1App(t *testing.T) githubapp.App {
	t.Helper()
	encoded := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(testKey()),
	})
	return githubapp.App{ID: 42, PrivateKey: secret.New(string(encoded))}
}

func TestJWT(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

	assertion, err := pkcs1App(t).JWT(now)
	if err != nil {
		t.Fatalf("JWT: %v", err)
	}

	parts := strings.Split(assertion.Reveal(), ".")
	if len(parts) != 3 {
		t.Fatalf("assertion has %d segments, want 3", len(parts))
	}

	t.Run("GitHub can verify the signature", func(t *testing.T) {
		signature, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			t.Fatalf("decode signature: %v", err)
		}
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err := rsa.VerifyPKCS1v15(&testKey().PublicKey, crypto.SHA256, digest[:], signature); err != nil {
			t.Errorf("signature does not verify: %v", err)
		}
	})

	t.Run("the claims are what GitHub requires", func(t *testing.T) {
		var claims struct {
			Iat int64 `json:"iat"`
			Exp int64 `json:"exp"`
			Iss int64 `json:"iss"`
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			t.Fatalf("decode claims: %v", err)
		}
		if err := json.Unmarshal(payload, &claims); err != nil {
			t.Fatalf("parse claims: %v", err)
		}

		if claims.Iss != 42 {
			t.Errorf("iss = %d, want the app id 42", claims.Iss)
		}
		// GitHub rejects an assertion issued in its own future, so iat is
		// backdated, and it rejects a lifetime over ten minutes.
		if claims.Iat >= now.Unix() {
			t.Errorf("iat = %d, want it backdated below %d", claims.Iat, now.Unix())
		}
		if lifetime := time.Duration(claims.Exp-claims.Iat) * time.Second; lifetime > 10*time.Minute {
			t.Errorf("lifetime = %s, want at most 10m", lifetime)
		}
	})

	t.Run("the assertion is redacted when logged", func(t *testing.T) {
		if !strings.Contains(assertion.String(), "REDACTED") {
			t.Errorf("String() = %q, want it redacted", assertion.String())
		}
	})
}

func TestJWTKeyEncodings(t *testing.T) {
	pkcs8, err := x509.MarshalPKCS8PrivateKey(testKey())
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}

	tests := []struct {
		name string
		app  githubapp.App
		ok   bool
	}{
		{"the PKCS#1 key GitHub issues is accepted", pkcs1App(t), true},
		{
			name: "a PKCS#8 key is accepted",
			app: githubapp.App{ID: 42, PrivateKey: secret.New(string(pem.EncodeToMemory(
				&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})))},
			ok: true,
		},
		{
			name: "a key that is not PEM is rejected",
			app:  githubapp.App{ID: 42, PrivateKey: secret.New("clearly not a key")},
		},
		{
			name: "a missing key says Dusk is not onboarded",
			app:  githubapp.App{ID: 42},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.app.JWT(time.Now())
			if tt.ok && err != nil {
				t.Errorf("JWT: %v", err)
			}
			if !tt.ok && err == nil {
				t.Error("JWT succeeded, want an error")
			}
		})
	}
}

func TestInstallationToken(t *testing.T) {
	var gotAuth, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("Authorization")
		gotPath = req.URL.Path
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"ghs_secret","expires_at":"2026-08-11T13:00:00Z"}`))
	}))
	defer server.Close()

	client := &githubapp.Client{BaseURL: server.URL}
	token, err := client.InstallationToken(t.Context(), pkcs1App(t), 99)
	if err != nil {
		t.Fatalf("InstallationToken: %v", err)
	}

	if got, want := token.Token.Reveal(), "ghs_secret"; got != want {
		t.Errorf("token = %q, want %q", got, want)
	}
	if got, want := gotPath, "/app/installations/99/access_tokens"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if !strings.HasPrefix(gotAuth, "Bearer eyJ") {
		t.Errorf("Authorization = %q, want a bearer assertion", gotAuth)
	}
}

func TestInstallationTokenReportsGitHubsMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
	}))
	defer server.Close()

	client := &githubapp.Client{BaseURL: server.URL}
	_, err := client.InstallationToken(t.Context(), pkcs1App(t), 99)
	if err == nil {
		t.Fatal("InstallationToken succeeded on 403, want an error")
	}
	// GitHub's own message usually names the missing permission, so losing it
	// turns a fixable problem into a mystery.
	if !strings.Contains(err.Error(), "not accessible by integration") {
		t.Errorf("error = %q, want GitHub's message", err)
	}
}

func TestTokensAreReusedUntilNearExpiry(t *testing.T) {
	minted := 0
	expiry := time.Date(2026, 8, 11, 13, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		minted++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"ghs_secret","expires_at":"` + expiry.Format(time.RFC3339) + `"}`))
	}))
	defer server.Close()

	now := expiry.Add(-time.Hour)
	tokens := &githubapp.Tokens{
		Client: &githubapp.Client{BaseURL: server.URL},
		App:    pkcs1App(t),
		Now:    func() time.Time { return now },
	}

	for range 3 {
		if _, err := tokens.Token(t.Context(), 99); err != nil {
			t.Fatalf("Token: %v", err)
		}
	}
	if minted != 1 {
		t.Errorf("minted %d tokens, want 1 reused across three reads", minted)
	}

	t.Run("a token close to expiring is replaced early", func(t *testing.T) {
		now = expiry.Add(-time.Minute)
		if _, err := tokens.Token(t.Context(), 99); err != nil {
			t.Fatalf("Token: %v", err)
		}
		if minted != 2 {
			t.Errorf("minted %d tokens, want a second once the first was nearly expired", minted)
		}
	})

	t.Run("installations do not share a token", func(t *testing.T) {
		if _, err := tokens.Token(t.Context(), 100); err != nil {
			t.Fatalf("Token: %v", err)
		}
		if minted != 3 {
			t.Errorf("minted %d tokens, want a separate one per installation", minted)
		}
	})
}
