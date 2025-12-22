package rca

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"nms/pkg/database"
	"nms/pkg/models"
	"nms/pkg/plugin"

	"github.com/masterzen/winrm"
)

func TestWinRM_FromDB(t *testing.T) {
	// 1. Connect to DB
	db, err := database.Connect()
	if err != nil {
		t.Fatalf("Failed to connect to DB: %v", err)
	}

	// 2. Get latest credential profile
	var cred models.CredentialProfile
	if err := db.Order("id desc").First(&cred).Error; err != nil {
		t.Fatalf("Failed to get credential from DB: %v", err)
	}
	fmt.Printf("Testing Credential ID: %d, Name: %s\n", cred.ID, cred.Name)

	// 3. Decrypt payload using poller logic
	payload, err := plugin.DecryptPayload(&cred)
	if err != nil {
		t.Fatalf("Poller-style decryption failed: %v", err)
	}
	fmt.Printf("Decrypted Payload: %s\n", payload)

	// 4. Try WinRM connection
	target := "127.0.0.1"
	port := 15985

	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(payload), &creds); err != nil {
		t.Fatalf("Failed to parse credentials: %v", err)
	}

	endpoint := winrm.NewEndpoint(target, port, false, true, nil, nil, nil, 10*time.Second)
	client, err := winrm.NewClient(endpoint, creds.Username, creds.Password)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	stdout, stderr, exitCode, err := client.RunWithString("hostname", "")
	if err != nil {
		t.Fatalf("Connection failed: %v", err)
	}

	fmt.Printf("SUCCESS: ExitCode=%d, Stdout=%s\n", exitCode, stdout)
	if stderr != "" {
		fmt.Printf("Stderr: %s\n", stderr)
	}
}
