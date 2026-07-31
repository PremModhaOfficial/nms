package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nms/pkg/config"
	"nms/pkg/models"

	"github.com/golang-jwt/jwt/v4"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
		msg  string
	}{
		{"nil", nil, http.StatusOK, ""},
		{"not found rows", sql.ErrNoRows, http.StatusNotFound, "record not found"},
		{"not found message", errors.New("record not found"), http.StatusNotFound, "record not found"},
		{"duplicate key", errors.New("duplicate key value violates unique constraint"), http.StatusConflict, "record already exists"},
		{"unique constraint", errors.New("ERROR: duplicate key ... unique constraint \"devices_ip_port_key\""), http.StatusConflict, "record already exists"},
		{"device validation", errors.New("credential_profile_id is required and must be >= 1"), http.StatusBadRequest, "credential_profile_id is required and must be >= 1"},
		{"immutable", errors.New("credential_profile_id and discovery_profile_id are immutable after creation"), http.StatusBadRequest, "credential_profile_id and discovery_profile_id are immutable after creation"},
		{"name validation", errors.New("name cannot be empty or whitespace-only"), http.StatusBadRequest, "name cannot be empty or whitespace-only"},
		{"port validation", errors.New("port must be between 1 and 65535"), http.StatusBadRequest, "port must be between 1 and 65535"},
		{"unknown DB error", errors.New("connection refused"), http.StatusInternalServerError, "internal server error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, msg := classifyError(tt.err)
			if code != tt.code || msg != tt.msg {
				t.Fatalf("classifyError(%v) = (%d, %q), want (%d, %q)", tt.err, code, msg, tt.code, tt.msg)
			}
		})
	}
}

func TestDoRequestTimesOutWhenServiceNeverReplies(t *testing.T) {
	old := rpcTimeout
	rpcTimeout = 50 * time.Millisecond
	defer func() { rpcTimeout = old }()

	// A service that never drains the request channel and never replies.
	reqCh := make(chan models.Request, 1)
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	_, err := doRequest(r, reqCh, models.Request{ReplyCh: make(chan models.Response, 1)})
	if !errors.Is(err, errServiceUnavailable) {
		t.Fatalf("doRequest error = %v, want errServiceUnavailable", err)
	}
}

func TestDoRequestReturnsReply(t *testing.T) {
	old := rpcTimeout
	rpcTimeout = time.Second
	defer func() { rpcTimeout = old }()

	reqCh := make(chan models.Request, 1)
	go func() {
		req := <-reqCh
		req.ReplyCh <- models.Response{Data: "ok"}
	}()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	resp, err := doRequest(r, reqCh, models.Request{ReplyCh: make(chan models.Response, 1)})
	if err != nil {
		t.Fatalf("doRequest error = %v, want nil", err)
	}
	if resp.Data != "ok" {
		t.Fatalf("doRequest Data = %v, want %q", resp.Data, "ok")
	}
}

func TestLoginThrottle(t *testing.T) {
	key := "admin|127.0.0.1"
	lt := newLoginThrottle()

	if _, ok := lt.allowed(key); !ok {
		t.Fatal("fresh key should be allowed")
	}
	for i := 0; i < maxLoginFailures; i++ {
		lt.recordFailure(key)
	}
	if _, ok := lt.allowed(key); ok {
		t.Fatal("key should be locked after maxLoginFailures")
	}
	lt.clear(key)
	if _, ok := lt.allowed(key); !ok {
		t.Fatal("cleared key should be allowed")
	}
}

func TestLoginHandlerThrottlesAfterFailures(t *testing.T) {
	cfg := &config.Config{
		JWTSecret:            "test-secret-that-is-long-enough-for-hs256-signing",
		AdminUser:            "admin",
		AdminHash:            "$2a$10$BST/uOdLLXUyqO4fN.b9cuwVwoXEJWWFzpc4iirHiu3GcgbuJqtdu", // hash of "admin"
		SessionDurationHours: 1,
	}
	auth := Auth(cfg)

	body := `{"username":"admin","password":"wrong"}`
	for i := 0; i < maxLoginFailures+1; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		auth.LoginHandler(w, req)
		last := w.Code
		if i < maxLoginFailures && last != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i, last)
		}
		if i == maxLoginFailures && last != http.StatusTooManyRequests {
			t.Fatalf("attempt %d: status = %d, want 429", i, last)
		}
	}
}

