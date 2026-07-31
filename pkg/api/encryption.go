package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"

	"nms/pkg/models"
)

// newAEAD builds an AES-256-GCM cipher from a 64-char hex key. The previous
// gocrypt dependency used exactly this layout (nonce-prefixed, hex-encoded), so
// existing stored ciphertext remains decryptable with no migration.
func newAEAD(secretKey string) (cipher.AEAD, error) {
	if len(secretKey) != 64 {
		return nil, fmt.Errorf("encryption key must be 64 hex characters, got %d", len(secretKey))
	}
	key, err := hex.DecodeString(secretKey)
	if err != nil {
		return nil, fmt.Errorf("invalid hex encryption key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// encryptString encrypts plain with AES-256-GCM and hex-encodes the
// nonce-prefixed ciphertext.
func encryptString(aead cipher.AEAD, plain string) (string, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := aead.Seal(nonce, nonce, []byte(plain), nil)
	return hex.EncodeToString(ciphertext), nil
}

// decryptString decodes and decrypts the nonce-prefixed hex ciphertext.
func decryptString(aead cipher.AEAD, encoded string) (string, error) {
	ciphertext, err := hex.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("invalid hex ciphertext: %w", err)
	}
	nonceSize := aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	plain, err := aead.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// EncryptStruct encrypts string fields tagged with `gocrypt:"aes"` in place.
// Empty strings are left untouched so partial updates can omit them.
func EncryptStruct[T any](entity T, secretKey string) (T, error) {
	aead, err := newAEAD(secretKey)
	if err != nil {
		return entity, err
	}
	if err := transformStringFields(&entity, aead, encryptString); err != nil {
		return entity, err
	}
	return entity, nil
}

// DecryptStruct decrypts string fields tagged with `gocrypt:"aes"` in place.
func DecryptStruct[T any](entity T, secretKey string) (T, error) {
	aead, err := newAEAD(secretKey)
	if err != nil {
		return entity, err
	}
	if err := transformStringFields(&entity, aead, decryptString); err != nil {
		return entity, err
	}
	return entity, nil
}

// transformStringFields applies fn to each exported string field tagged
// gocrypt:"aes". Only CredentialProfile.Payload carries the tag today.
func transformStringFields(entity any, aead cipher.AEAD, fn func(cipher.AEAD, string) (string, error)) error {
	v := reflect.ValueOf(entity)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return nil
	}
	v = v.Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.Tag.Get("gocrypt") != "aes" {
			continue
		}
		fv := v.Field(i)
		if fv.Kind() != reflect.String || fv.String() == "" {
			continue
		}
		out, err := fn(aead, fv.String())
		if err != nil {
			return fmt.Errorf("failed to process field %s: %w", field.Name, err)
		}
		fv.SetString(out)
	}
	return nil
}

// DecryptPayload decrypts a CredentialProfile and returns the raw payload.
// The payload format is protocol-specific; plugins parse it themselves.
func DecryptPayload(cred *models.CredentialProfile, secretKey string) (json.RawMessage, error) {
	if cred == nil {
		return nil, nil
	}

	decrypted, err := DecryptStruct(*cred, secretKey)
	if err != nil {
		// Fallback exists for unencrypted data written during development/migration.
		// It is gated on a `{` prefix (hex ciphertext never starts with `{`) AND on
		// non-production (a real key-rotation failure must not be silently masked
		// in production), and it is logged at WARN.
		slog.Warn("Decryption failed, checking raw fallback", "credential_id", cred.ID, "error", err)
		if os.Getenv("APP_ENV") != "production" && len(cred.Payload) > 0 && cred.Payload[0] == '{' {
			return json.RawMessage(cred.Payload), nil
		}
		return nil, err
	}

	slog.Debug("Decryption successful", "credential_id", cred.ID)
	return json.RawMessage(decrypted.Payload), nil
}
