package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// KeyID identifies which key encrypted a value.
const DefaultKeyID = "env:MEETINGCTL_ENCRYPTION_KEY"

// Box encrypts and decrypts sensitive meeting text.
type Box struct {
	keyID string
	gcm   cipher.AEAD
}

// NewBoxFromEnv loads a 32-byte AES key from MEETINGCTL_ENCRYPTION_KEY.
// The key may be 64 hex chars or 44 standard base64 chars (32 raw bytes).
func NewBoxFromEnv() (*Box, error) {
	raw := strings.TrimSpace(os.Getenv("MEETINGCTL_ENCRYPTION_KEY"))
	if raw == "" {
		return nil, errors.New("MEETINGCTL_ENCRYPTION_KEY is required (32-byte key as hex or base64)")
	}
	key, err := decodeKey(raw)
	if err != nil {
		return nil, err
	}
	return NewBox(DefaultKeyID, key)
}

// NewBox builds a Box from a raw 32-byte key.
func NewBox(keyID string, key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if keyID == "" {
		keyID = DefaultKeyID
	}
	return &Box{keyID: keyID, gcm: gcm}, nil
}

// Seal encrypts plaintext and returns keyID, nonce, and ciphertext (all base64 for storage).
func (b *Box) Seal(plaintext string) (keyID, nonceB64, cipherB64 string, err error) {
	nonce := make([]byte, b.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", "", "", err
	}
	sealed := b.gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return b.keyID,
		base64.StdEncoding.EncodeToString(nonce),
		base64.StdEncoding.EncodeToString(sealed),
		nil
}

// Open decrypts a sealed value.
func (b *Box) Open(keyID, nonceB64, cipherB64 string) (string, error) {
	if keyID != "" && keyID != b.keyID {
		return "", fmt.Errorf("unknown encryption key id %q", keyID)
	}
	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil {
		return "", fmt.Errorf("decode nonce: %w", err)
	}
	sealed, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	plain, err := b.gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plain), nil
}

// KeyID returns the active key identifier.
func (b *Box) KeyID() string {
	return b.keyID
}

func decodeKey(raw string) ([]byte, error) {
	if len(raw) == 64 {
		if key, err := hex.DecodeString(raw); err == nil && len(key) == 32 {
			return key, nil
		}
	}
	if key, err := base64.StdEncoding.DecodeString(raw); err == nil && len(key) == 32 {
		return key, nil
	}
	if key, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(key) == 32 {
		return key, nil
	}
	return nil, errors.New("encryption key must be 32 bytes encoded as 64 hex chars or base64")
}

// GenerateKey returns a random 32-byte key as hex (for setup docs/tests).
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}
