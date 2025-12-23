package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/masterzen/winrm"
	"golang.org/x/text/encoding/unicode"
)

// Input aligns with plugin.Task from pkg/plugin/types.go but adds compatibility for tests
type Input struct {
	MonitorID   int64           `json:"monitor_id,omitempty"`
	RequestID   string          `json:"request_id,omitempty"` // For test compatibility
	Target      string          `json:"target"`
	Port        int             `json:"port"`
	Credentials json.RawMessage `json:"credentials,omitempty"` // Flexible: string or object
	IP          string          `json:"IP,omitempty"`          // Legacy/Alias
}

// WinRMCreds structure expected in the decrypted string or raw object
type WinRMCreds struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Domain   string `json:"domain,omitempty"`
}

// Output aligns with plugin.Result from pkg/plugin/types.go
type Output struct {
	MonitorID int64           `json:"monitor_id,omitempty"`
	RequestID string          `json:"request_id,omitempty"` // Echo back for tests
	Target    string          `json:"target"`
	Port      int             `json:"port"`
	Success   bool            `json:"success"`
	Error     string          `json:"error,omitempty"`
	Hostname  string          `json:"hostname,omitempty"`
	Metrics   []Metric        `json:"metrics,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

type Metric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

var (
	discoveryMode = flag.Bool("discovery", false, "Run in discovery mode")
	timeout       = flag.Duration("timeout", 60*time.Second, "Timeout for WinRM commands")
)

func main() {
	flag.Parse()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	inputData, err := io.ReadAll(os.Stdin)
	if err != nil {
		slog.Error("Failed to read Stdin", "error", err)
		os.Exit(1)
	}
	if len(inputData) == 0 {
		return
	}

	var inputs []Input
	if err := json.Unmarshal(inputData, &inputs); err != nil {
		slog.Error("Invalid JSON input", "error", err)
		os.Exit(1)
	}

	outputs := make([]Output, len(inputs))
	var wg sync.WaitGroup

	for i, task := range inputs {
		wg.Add(1)
		go func(idx int, t Input) {
			defer wg.Done()
			outputs[idx] = processTask(t)
		}(i, task)
	}

	wg.Wait()

	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(outputs); err != nil {
		slog.Error("Failed to write output", "error", err)
		os.Exit(1)
	}
}

func processTask(task Input) Output {
	out := Output{
		MonitorID: task.MonitorID,
		RequestID: task.RequestID,
		Target:    task.Target,
		Port:      task.Port,
		Success:   false,
	}

	if out.Target == "" {
		out.Target = task.IP
	}
	if out.Port == 0 {
		out.Port = 5985
	}

	var creds WinRMCreds
	if len(task.Credentials) > 0 {
		// Try parsing as JSON string first (the new format: string containing JSON)
		var credsStr string
		if err := json.Unmarshal(task.Credentials, &credsStr); err == nil {
			// It was a string, now parse that string as JSON
			if err := json.Unmarshal([]byte(credsStr), &creds); err != nil {
				out.Error = fmt.Sprintf("Failed to parse credentials string: %v", err)
				return out
			}
		} else {
			// Not a string, try parsing as object (the old/test format: direct object)
			if err := json.Unmarshal(task.Credentials, &creds); err != nil {
				out.Error = fmt.Sprintf("Failed to parse credentials object: %v", err)
				return out
			}
		}
	}

	endpoint := winrm.NewEndpoint(out.Target, out.Port, false, true, nil, nil, nil, *timeout)
	var client *winrm.Client
	var err error

	if creds.Domain != "" {
		params := winrm.DefaultParameters
		params.TransportDecorator = func() winrm.Transporter { return &winrm.ClientNTLM{} }
		client, err = winrm.NewClientWithParameters(
			endpoint,
			fmt.Sprintf("%s\\%s", creds.Domain, creds.Username),
			creds.Password,
			params,
		)
	} else {
		client, err = winrm.NewClient(endpoint, creds.Username, creds.Password)
	}

	if err != nil {
		out.Error = fmt.Sprintf("Failed to create client: %v", err)
		return out
	}

	if *discoveryMode {
		return runDiscovery(client, out)
	}
	return runPolling(client, out)
}

func runDiscovery(client *winrm.Client, out Output) Output {
	stdout, stderr, exitCode, err := client.RunWithString("hostname", "")
	if err != nil {
		out.Error = fmt.Sprintf("WinRM error: %v", err)
		return out
	}
	if exitCode != 0 {
		out.Error = fmt.Sprintf("Command failed (%d): %s", exitCode, stderr)
		return out
	}
	out.Success = true
	out.Hostname = strings.TrimSpace(stdout)
	return out
}

const metricsScript = `
$ErrorActionPreference = 'Stop'
try {
    $cpuRaw = Get-CimInstance Win32_PerfFormattedData_PerfOS_Processor | Select-Object Name, PercentProcessorTime
    $memRaw = Get-CimInstance Win32_OperatingSystem | Select-Object TotalVisibleMemorySize, FreePhysicalMemory
    $diskRaw = Get-CimInstance Win32_LogicalDisk -Filter "DriveType=3" | Select-Object DeviceID, Size, FreeSpace
    $netRaw = Get-CimInstance Win32_PerfFormattedData_Tcpip_NetworkInterface | Select-Object Name, BytesReceivedPersec, BytesSentPersec

    # Transform CPU
    $cpu = @{}
    foreach ($c in $cpuRaw) {
        $name = if ($c.Name -eq '_Total') { 'total' } else { $c.Name }
        $cpu[$name] = $c.PercentProcessorTime
    }

    # Transform Memory (convert KB to Bytes)
    $memory = @{
        total = [double]$memRaw.TotalVisibleMemorySize * 1024
        free = [double]$memRaw.FreePhysicalMemory * 1024
        used = ([double]$memRaw.TotalVisibleMemorySize - [double]$memRaw.FreePhysicalMemory) * 1024
    }

    # Transform Disks
    $disks = @{}
    $diskTotalSize = 0
    $diskTotalFree = 0
    foreach ($d in $diskRaw) {
        $id = $d.DeviceID.Replace(':', '').ToLower()
        $disks[$id] = @{
            total = $d.Size
            free = $d.FreeSpace
        }
        $diskTotalSize += $d.Size
        $diskTotalFree += $d.FreeSpace
    }

    # Transform Network
    $network = @{}
    $netTotalRx = 0
    $netTotalTx = 0
    foreach ($n in $netRaw) {
        # Clean name for key
        $id = $n.Name -replace '[^a-zA-Z0-9]', '_' -replace '_+', '_' -replace '^_+|_+$', ''
        $id = $id.ToLower()
        $network[$id] = @{
            rx = $n.BytesReceivedPersec
            tx = $n.BytesSentPersec
        }
        $netTotalRx += $n.BytesReceivedPersec
        $netTotalTx += $n.BytesSentPersec
    }

    $data = @{
        cpu = $cpu
        memory = $memory
        disk = @{
            total = @{ size = $diskTotalSize; free = $diskTotalFree }
            drives = $disks
        }
        network = @{
            total = @{ rx = $netTotalRx; tx = $netTotalTx }
            interfaces = $network
        }
    }
    $data | ConvertTo-Json -Depth 5
} catch {
    Write-Error $_.Exception.Message
    exit 1
}
`

func runPolling(client *winrm.Client, out Output) Output {
	// Encode script to Base64 (UTF-16LE) for PowerShell -EncodedCommand
	utf16 := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
	encoded, _ := utf16.NewEncoder().String(metricsScript)
	b64 := base64.StdEncoding.EncodeToString([]byte(encoded))

	stdout, stderr, exitCode, err := client.RunWithString(fmt.Sprintf("powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand %s", b64), "")
	if err != nil {
		out.Error = fmt.Sprintf("WinRM error: %v", err)
		return out
	}
	if exitCode != 0 {
		out.Error = fmt.Sprintf("Script failed (%d): %s", exitCode, stderr)
		return out
	}

	out.Success = true
	out.Data = json.RawMessage(stdout)
	return out
}
