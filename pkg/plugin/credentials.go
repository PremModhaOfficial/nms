package plugin

import (
	"nms/pkg/database"
	"nms/pkg/models"
)

// DecryptPayload decrypts a CredentialProfile and returns the raw payload string.
// The payload format is protocol-specific; plugins parse it themselves.
func DecryptPayload(cred *models.CredentialProfile) (string, error) {
	if cred == nil {
		return "", nil
	}

	decrypted, err := database.DecryptStruct(*cred)
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
