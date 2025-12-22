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
		return "", err
	}

	return decrypted.Payload, nil
}
