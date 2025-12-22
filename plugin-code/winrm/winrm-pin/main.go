package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/masterzen/winrm"
)

// Input represents the task received from the core
type Input struct {
	RequestID   string      `json:"request_id"`
	Target      string      `json:"target"`
	IP          string      `json:"IP"` // Alias for target to support DiscoveryTask struct
	Port        int         `json:"port"`
	Credentials Credentials `json:"credentials"`
}

type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Domain   string `json:"domain,omitempty"` // Optional domain for NTLM
}

// Output represents the result sent back to the core
type Output struct {
	RequestID string   `json:"request_id"`
	Status    string   `json:"status"` // "success" or "failed"
	Error     string   `json:"error,omitempty"`
	Metrics   []Metric `json:"metrics,omitempty"`
	Hostname  string   `json:"hostname,omitempty"` // Populated in discovery mode
}

type Metric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

// Flags
var (
	discoveryMode = flag.Bool("discovery", false, "Run in discovery mode to fetch system hostname")
	timeout       = flag.Duration("timeout", 30*time.Second, "Connection timeout")
)

func main() {
	// 1. Parse flags
	flag.Parse()

	// 2. Configure logging to Stderr (keeping Stdout clean for JSON)
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ltime | log.Lshortfile)

	// 3. Read Input from Stdin
	inputData, err := io.ReadAll(os.Stdin)
	if err != nil {
		log.Fatalf("Failed to read Stdin: %v", err)
	}
	if len(inputData) == 0 {
		return // Nothing to do
	}

	var inputs []Input
	if err := json.Unmarshal(inputData, &inputs); err != nil {
		log.Fatalf("Invalid JSON input: %v", err)
	}

	// 4. Process Tasks (Concurrent)
	outputs := make([]Output, len(inputs))
	var wg sync.WaitGroup

	for i, task := range inputs {
		wg.Add(1)
		go func(i int, t Input) {
			defer wg.Done()
			outputs[i] = processTask(t)
		}(i, task)
	}

	wg.Wait()

	// 5. Write Output to Stdout
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ") // Pretty print for readability
	if err := encoder.Encode(outputs); err != nil {
		log.Fatalf("Failed to write JSON output: %v", err)
	}
}

func processTask(task Input) Output {
	out := Output{
		RequestID: task.RequestID,
		Status:    "failed",
	}

	// Determine target
	target := task.Target
	if target == "" {
		target = task.IP
	}

	// Default WinRM port
	port := task.Port
	if port == 0 {
		port = 5985
	}

	// Create Endpoint
	endpoint := winrm.NewEndpoint(
		target,
		port,
		false, // HTTPS? (False for 5985)
		true,  // Insecure (Skip verification)
		nil, nil, nil,
		*timeout,
	)

	// Create Client
	var client *winrm.Client
	var err error

	if task.Credentials.Domain != "" {
		// Use NTLM if domain is present
		params := winrm.DefaultParameters
		params.TransportDecorator = func() winrm.Transporter { return &winrm.ClientNTLM{} }
		client, err = winrm.NewClientWithParameters(
			endpoint,
			fmt.Sprintf("%s\\%s", task.Credentials.Domain, task.Credentials.Username),
			task.Credentials.Password,
			params,
		)
	} else {
		// Basic Auth
		client, err = winrm.NewClient(endpoint, task.Credentials.Username, task.Credentials.Password)
	}

	if err != nil {
		out.Error = fmt.Sprintf("Failed to create client: %v", err)
		return out
	}

	// Execute Logic based on Mode
	if *discoveryMode {
		return runDiscovery(client, out)
	}
	return runPolling(client, out)
}

func runDiscovery(client *winrm.Client, out Output) Output {
	// Command: hostname
	stdout, stderr, exitCode, err := client.RunWithString("hostname", "")
	if err != nil {
		out.Error = fmt.Sprintf("Connection error: %v", err)
		return out
	}
	if exitCode != 0 {
		out.Error = fmt.Sprintf("Hostname command failed (exit %d): %s", exitCode, stderr)
		return out
	}

	out.Status = "success"
	out.Hostname = strings.TrimSpace(stdout)
	return out
}

func runPolling(client *winrm.Client, out Output) Output {
	// Simple polling example: Check if we can echo
	// In a real simplified plugin, maybe we just return 1 metric
	// Command: Write-Output "OK"
	_, stderr, exitCode, err := client.RunWithString("echo OK", "")
	if err != nil {
		out.Error = fmt.Sprintf("Connection error: %v", err)
		return out
	}
	if exitCode != 0 {
		out.Error = fmt.Sprintf("Poll command failed (exit %d): %s", exitCode, stderr)
		return out
	}

	out.Status = "success"
	out.Metrics = []Metric{
		{Name: "system.status", Value: 1},
	}
	return out
}
