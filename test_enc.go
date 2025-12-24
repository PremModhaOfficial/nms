package main

import (
	"encoding/json"
	"fmt"
	"nms/pkg/api"
	"nms/pkg/models"
)

func main() {
	key := "1234567890123456789012345678901212345678901234567890123456789012"
	payload := json.RawMessage(`{"username":"vboxuser","password":"admin"}`)
	cred := models.CredentialProfile{
		Name:    "Test",
		Payload: payload,
	}

	fmt.Printf("Original Payload: %s\n", string(cred.Payload))

	enc, err := api.EncryptStruct(cred, key)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Encrypted Payload (RawMessage): %s\n", string(enc.Payload))

	dec, err := api.DecryptStruct(enc, key)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Decrypted Payload: %s\n", string(dec.Payload))

	// Test DecryptPayload
	raw, err := api.DecryptPayload(&enc, key)
	if err != nil {
		panic(err)
	}
	fmt.Printf("DecryptPayload result: %s\n", string(raw))
}
