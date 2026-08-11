// Package secret provides a string type that will not render itself.
//
// Accidental exposure through a log line or an API response is a far more
// likely failure than a stolen disk, and encryption does nothing for it.
package secret

import (
	"encoding/json"
	"log/slog"
)

// Redacted is what a String renders as everywhere except Reveal.
const Redacted = "[REDACTED]"

// String holds a secret value. Its String, MarshalJSON, and LogValue all
// render Redacted, so leaking one takes deliberate effort.
type String struct {
	v string
}

// New wraps a sensitive value.
func New(v string) String { return String{v: v} }

// Reveal returns the underlying value. Every call site is a place to check.
func (s String) Reveal() string { return s.v }

// IsZero reports whether no value is set, without revealing it.
func (s String) IsZero() bool { return s.v == "" }

func (s String) String() string { return Redacted }

// GoString covers %#v, which would otherwise print the struct field.
func (s String) GoString() string { return Redacted }

// MarshalJSON renders Redacted, so a secret cannot reach an API response.
func (s String) MarshalJSON() ([]byte, error) { return json.Marshal(Redacted) }

// UnmarshalJSON accepts a real value, since input is how secrets arrive.
func (s *String) UnmarshalJSON(b []byte) error {
	var v string
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	s.v = v
	return nil
}

// LogValue makes slog render Redacted, including inside structs.
func (s String) LogValue() slog.Value { return slog.StringValue(Redacted) }