func TestJWTMiddleware(t *testing.T) {
	cfg := &config.Config{
		JWTSecret:            "test-secret-that-is-long-enough-for-hs256-signing",
		AdminUser:            "admin",
		AdminHash:            "$2a$10$BST/uOdLLXUyqO4fN.b9cuwVwoXEJWWFzpc4iirHiu3GcgbuJqtdu",
		SessionDurationHours: 1,
	}
	auth := Auth(cfg)

	valid := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": "admin",
		"iss":      "nms-lite",
		"exp":      time.Now().Add(time.Hour).Unix(),
		"iat":      time.Now().Unix(),
	})
	validStr, err := valid.SignedString([]byte(cfg.JWTSecret))
	if err != nil {
		t.Fatalf("sign valid token: %v", err)
	}

	wrongAlg := jwt.NewWithClaims(jwt.SigningMethodHS384, jwt.MapClaims{
		"username": "admin",
		"iss":      "nms-lite",
		"exp":      time.Now().Add(time.Hour).Unix(),
	})
	wrongAlgStr, err := wrongAlg.SignedString([]byte(cfg.JWTSecret))
	if err != nil {
		t.Fatalf("sign wrong-alg token: %v", err)
	}

	wrongIssuer := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": "admin",
		"iss":      "evil",
		"exp":      time.Now().Add(time.Hour).Unix(),
	})
	wrongIssuerStr, err := wrongIssuer.SignedString([]byte(cfg.JWTSecret))
	if err != nil {
		t.Fatalf("sign wrong-issuer token: %v", err)
	}

	var passed bool
	handler := auth.JWTMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		passed = true
		if got := Username(r); got != "admin" {
			t.Fatalf("username claim = %q, want %q", got, "admin")
		}
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name string
		tok  string
		code int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"bad header", "Basic abc", http.StatusUnauthorized},
		{"valid", "Bearer " + validStr, http.StatusOK},
		{"wrong alg", "Bearer " + wrongAlgStr, http.StatusUnauthorized},
		{"wrong issuer", "Bearer " + wrongIssuerStr, http.StatusUnauthorized},
		{"garbage", "Bearer not.a.token", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			passed = false
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
			if tt.tok != "" {
				req.Header.Set("Authorization", tt.tok)
			}
			handler.ServeHTTP(w, req)
			if w.Code != tt.code {
				t.Fatalf("status = %d, want %d", w.Code, tt.code)
			}
			if tt.code == http.StatusOK && !passed {
				t.Fatal("next handler not called for valid token")
			}
		})
	}
}

func TestAuthPanicsOnBadConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
	}{
		{"empty secret", &config.Config{AdminHash: "$2a$10$BST/uOdLLXUyqO4fN.b9cuwVwoXEJWWFzpc4iirHiu3GcgbuJqtdu", SessionDurationHours: 1}},
		{"empty hash", &config.Config{JWTSecret: "secret", SessionDurationHours: 1}},
		{"bad hash", &config.Config{JWTSecret: "secret", AdminHash: "not-a-bcrypt-hash", SessionDurationHours: 1}},
		{"zero expiry", &config.Config{JWTSecret: "secret", AdminHash: "$2a$10$BST/uOdLLXUyqO4fN.b9cuwVwoXEJWWFzpc4iirHiu3GcgbuJqtdu", SessionDurationHours: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatal("Auth() did not panic on invalid config")
				}
			}()
			Auth(tt.cfg)
		})
	}
}

func TestMaxBodyBytesMiddleware(t *testing.T) {
	handler := MaxBodyBytes(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, m)
	}))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`{"data":"this payload is longer than ten bytes"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized body status = %d, want 400", w.Code)
	}
}

// fakeEntityService drains request channel and replies as configured, letting
// handlers complete without a real service running.
func fakeEntityService(t *testing.T, reqCh <-chan models.Request, fn func(models.Request) models.Response) {
	t.Helper()
	go func() {
		for req := range reqCh {
			req.ReplyCh <- fn(req)
		}
	}()
}

func TestRunDiscoveryHandlerValidatesProfile(t *testing.T) {
	old := rpcTimeout
	rpcTimeout = time.Second
	defer func() { rpcTimeout = old }()

	eventChan := make(chan models.Event, 1)
	crudReqCh := make(chan models.Request, 1)

	fakeEntityService(t, crudReqCh, func(req models.Request) models.Response {
		if req.Operation == models.OpGet && req.EntityType == "DiscoveryProfile" {
			if req.ID == 42 {
				return models.Response{Data: &models.DiscoveryProfile{ID: 42}}
			}
			return models.Response{Error: sql.ErrNoRows}
		}
		return models.Response{}
	})

	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/discovery_profiles/{id}/run", RunDiscoveryHandler(eventChan, crudReqCh))

	// Unknown profile -> 404, no event published.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/discovery_profiles/99/run", nil)
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing profile status = %d, want 404", w.Code)
	}
	select {
	case e := <-eventChan:
		t.Fatalf("unexpected event published for missing profile: %+v", e)
	default:
	}

	// Known profile -> 202 and event queued.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/discovery_profiles/42/run", nil)
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("known profile status = %d, want 202", w.Code)
	}
	select {
	case e := <-eventChan:
		if e.Type != models.EventTriggerDiscovery {
			t.Fatalf("event type = %v, want EventTriggerDiscovery", e.Type)
		}
	default:
		t.Fatal("expected discovery trigger event")
	}
}
