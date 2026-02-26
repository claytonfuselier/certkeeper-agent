package enrollment

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsRetryableCollision(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"generic", fmt.Errorf("network error"), false},
		{"collision", fmt.Errorf("enrollment failed (409): fingerprint collision — retry with new key pair"), true},
		{"contains keyword", fmt.Errorf("something fingerprint collision something"), true},
		{"already enrolled", fmt.Errorf("enrollment failed (409): agent is already enrolled — do not retry"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRetryableCollision(tt.err)
			if got != tt.want {
				t.Errorf("isRetryableCollision(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestHandleEnrollError(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     interface{}
		contains string
	}{
		{
			"400",
			http.StatusBadRequest,
			ErrorResponse{Error: "invalid CSR format"},
			"invalid CSR",
		},
		{
			"401",
			http.StatusUnauthorized,
			ErrorResponse{Error: "token expired"},
			"invalid or expired enrollment token",
		},
		{
			"403",
			http.StatusForbidden,
			ErrorResponse{Error: "agent disabled"},
			"agent is disabled",
		},
		{
			"409-already-enrolled",
			http.StatusConflict,
			ErrorResponse{Error: "already enrolled", Retry: false},
			"already enrolled",
		},
		{
			"409-collision",
			http.StatusConflict,
			ErrorResponse{Error: "fingerprint exists", Retry: true},
			"fingerprint collision",
		},
		{
			"500",
			http.StatusInternalServerError,
			map[string]string{"error": "internal"},
			"500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			err := handleEnrollError(tt.status, body)
			if err == nil {
				t.Fatal("expected error")
			}
			if got := err.Error(); !containsStr(got, tt.contains) {
				t.Errorf("error = %q, want it to contain %q", got, tt.contains)
			}
		})
	}
}

func TestSendEnrollRequest_Success(t *testing.T) {
	enrollResp := EnrollResponse{
		Certificate:   "-----BEGIN CERTIFICATE-----\ncert\n-----END CERTIFICATE-----\n",
		CACertificate: "-----BEGIN CERTIFICATE-----\nca\n-----END CERTIFICATE-----\n",
		Fingerprint:   "abc123",
		ExpiresAt:     "2026-04-02 14:30:00",
		Agent: struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}{ID: 1, Name: "test-agent"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/enroll" {
			http.NotFound(w, r)
			return
		}
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Authorization") != "Bearer cke_testtoken" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(enrollResp)
	}))
	defer srv.Close()

	csrPEM := []byte("-----BEGIN CERTIFICATE REQUEST-----\ncsr\n-----END CERTIFICATE REQUEST-----\n")
	resp, err := sendEnrollRequest(srv.URL, "cke_testtoken", csrPEM)
	if err != nil {
		t.Fatalf("sendEnrollRequest() error: %v", err)
	}

	if resp.Fingerprint != "abc123" {
		t.Errorf("Fingerprint = %q, want %q", resp.Fingerprint, "abc123")
	}
	if resp.Agent.Name != "test-agent" {
		t.Errorf("Agent.Name = %q, want %q", resp.Agent.Name, "test-agent")
	}
}

func TestSendEnrollRequest_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "invalid token"})
	}))
	defer srv.Close()

	_, err := sendEnrollRequest(srv.URL, "bad_token", []byte("csr"))
	if err == nil {
		t.Error("expected error on 401")
	}
}

func TestSendEnrollRequest_409Collision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "fingerprint exists", Retry: true})
	}))
	defer srv.Close()

	_, err := sendEnrollRequest(srv.URL, "cke_token", []byte("csr"))
	if err == nil {
		t.Fatal("expected error on 409")
	}
	if !isRetryableCollision(err) {
		t.Errorf("409 with retry:true should be retryable, got: %v", err)
	}
}

func TestSendEnrollRequest_MissingCert(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(EnrollResponse{
			Certificate:   "",
			CACertificate: "",
		})
	}))
	defer srv.Close()

	_, err := sendEnrollRequest(srv.URL, "cke_token", []byte("csr"))
	if err == nil {
		t.Error("expected error when response is missing certificate data")
	}
}

func TestCheckServerTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/time" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(TimeResponse{
			ServerTime: "2026-02-15T10:30:00.000Z",
		})
	}))
	defer srv.Close()

	// Should not return an error (time check is non-fatal).
	if err := CheckServerTime(srv.URL); err != nil {
		t.Errorf("CheckServerTime() error: %v", err)
	}
}

func TestCheckServerTime_Unreachable(t *testing.T) {
	// Bad URL — should still not return an error (non-fatal).
	if err := CheckServerTime("http://localhost:99999"); err != nil {
		t.Errorf("CheckServerTime() should be non-fatal, got: %v", err)
	}
}

// containsStr is a test helper that checks if a string contains a substring.
func containsStr(s, substr string) bool {
	return strings.Contains(s, substr)
}
