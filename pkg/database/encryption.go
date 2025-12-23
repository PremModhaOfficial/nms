package database

import (
	"nms/pkg/models"

	"github.com/firdasafridi/gocrypt"
)

// EncryptStruct encrypts the fields tagged with gocrypt using the provided secret key.
func EncryptStruct[T any](entity T, secretKey string) (T, error) {
	aesOpt, err := gocrypt.NewAESOpt(secretKey)
	if err != nil {
		return entity, err
	}

	opt := &gocrypt.Option{
		AESOpt: aesOpt,
	}

	gc := gocrypt.New(opt)
	err = gc.Encrypt(&entity)
	if err != nil {
		return entity, err
	}
	return entity, nil
}

// DecryptStruct decrypts the fields tagged with gocrypt using the provided secret key.
func DecryptStruct[T any](entity T, secretKey string) (T, error) {
	aesOpt, err := gocrypt.NewAESOpt(secretKey)
	if err != nil {
		return entity, err
	}

	opt := &gocrypt.Option{
		AESOpt: aesOpt,
	}

	gc := gocrypt.New(opt)
	err = gc.Decrypt(&entity)
	if err != nil {
		return entity, err
	}
	return entity, nil
}

// DecryptPayload decrypts a CredentialProfile and returns the raw payload string.
// The payload format is protocol-specific; plugins parse it themselves.
func DecryptPayload(cred *models.CredentialProfile, secretKey string) (string, error) {
	if cred == nil {
		return "", nil
	}

	decrypted, err := DecryptStruct(*cred, secretKey)
	if err != nil {
		// Fallback: If it's already raw JSON (starts with {), use it as is
		// This handles unencrypted data in the DB during development/migration
		if len(cred.Payload) > 0 && cred.Payload[0] == '{' {
			return cred.Payload, nil
		}
		return "", err
	}

	return decrypted.Payload, nil
}
