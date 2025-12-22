package rca

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/masterzen/winrm"
)

func TestCredentialParsing(t *testing.T) {
	// This mimics what the plugin does
	rawJSON := []byte(`"{\"username\":\"vboxuser\",\"password\":\"admin\"}"`)

	var credsStr string
	if err := json.Unmarshal(rawJSON, &credsStr); err != nil {
		t.Fatalf("Failed to unmarshal to string: %v", err)
	}

	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(credsStr), &creds); err != nil {
		t.Fatalf("Failed to unmarshal string to struct: %v", err)
	}

	if creds.Username != "vboxuser" || creds.Password != "admin" {
		t.Errorf("Mismatch! Got %+v", creds)
	}
}

func TestWinRM_RCA(t *testing.T) {
	target := "127.0.0.1"
	port := 15985
	user := "vboxuser"
	pass := "admin"

	tests := []struct {
		name      string
		useHTTPS  bool
		useNTLM   bool
		plaintext bool
	}{
		{
			name:      "Basic Auth - HTTP - Plaintext",
			useHTTPS:  false,
			useNTLM:   false,
			plaintext: true,
		},
		{
			name:      "Basic Auth - HTTP - Default",
			useHTTPS:  false,
			useNTLM:   false,
			plaintext: false,
		},
		{
			name:      "NTLM Auth - HTTP (Diagnostic Only)",
			useHTTPS:  false,
			useNTLM:   true,
			plaintext: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint := winrm.NewEndpoint(target, port, tt.useHTTPS, !tt.plaintext, nil, nil, nil, 10*time.Second)

			params := winrm.DefaultParameters
			if tt.useNTLM {
				params.TransportDecorator = func() winrm.Transporter { return &winrm.ClientNTLM{} }
			}

			client, err := winrm.NewClientWithParameters(endpoint, user, pass, params)
			if err != nil {
				t.Fatalf("Failed to create client: %v", err)
			}

			stdout, stderr, exitCode, err := client.RunWithString("hostname", "")
			if err != nil {
				fmt.Printf("[%s] ERROR: %v\n", tt.name, err)
			} else {
				fmt.Printf("[%s] SUCCESS: ExitCode=%d, Stdout=%s, Stderr=%s\n", tt.name, exitCode, stdout, stderr)
			}
		})
	}
}
