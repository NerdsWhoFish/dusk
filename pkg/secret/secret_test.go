package secret_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/FetchHQ/dusk/pkg/secret"
)

const sensitive = "sk-live-do-not-leak-me"

// A leak through logs or an API response is likelier than a stolen disk, and
// encryption does nothing for it. Every rendering path must redact.
func TestADR0022_SecretsNeverRenderThemselves(t *testing.T) {
	s := secret.New(sensitive)

	tests := []struct {
		name   string
		render func() string
	}{
		{"String()", func() string { return s.String() }},
		{"fmt %v", func() string { return fmt.Sprintf("%v", s) }},
		// Exercising the %s verb is the point, so String() is not equivalent.
		{"fmt %s", func() string { return fmt.Sprintf("%s", s) }}, //nolint:staticcheck
		{"fmt %q", func() string { return fmt.Sprintf("%q", s) }},
		{"fmt %#v", func() string { return fmt.Sprintf("%#v", s) }},
		{"inside a struct", func() string { return fmt.Sprintf("%v", struct{ Token secret.String }{s}) }},
		{"json.Marshal", func() string {
			b, err := json.Marshal(s)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			return string(b)
		}},
		{"json.Marshal inside a struct", func() string {
			b, err := json.Marshal(struct {
				Token secret.String `json:"token"`
			}{s})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			return string(b)
		}},
		{"slog", func() string {
			var buf bytes.Buffer
			slog.New(slog.NewTextHandler(&buf, nil)).Info("hello", "token", s)
			return buf.String()
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.render()
			if strings.Contains(got, sensitive) {
				t.Fatalf("secret leaked: %s", got)
			}
			if !strings.Contains(got, secret.Redacted) {
				t.Errorf("want %q in output, got %q", secret.Redacted, got)
			}
		})
	}
}

func TestReveal(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantZero   bool
		wantReveal string
	}{
		{name: "a set value is retrievable", in: sensitive, wantReveal: sensitive},
		{name: "the zero value reveals empty and reports zero", wantZero: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := secret.New(tt.in)
			if got := s.Reveal(); got != tt.wantReveal {
				t.Errorf("Reveal() = %q, want %q", got, tt.wantReveal)
			}
			if got := s.IsZero(); got != tt.wantZero {
				t.Errorf("IsZero() = %v, want %v", got, tt.wantZero)
			}
		})
	}
}

func TestUnmarshalAcceptsRealValues(t *testing.T) {
	var got struct {
		Token secret.String `json:"token"`
	}
	if err := json.Unmarshal([]byte(`{"token":"`+sensitive+`"}`), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Token.Reveal() != sensitive {
		t.Errorf("Reveal() = %q, want %q", got.Token.Reveal(), sensitive)
	}
	if s := fmt.Sprintf("%v", got.Token); s != secret.Redacted {
		t.Errorf("round-tripped secret renders as %q, want %q", s, secret.Redacted)
	}
}
