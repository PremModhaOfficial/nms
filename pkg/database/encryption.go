package database

import (
	"log"
	"os"

	"github.com/firdasafridi/gocrypt"
)

var secretKey string

func init() {
	secretKey = os.Getenv("NMS_SECRET")
	if secretKey == "" {
		// Default key for development (64 bytes)
		secretKey = "1234567890123456789012345678901212345678901234567890123456789012" 
		log.Println("WARNING: NMS_SECRET not set. Using default insecure key.")
	}
}

// EncryptStruct encrypts the fields tagged with gocrypt
func EncryptStruct[T any](entity T) (T, error) {
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

// DecryptStruct decrypts the fields tagged with gocrypt
func DecryptStruct[T any](entity T) (T, error) {
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
