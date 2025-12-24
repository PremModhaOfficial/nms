package api

import (
	"encoding/json"
	"nms/pkg/models"
	"reflect"

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

	// gocrypt v1.1.0 only supports string fields.
	// To support json.RawMessage, we need to handle it manually or use a trick.
	// Since we want the API to accept JSON objects, we use json.RawMessage in the model.
	// Before encrypting, if a field is json.RawMessage, we convert it to a string.
	// But gocrypt works on the struct via reflection.

	err = gc.Encrypt(&entity)
	if err != nil {
		return entity, err
	}

	// Special handling for json.RawMessage because gocrypt might have skipped it
	// We'll use reflection to find json.RawMessage fields with gocrypt tag
	if err := handleRawMessageFields(&entity, secretKey, true); err != nil {
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

	// Special handling for json.RawMessage
	if err := handleRawMessageFields(&entity, secretKey, false); err != nil {
		return entity, err
	}

	return entity, nil
}

func handleRawMessageFields(entity interface{}, secretKey string, encrypt bool) error {
	v := reflect.ValueOf(entity)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return nil
	}
	v = v.Elem()
	t := v.Type()

	aesOpt, err := gocrypt.NewAESOpt(secretKey)
	if err != nil {
		return err
	}
	gc := gocrypt.New(&gocrypt.Option{AESOpt: aesOpt})

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("gocrypt")
		if tag == "" {
			continue
		}

		val := v.Field(i)
		if val.Type() == reflect.TypeOf(json.RawMessage{}) {
			raw := val.Interface().(json.RawMessage)
			if len(raw) == 0 {
				continue
			}

			if encrypt {
				// Encrypt: raw bytes -> string -> encrypt -> base64 string -> json.RawMessage (quoted string)
				// If it's already a quoted string, it might be encrypted.
				// But here we expect the API input which is a raw JSON object/value.

				var dataToEncrypt string
				// If it starts with { or [, it's a JSON object/array.
				// If it starts with ", it's a JSON string.
				dataToEncrypt = string(raw)

				encrypted, err := gc.AESOpt.Encrypt([]byte(dataToEncrypt))
				if err != nil {
					return err
				}

				// Store as a JSON-quoted string so it's still valid json.RawMessage
				quoted, _ := json.Marshal(encrypted)
				val.Set(reflect.ValueOf(json.RawMessage(quoted)))
			} else {
				// Decrypt: json.RawMessage (quoted string) -> unquote -> decrypt -> raw bytes
				var encrypted string
				if err := json.Unmarshal(raw, &encrypted); err != nil {
					// If fail to unmarshal, it might not be a quoted string (e.g. raw JSON during dev)
					// Skip decryption and let it be
					continue
				}

				decrypted, err := gc.AESOpt.Decrypt([]byte(encrypted))
				if err != nil {
					// If decryption fails, it might not be encrypted. Skip.
					continue
				}

				val.Set(reflect.ValueOf(json.RawMessage(decrypted)))
			}
		}
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
		// Fallback: If it's already raw JSON (starts with {), use it as is
		// This handles unencrypted data in the db during development/migration
		if len(cred.Payload) > 0 && cred.Payload[0] == '{' {
			return cred.Payload, nil
		}
		return nil, err
	}

	return decrypted.Payload, nil
}
