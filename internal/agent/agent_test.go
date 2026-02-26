package agent

import (
	"errors"
	"testing"
	"time"

	"github.com/claytonfuselier/certkeeper-agent/internal/state"
)

func TestParseTime(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"RFC3339", "2026-04-02T14:30:00Z", false},
		{"RFC3339Nano", "2026-04-02T14:30:00.123456789Z", false},
		{"space-separated", "2026-04-02 14:30:00", false},
		{"millis-Z", "2026-04-02T14:30:00.000Z", false},
		{"invalid", "not-a-date", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseTime(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseTime(%q) expected error, got %v", tt.input, result)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTime(%q) unexpected error: %v", tt.input, err)
			}
			if result.Year() != 2026 || result.Month() != 4 || result.Day() != 2 {
				t.Errorf("parseTime(%q) = %v, expected 2026-04-02", tt.input, result)
			}
		})
	}
}

func TestNextDelay(t *testing.T) {
	a := &Agent{
		state: &state.State{HeartbeatInterval: 180},
	}

	tests := []struct {
		failures int
		want     time.Duration
	}{
		{0, 180 * time.Second},     // normal interval
		{1, 5 * time.Second},       // first failure: initialBackoff
		{2, 10 * time.Second},      // 5s * 2
		{3, 20 * time.Second},      // 5s * 4
		{4, 40 * time.Second},      // 5s * 8
		{5, 80 * time.Second},      // 5s * 16
		{6, 160 * time.Second},     // 5s * 32
		{7, 5 * time.Minute},       // capped at maxBackoff
		{100, 5 * time.Minute},     // still capped
	}

	for _, tt := range tests {
		got := a.nextDelay(tt.failures)
		if got != tt.want {
			t.Errorf("nextDelay(%d) = %v, want %v", tt.failures, got, tt.want)
		}
	}
}

func TestIsFatal(t *testing.T) {
	a := &Agent{}

	tests := []struct {
		name  string
		err   error
		fatal bool
	}{
		{"nil", nil, false},
		{"unauthorized", errAgentUnauthorized, true},
		{"disabled", errAgentDisabled, true},
		{"generic error", errors.New("network timeout"), false},
		{"wrapped unauth", errors.New("some wrapper"), false}, // not same instance
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := a.isFatal(tt.err)
			if got != tt.fatal {
				t.Errorf("isFatal(%v) = %v, want %v", tt.err, got, tt.fatal)
			}
		})
	}
}
