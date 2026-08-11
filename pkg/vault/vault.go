// Package vault seals secrets with envelope encryption.
//
// A random data key encrypts the payload; the master key encrypts the data
// key. Rotating the master key therefore rewrites a small wrapped blob rather
// than re-encrypting everything, which is what makes rotation possible at all.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// KeySize is the required master key length in bytes.
const KeySize = 32

// ErrWrongKey is returned when a blob cannot be opened with the given key,
// which in practice means the master key changed.
var ErrWrongKey = errors.New("vault: cannot decrypt, wrong encryption key")

// Envelope is the stored form. The data key is wrapped by the master key, so
// the payload never has to be rewritten when the master key rotates.
type Envelope struct {
	Version    int    `json:"v"`
	WrappedKey []byte `json:"wrapped_key"`
	Payload    []byte `json:"payload"`
}

// ParseKey decodes a base64 master key and checks its length, so a bad key
// fails at boot rather than on first decrypt.
func ParseKey(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, errors.New("vault: encryption key is empty")
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("vault: encryption key is not valid base64: %w", err)
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("vault: encryption key must be %d bytes, got %d", KeySize, len(key))
	}
	return key, nil
}

// NewKey generates a master key, base64 encoded and ready for the environment.
func NewKey() (string, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("vault: generate key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

// Seal encrypts plaintext under a fresh data key wrapped by master.
func Seal(master, plaintext []byte) ([]byte, error) {
	if len(master) != KeySize {
		return nil, fmt.Errorf("vault: master key must be %d bytes", KeySize)
	}

	dataKey := make([]byte, KeySize)
	if _, err := rand.Read(dataKey); err != nil {
		return nil, fmt.Errorf("vault: generate data key: %w", err)
	}

	payload, err := sealWith(dataKey, plaintext)
	if err != nil {
		return nil, err
	}
	wrapped, err := sealWith(master, dataKey)
	if err != nil {
		return nil, err
	}

	return json.Marshal(Envelope{Version: 1, WrappedKey: wrapped, Payload: payload})
}

// Open decrypts a sealed envelope.
func Open(master, blob []byte) ([]byte, error) {
	if len(master) != KeySize {
		return nil, fmt.Errorf("vault: master key must be %d bytes", KeySize)
	}

	var env Envelope
	if err := json.Unmarshal(blob, &env); err != nil {
		return nil, fmt.Errorf("vault: malformed envelope: %w", err)
	}
	if env.Version != 1 {
		return nil, fmt.Errorf("vault: unsupported envelope version %d", env.Version)
	}

	dataKey, err := openWith(master, env.WrappedKey)
	if err != nil {
		return nil, err
	}
	return openWith(dataKey, env.Payload)
}

// Rewrap re-encrypts the data key under a new master without touching the
// payload, which is what makes key rotation cheap.
func Rewrap(oldMaster, newMaster, blob []byte) ([]byte, error) {
	var env Envelope
	if err := json.Unmarshal(blob, &env); err != nil {
		return nil, fmt.Errorf("vault: malformed envelope: %w", err)
	}

	dataKey, err := openWith(oldMaster, env.WrappedKey)
	if err != nil {
		return nil, err
	}
	wrapped, err := sealWith(newMaster, dataKey)
	if err != nil {
		return nil, err
	}

	env.WrappedKey = wrapped
	return json.Marshal(env)
}

func sealWith(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("vault: generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func openWith(key, blob []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, ErrWrongKey
	}
	nonce, ct := blob[:gcm.NonceSize()], blob[gcm.NonceSize():]

	out, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrWrongKey
	}
	return out, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("vault: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("vault: new gcm: %w", err)
	}
	return gcm, nil
}
