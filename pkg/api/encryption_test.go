package api

import (
	"testing"

	"nms/pkg/models"
)

func TestDecryptStructOnNonEncryptedEntity(t *testing.T) {
	key := "1234567890123456789012345678901212345678901234567890123456789012"
	dev := models.Device{ID: 1, Hostname: "host1", IPAddress: "10.0.0.1", Status: "active"}
	out, err := DecryptStruct(dev, key)
	if err != nil {
		t.Fatalf("DecryptStruct(Device) error = %v, want nil", err)
	}
	if out.Hostname != "host1" {
		t.Fatalf("Device hostname = %q, want host1", out.Hostname)
	}

	dp := models.DiscoveryProfile{ID: 2, Name: "p", Target: "10.0.0.0/24", Port: 22, CredentialProfileID: 1}
	out2, err := DecryptStruct(dp, key)
	if err != nil {
		t.Fatalf("DecryptStruct(DiscoveryProfile) error = %v, want nil", err)
	}
	if out2.Name != "p" {
		t.Fatalf("DiscoveryProfile name = %q, want p", out2.Name)
	}
}

func TestDecryptStructCredentialRoundTrip(t *testing.T) {
	key := "1234567890123456789012345678901212345678901234567890123456789012"
	cred := models.CredentialProfile{ID: 1, Name: "win", Protocol: "winrm", Payload: `{"user":"u","pass":"p"}`}
	enc, err := EncryptStruct(cred, key)
	if err != nil {
		t.Fatalf("EncryptStruct error = %v", err)
	}
	if enc.Payload == cred.Payload {
		t.Fatal("payload was not encrypted")
	}
	dec, err := DecryptStruct(enc, key)
	if err != nil {
		t.Fatalf("DecryptStruct error = %v", err)
	}
	if dec.Payload != cred.Payload {
		t.Fatalf("decrypted payload = %q, want %q", dec.Payload, cred.Payload)
	}
}
