package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"nms/pkg/config"

	"github.com/golang-jwt/jwt/v4"
	"golang.org/x/crypto/bcrypt"
)

// JwtAuth handles user authentication and JWT operations.
type JwtAuth struct {
	jwtSecret     []byte
	adminUsername string
	adminPassHash []byte
	expiryHours   int
	clientIP      func(*http.Request) string
}

// usernameKey is the context key for the authenticated username.
type usernameKey struct{}

// Username returns the authenticated username set by JWTMiddleware.
func Username(r *http.Request) string {
	if v, ok := r.Context().Value(usernameKey{}).(string); ok {
		return v
	}
	return ""
}

// newClientIPFunc returns a function that resolves the client IP for
// throttling. With no trusted proxies (default) the direct TCP peer is used, so
// X-Forwarded-For cannot be spoofed. When the peer is within a trusted CIDR,
// the leftmost X-Forwarded-For entry is honored.
func newClientIPFunc(trustedProxies string) func(*http.Request) string {
	var cidrs []*net.IPNet
	for _, c := range strings.Split(trustedProxies, ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ipnet, err := net.ParseCIDR(c); err == nil {
			cidrs = append(cidrs, ipnet)
		}
	}

	return func(r *http.Request) string {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return r.RemoteAddr
		}
		if len(cidrs) > 0 {
			if ip := net.ParseIP(host); ip != nil {
				for _, cidr := range cidrs {
					if cidr.Contains(ip) {
						if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
							if first := strings.TrimSpace(strings.Split(fwd, ",")[0]); first != "" {
								return first
							}
						}
					}
				}
			}
		}
		return host
	}
}

// Auth creates a new JwtAuth with the provided configuration.
// It asserts the invariants the rest of the code depends on: a non-empty
// signing secret, a bcrypt-parseable admin hash, and a sane session length.
func Auth(cfg *config.Config) *JwtAuth {
	if strings.TrimSpace(cfg.JWTSecret) == "" {
		panic("JWT_SECRET must not be empty")
	}
	if strings.TrimSpace(cfg.AdminHash) == "" {
		panic("NMS_ADMIN_HASH must not be empty")
	}
	if cfg.SessionDurationHours < 1 {
		panic("SESSION_DURATION_HOURS must be >= 1")
	}
	if _, err := bcrypt.Cost([]byte(cfg.AdminHash)); err != nil {
		panic("NMS_ADMIN_HASH is not a valid bcrypt hash")
	}

	return &JwtAuth{
		jwtSecret:     []byte(cfg.JWTSecret),
		adminUsername: cfg.AdminUser,
		adminPassHash: []byte(cfg.AdminHash),
		expiryHours:   cfg.SessionDurationHours,
		clientIP:      newClientIPFunc(cfg.TrustedProxies),
	}
}

// LoginRequest represents the login payload
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// loginThrottle tracks failed login attempts per username+IP and enforces
// exponential backoff. The map is bounded; expired entries are purged.
type loginThrottle struct {
	mu    sync.Mutex
	fails map[string]loginFailure
}

type loginFailure struct {
	count int
	until time.Time
}

const (
	maxLoginFailures = 5
	loginLockBase    = time.Minute
	maxThrottleKeys  = 4096
)

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{fails: make(map[string]loginFailure)}
}

// allowed reports whether logins for key are permitted now, and how long the
// lock lasts when they are not.
func (t *loginThrottle) allowed(key string) (wait time.Duration, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	f, exists := t.fails[key]
	if !exists {
		return 0, true
	}
	if f.until.IsZero() {
		// Failures recorded but below the lock threshold; keep counting.
		return 0, true
	}
	if time.Now().After(f.until) {
		delete(t.fails, key)
		return 0, true
	}
	return time.Until(f.until), false
}

func (t *loginThrottle) recordFailure(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.fails) >= maxThrottleKeys {
		t.purgeLocked()
	}

	f := t.fails[key]
	f.count++
	if f.count >= maxLoginFailures {
		shift := f.count - maxLoginFailures
		if shift > 6 {
			shift = 6
		}
		f.until = time.Now().Add(loginLockBase << shift)
	}
	t.fails[key] = f
}

func (t *loginThrottle) clear(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fails, key)
}

// purgeLocked drops expired entries. Called with t.mu held.
func (t *loginThrottle) purgeLocked() {
	now := time.Now()
	for k, f := range t.fails {
		if now.After(f.until) {
			delete(t.fails, k)
		}
	}
	// Hard cap: if the map is still at the limit, drop everything.
	if len(t.fails) >= maxThrottleKeys {
		t.fails = make(map[string]loginFailure)
	}
}

var loginLimiter = newLoginThrottle()

// LoginHandler handles user authentication and issues a JWT.
func (jwtAuth *JwtAuth) LoginHandler(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	key := req.Username + "|" + jwtAuth.clientIP(r)
	if wait, ok := loginLimiter.allowed(key); !ok {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(wait.Seconds())+1))
		respondError(w, http.StatusTooManyRequests, "too many failed login attempts, try again later")
		return
	}

	// Validate credentials against configured values
	if req.Username != jwtAuth.adminUsername {
		loginLimiter.recordFailure(key)
		respondError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Compare password against bcrypt hash
	if err := bcrypt.CompareHashAndPassword(jwtAuth.adminPassHash, []byte(req.Password)); err != nil {
		loginLimiter.recordFailure(key)
		respondError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	loginLimiter.clear(key)

	// Create the token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": req.Username,
		"iss":      "nms-lite",
		"exp":      time.Now().Add(time.Duration(jwtAuth.expiryHours) * time.Hour).Unix(),
		"iat":      time.Now().Unix(),
	})

	tokenString, err := token.SignedString(jwtAuth.jwtSecret)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to sign token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"token": tokenString})
}

// JWTMiddleware validates the Authorization header.
func (jwtAuth *JwtAuth) JWTMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				respondError(w, http.StatusUnauthorized, "authorization header required")
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				respondError(w, http.StatusUnauthorized, "invalid authorization header format")
				return
			}

			tokenString := parts[1]
			token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
				if token.Method != jwt.SigningMethodHS256 {
					return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
				}
				return jwtAuth.jwtSecret, nil
			}, jwt.WithValidMethods([]string{"HS256"}))

			if err != nil || !token.Valid {
				respondError(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok || claims["iss"] != "nms-lite" {
				respondError(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}

			username, ok := claims["username"].(string)
			if !ok || username == "" {
				respondError(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), usernameKey{}, username)))
		})
	}
}

// SecurityHeaders returns middleware that sets security headers.
func SecurityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			// JSON API: deny all inline script/style and object embedding.
			w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			// Only send HSTS when served over HTTPS (it is ignored by browsers over
			// plain HTTP, but sending it on a cleartext connection is harmless and
			// still cached by compliant clients that later upgrade).
			if r.TLS != nil {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}
