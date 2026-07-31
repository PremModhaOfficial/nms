package database

import (
	"strings"
	"testing"

	"nms/pkg/models"
)

func TestValidColumnsDevice(t *testing.T) {
	cols := validColumns[models.Device]()
	for _, want := range []string{"id", "hostname", "ip_address", "plugin_id", "port", "credential_profile_id", "discovery_profile_id", "polling_interval_seconds", "should_ping", "status", "created_at", "updated_at"} {
		if _, ok := cols[want]; !ok {
			t.Errorf("validColumns[Device] missing %q", want)
		}
	}
	// Non-DB and db:"-" fields must not be queryable.
	for _, banned := range []string{"credential_profile", "discovery_profile", "Payload", "Name"} {
		if _, ok := cols[banned]; ok {
			t.Errorf("validColumns[Device] unexpectedly allows %q", banned)
		}
	}
}

func TestBuildFilterClauseRejectsUnknownColumns(t *testing.T) {
	cols := validColumns[models.Device]()

	// Valid filters build a parameterized clause in deterministic count.
	conds, args, err := buildFilterClause(map[string]any{
		"ip_address": "10.0.0.1",
		"port":       5985,
	}, cols)
	if err != nil {
		t.Fatalf("valid filters rejected: %v", err)
	}
	if len(conds) != 2 || len(args) != 2 {
		t.Fatalf("got %d conditions, %d args; want 2/2", len(conds), len(args))
	}
	for i, c := range conds {
		if !strings.HasPrefix(c, "=") && !strings.Contains(c, "= $") {
			t.Errorf("condition %d not parameterized: %q", i, c)
		}
	}

	// SQL injection attempt through a map key must be rejected.
	if _, _, err := buildFilterClause(map[string]any{
		"ip_address = '1.1.1.1' OR 1=1; DROP TABLE devices; --": 1,
	}, cols); err == nil {
		t.Fatal("injection-style column name was accepted")
	}

	// Empty filters rejected.
	if _, _, err := buildFilterClause(map[string]any{}, cols); err == nil {
		t.Fatal("empty filters accepted")
	}
}
