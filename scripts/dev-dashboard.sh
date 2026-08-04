#!/bin/bash
# ══════════════════════════════════════════════════════════════════════════════
# NMS Dev Dashboard - one-shot runner
# ══════════════════════════════════════════════════════════════════════════════
# One script that takes care of EVERYTHING needed to run the dev dashboard:
#
#   1. check  - verify deps (go, curl, fping, psql/pg_isready, python3)
#   2. db up  - ensure PostgreSQL is reachable, create the DB if missing,
#               apply schema.sql (idempotent: CREATE TABLE IF NOT EXISTS)
#   3. build  - bin/nms-server + plugins/winrm (same outputs as `make build`)
#   4. run    - start the server in the background (pidfile .dev-dashboard.pid,
#               log app.log), with dev defaults from pkg/config/config.go
#   5. wait   - poll the server until it answers (max 30s)
#   6. seed   - if the devices table is empty, run seed.py against the API
#               (seed.py needs a running server; it is NOT a standalone DB seed)
#   7. open   - print the dashboard URL and dev login
#
# Optional:
#   --smoke   - login (default admin/admin) then GET /api/v1/devices, PASS/FAIL
#   --no-seed - skip the seed-if-empty step
#   --open    - after startup, open the dashboard in the default browser
#   --stop    - kill the instance recorded in the pidfile
#
# Idempotent: re-running stops any previous instance first, and every DB step
# is safe to repeat. No interactive prompts.
# ══════════════════════════════════════════════════════════════════════════════

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$SCRIPT_DIR"

PID_FILE="$SCRIPT_DIR/.dev-dashboard.pid"
LOG_FILE="$SCRIPT_DIR/app.log"

# ── Logging ───────────────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
NC='\033[0m'

PREFIX="[dev-dashboard]"

log_info() { printf "${BLUE}%s${NC} %s\n" "$PREFIX" "$*"; }
log_ok()   { printf "${GREEN}%s${NC} %s\n" "$PREFIX" "$*"; }
log_warn() { printf "${YELLOW}%s${NC} %s\n" "$PREFIX" "$*"; }
log_err()  { printf "${RED}%s${NC} %s\n" "$PREFIX" "$*" >&2; }

die() { log_err "$*"; exit 1; }

usage() {
    cat <<'EOF'
Usage: scripts/dev-dashboard.sh [OPTIONS]

One-shot dev dashboard runner for NMS. From any cwd it resolves the repo root,
then: checks deps, ensures the DB exists and schema.sql is applied, builds the
server (bin/nms-server) and plugins (plugins/winrm), starts the server in the
background (pidfile .dev-dashboard.pid, log app.log), waits for it to respond,
seeds the DB if the devices table is empty, and prints the dashboard URL.

Options:
  --no-seed    Skip the seed-if-empty step.
  --smoke      After startup, smoke test: login (default admin/admin), then
               GET /api/v1/devices. Prints PASS/FAIL; exits non-zero on fail.
  --open       After startup, open the dashboard URL in the default browser
               (xdg-open on Linux, open on macOS).
  --stop       Kill the previously started instance (from the pidfile) and exit.
  --help, -h   Show this help and exit.

Env overrides (defaults match pkg/config/config.go):
  DB_HOST DB_PORT DB_USER DB_PASSWORD DB_NAME PLUGINS_DIR POLL_WORKER_COUNT
  POLL_INTERVAL_SEC JWT_SECRET ENCRYPTION_KEY NMS_ADMIN_USER NMS_ADMIN_HASH
  ADMIN_PASSWORD (password used by login/seed; default "admin")
  TLS_CERT_FILE TLS_KEY_FILE (when BOTH are set the server serves
               https://localhost:8443 and the script probes with -k)
  A .env file in the repo root is sourced if present (values win over defaults).

Default behavior (no flags): check -> db up -> build -> run -> wait -> seed
if empty -> print URL. Never sets APP_ENV=production (dev must not).
EOF
}

# ── Startup cleanup: only what this run started ───────────────────────────────
# If the script exits abnormally after starting the server, stop it. On a
# successful run STOP_SERVER_ON_EXIT is flipped to 0 so the server keeps
# running after the script exits. Also removes the temp dir.
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/dev-dashboard.XXXXXX")"
STOP_SERVER_ON_EXIT=1
SERVER_PID=""

cleanup() {
    local rc=$?
    rm -rf "$TMP_DIR" 2>/dev/null || true
    if [ "$STOP_SERVER_ON_EXIT" = "1" ] && [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
        log_warn "Run did not complete; stopping server started by this run (pid $SERVER_PID)."
        kill "$SERVER_PID" 2>/dev/null || true
        sleep 1
        kill -9 "$SERVER_PID" 2>/dev/null || true
        rm -f "$PID_FILE"
    fi
    exit "$rc"
}
trap cleanup EXIT

# ── Flags ─────────────────────────────────────────────────────────────────────
NO_SEED=0
SMOKE=0
DO_OPEN=0
DO_STOP=0
DO_HELP=0

while [ $# -gt 0 ]; do
    case "$1" in
        --no-seed) NO_SEED=1 ;;
        --smoke)   SMOKE=1 ;;
        --open)    DO_OPEN=1 ;;
        --stop)    DO_STOP=1 ;;
        --help|-h) DO_HELP=1 ;;
        *) die "Unknown option: $1. Run '$0 --help' for usage." ;;
    esac
    shift
done

if [ "$DO_HELP" = "1" ]; then
    usage
    exit 0
fi

# ── Dependencies ──────────────────────────────────────────────────────────────
cmd_check() {
    log_info "Checking dependencies..."
    local missing=() cmd
    for cmd in go curl fping; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            missing+=("$cmd")
        fi
    done
    # psql OR pg_isready is acceptable; psql is needed for schema/seed steps.
    if ! command -v psql >/dev/null 2>&1 && ! command -v pg_isready >/dev/null 2>&1; then
        missing+=("psql or pg_isready")
    fi
    if [ "${#missing[@]}" -gt 0 ]; then
        die "Missing dependencies: ${missing[*]}. Install them and retry."
    fi
    if ! command -v python3 >/dev/null 2>&1; then
        log_warn "python3 not found; the seed step will be skipped (build/run still work)."
    fi
    log_ok "Dependencies OK."
}

# ── Environment ───────────────────────────────────────────────────────────────
# Source .env first so its values win over the dev defaults below.
load_env() {
    if [ -f "$SCRIPT_DIR/.env" ]; then
        log_info "Sourcing .env (values override script defaults)..."
        # shellcheck disable=SC1091
        set -a; . "$SCRIPT_DIR/.env"; set +a
    fi
}

set_defaults() {
    export DB_HOST="${DB_HOST:-localhost}"
    export DB_PORT="${DB_PORT:-5432}"
    export DB_USER="${DB_USER:-nmslite}"
    export DB_PASSWORD="${DB_PASSWORD:-nmslite}"
    export DB_NAME="${DB_NAME:-nmslite}"
    export PLUGINS_DIR="${PLUGINS_DIR:-plugins}"
    export POLL_WORKER_COUNT="${POLL_WORKER_COUNT:-5}"
    export POLL_INTERVAL_SEC="${POLL_INTERVAL_SEC:-30}"
    export JWT_SECRET="${JWT_SECRET:-default-insecure-secret-change-me}"
    export ENCRYPTION_KEY="${ENCRYPTION_KEY:-1234567890123456789012345678901212345678901234567890123456789012}"
    export NMS_ADMIN_USER="${NMS_ADMIN_USER:-admin}"
    # Default bcrypt hash of "admin" (matches config.go and start.sh).
    export NMS_ADMIN_HASH="${NMS_ADMIN_HASH:-\$2a\$10\$BST/uOdLLXUyqO4fN.b9cuwVwoXEJWWFzpc4iirHiu3GcgbuJqtdu}"
    # Dev must never run as production: production refuses non-TLS. Default to
    # dev only when APP_ENV is unset; an explicit production value is honored.
    export APP_ENV="${APP_ENV:-dev}"
    export PGPASSWORD="$DB_PASSWORD"

    if [ "$APP_ENV" = "production" ]; then
        log_warn "APP_ENV=production is set: the server requires TLS and will refuse plain HTTP."
        log_warn "For the dev dashboard, unset APP_ENV (or leave it empty) so it defaults to dev."
    fi
}

# TLS detection: server binds :8443 with TLS only when BOTH cert and key are set.
detect_tls() {
    TLS_MODE=0
    if [ -n "${TLS_CERT_FILE:-}" ] && [ -n "${TLS_KEY_FILE:-}" ]; then
        TLS_MODE=1
    fi
    if [ "$TLS_MODE" = "1" ]; then
        SCHEME="https"
        PORT=8443
    else
        SCHEME="http"
        PORT=8080
    fi
    BASE_URL="$SCHEME://localhost:$PORT"
    CURL_OPTS=(-s)
    if [ "$TLS_MODE" = "1" ]; then
        CURL_OPTS+=(-k)   # dev certs are self-signed
    fi
}

# ── DB up ─────────────────────────────────────────────────────────────────────
cmd_db_up() {
    log_info "Checking PostgreSQL reachability at $DB_HOST:$DB_PORT..."
    if command -v pg_isready >/dev/null 2>&1; then
        pg_isready -h "$DB_HOST" -p "$DB_PORT" -t 5 >/dev/null 2>&1 \
            || die "PostgreSQL is not reachable at $DB_HOST:$DB_PORT. Start it and retry."
    else
        psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d postgres -tAc "SELECT 1" >/dev/null 2>&1 \
            || die "PostgreSQL is not reachable at $DB_HOST:$DB_PORT. Start it and retry."
    fi
    log_ok "PostgreSQL reachable."

    # Ensure the database exists (idempotent).
    if psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d postgres \
            -tAc "SELECT 1 FROM pg_database WHERE datname = '$DB_NAME'" | grep -qx 1; then
        log_ok "Database '$DB_NAME' already exists."
    else
        log_info "Database '$DB_NAME' missing; creating..."
        psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d postgres -v ON_ERROR_STOP=1 \
            -c "CREATE DATABASE \"$DB_NAME\"" >/dev/null \
            || die "Failed to create database '$DB_NAME'."
        log_ok "Database '$DB_NAME' created."
    fi

    log_info "Applying schema.sql (idempotent, safe to re-run)..."
    psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1 -q \
        -f "$SCRIPT_DIR/schema.sql" \
        || die "Failed to apply schema.sql."
    log_ok "Schema applied."
}

# ── Build ─────────────────────────────────────────────────────────────────────
cmd_build() {
    log_info "Building server -> bin/nms-server ..."
    mkdir -p "$SCRIPT_DIR/bin"
    (cd "$SCRIPT_DIR" && go build -o bin/nms-server ./cmd/app) \
        || die "Failed to build server (bin/nms-server)."

    log_info "Building winrm plugin -> plugins/winrm ..."
    mkdir -p "$SCRIPT_DIR/plugins"
    (cd "$SCRIPT_DIR/plugin-code/winrm" && go build -o ../../plugins/winrm main.go) \
        || die "Failed to build winrm plugin (plugins/winrm)."

    log_ok "Build complete."
}

# ── Stop (previous instance) ──────────────────────────────────────────────────
cmd_stop() {
    local stopped=0 pid
    if [ -f "$PID_FILE" ]; then
        pid="$(cat "$PID_FILE" 2>/dev/null || true)"
        if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
            log_info "Stopping NMS server (pid $pid)..."
            kill "$pid" 2>/dev/null || true
            # Bounded wait for graceful shutdown (max ~5s), then SIGKILL.
            local i
            for i in $(seq 1 10); do
                kill -0 "$pid" 2>/dev/null || { stopped=1; break; }
                sleep 0.5
            done
            if [ "$stopped" != "1" ]; then
                log_warn "Server did not exit gracefully; sending SIGKILL."
                kill -9 "$pid" 2>/dev/null || true
            fi
        else
            log_warn "Pidfile $PID_FILE is stale (process not running); removing."
        fi
        rm -f "$PID_FILE"
        stopped=1
    fi

    # Fallback: kill any leftover nms-server started from bin/ (e.g. no pidfile).
    local leftover
    leftover="$(pgrep -f 'bin/nms-server' 2>/dev/null || true)"
    if [ -n "$leftover" ]; then
        log_warn "Killing leftover nms-server process(es): $leftover"
        pkill -f 'bin/nms-server' 2>/dev/null || true
        stopped=1
    fi

    if [ "$stopped" = "1" ]; then
        log_ok "Stopped."
    else
        log_info "No running NMS server found; nothing to stop."
    fi
}

# ── Run ───────────────────────────────────────────────────────────────────────
cmd_run() {
    # Idempotency: stop whatever was running before, then start fresh.
    cmd_stop >/dev/null 2>&1 || true

    if [ ! -x "$SCRIPT_DIR/bin/nms-server" ]; then
        die "bin/nms-server missing; run the build step first (or use the full default run)."
    fi

    log_info "Starting NMS server in background (pidfile: $PID_FILE, log: $LOG_FILE)..."
    nohup "$SCRIPT_DIR/bin/nms-server" >> "$LOG_FILE" 2>&1 &
    SERVER_PID=$!
    printf '%s\n' "$SERVER_PID" > "$PID_FILE"

    # Early liveness check so a crash at boot fails fast with the log tail.
    sleep 1
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
        rm -f "$PID_FILE"
        log_err "Server exited immediately after start. Last log lines:"
        tail -n 20 "$LOG_FILE" 2>/dev/null | sed 's/^/    /' || true
        exit 1
    fi
    log_ok "Server started (pid $SERVER_PID)."
}

# ── Wait ──────────────────────────────────────────────────────────────────────
cmd_wait() {
    log_info "Waiting for server at $BASE_URL/ (max ~30s)..."
    local attempts=30 i code
    for i in $(seq 1 "$attempts"); do
        code="$(curl "${CURL_OPTS[@]}" -o /dev/null -w '%{http_code}' --max-time 1 "$BASE_URL/" 2>/dev/null || true)"
        # GET / has no handler (404 by design): ANY HTTP response means the
        # router is live, which only happens after DB + fping + services booted.
        if [ -n "$code" ] && [ "$code" != "000" ]; then
            log_ok "Server is up (HTTP $code on $BASE_URL/)."
            return 0
        fi
        sleep 1
    done
    log_err "Server did not respond within ${attempts}s. Last log lines:"
    tail -n 20 "$LOG_FILE" 2>/dev/null | sed 's/^/    /' || true
    exit 1
}

# ── Seed ──────────────────────────────────────────────────────────────────────
cmd_seed() {
    if [ "$NO_SEED" = "1" ]; then
        log_info "--no-seed: skipping seed step."
        return 0
    fi
    if ! command -v python3 >/dev/null 2>&1; then
        log_warn "python3 not found; skipping seed."
        return 0
    fi
    if ! python3 -c 'import requests' >/dev/null 2>&1; then
        log_warn "python3 'requests' module not found; skipping seed (pip install requests)."
        return 0
    fi
    if [ "$TLS_MODE" = "1" ]; then
        log_warn "seed.py hardcodes http://localhost:8080, so seeding is skipped under TLS."
        return 0
    fi

    local count
    count="$(psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" \
        -tAc "SELECT COUNT(*) FROM devices" | tr -d '[:space:]')" \
        || die "Failed to query device count."

    case "$count" in
        ''|*[!0-9]*) die "Unexpected device count from database: '$count'." ;;
    esac

    if [ "$count" -eq 0 ]; then
        log_info "devices table is empty; seeding via seed.py (needs the running server)..."
        (cd "$SCRIPT_DIR" && python3 seed.py) \
            || die "Seed failed (seed.py). The server is still running; check app.log."
        log_ok "Seed complete."
    else
        log_ok "devices table already has $count row(s); skipping seed."
    fi
}

# ── Smoke test ────────────────────────────────────────────────────────────────
cmd_smoke() {
    log_info "Smoke test: login + GET /api/v1/devices ..."
    local admin_user="${NMS_ADMIN_USER:-admin}"
    local admin_pass="${ADMIN_PASSWORD:-admin}"
    local login_body="$TMP_DIR/login.json"
    local login_resp="$TMP_DIR/login_resp.json"
    local devices_resp="$TMP_DIR/devices.json"
    local code token dev_code

    printf '{"username":"%s","password":"%s"}' "$admin_user" "$admin_pass" > "$login_body"

    code="$(curl "${CURL_OPTS[@]}" -o "$login_resp" -w '%{http_code}' --max-time 10 \
        -X POST -H 'Content-Type: application/json' --data-binary @"$login_body" \
        "$BASE_URL/login" 2>/dev/null || true)"
    if [ "$code" != "200" ]; then
        log_err "Smoke FAIL: login returned HTTP $code (expected 200)."
        log_err "Response: $(tr -d '\n' < "$login_resp" 2>/dev/null | head -c 300 || true)"
        log_err "Tip: with a custom NMS_ADMIN_HASH, set ADMIN_PASSWORD to the matching password."
        return 1
    fi

    token="$(sed -n 's/.*"token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$login_resp" | head -n1)"
    if [ -z "$token" ]; then
        log_err "Smoke FAIL: login returned 200 but no token was found in the response."
        return 1
    fi
    log_ok "Login OK (HTTP 200, JWT obtained)."

    dev_code="$(curl "${CURL_OPTS[@]}" -o "$devices_resp" -w '%{http_code}' --max-time 10 \
        -H "Authorization: Bearer $token" "$BASE_URL/api/v1/devices" 2>/dev/null || true)"
    log_ok "GET /api/v1/devices -> HTTP $dev_code (expected 200)."

    if [ "$dev_code" = "200" ]; then
        log_ok "SMOKE PASS"
        return 0
    fi
    log_err "Smoke FAIL: devices endpoint returned HTTP $dev_code (expected 200)."
    return 1
}

# ── Open / URL ────────────────────────────────────────────────────────────────
cmd_open() {
    local pass="${ADMIN_PASSWORD:-admin}"
    echo ""
    printf "${GREEN}${BOLD}  >>> Dashboard: %s/  (login: %s / %s unless overridden)${NC}\n" \
        "$BASE_URL" "$NMS_ADMIN_USER" "$pass"
    log_ok "Logs: $LOG_FILE   |   Stop with: $0 --stop"
    echo ""
    if [ "$DO_OPEN" = "1" ]; then
        if command -v xdg-open >/dev/null 2>&1; then
            log_info "Opening dashboard in browser (xdg-open)..."
            xdg-open "$BASE_URL/" >/dev/null 2>&1 || log_warn "Could not auto-open the browser."
        elif command -v open >/dev/null 2>&1; then
            log_info "Opening dashboard in browser (open)..."
            open "$BASE_URL/" >/dev/null 2>&1 || log_warn "Could not auto-open the browser."
        else
            log_warn "No xdg-open/open found; open $BASE_URL/ manually."
        fi
    fi
}

# ══════════════════════════════════════════════════════════════════════════════
# Entry point
# ══════════════════════════════════════════════════════════════════════════════
if [ "$DO_STOP" = "1" ]; then
    cmd_stop
    exit 0
fi

cmd_check
load_env
set_defaults
detect_tls

log_info "Starting full dev dashboard run for $SCRIPT_DIR"
cmd_db_up
cmd_build
cmd_run
cmd_wait

# Server is up: from here on, even a failure leaves it running for inspection.
STOP_SERVER_ON_EXIT=0

cmd_seed
if [ "$SMOKE" = "1" ]; then
    if ! cmd_smoke; then
        log_err "Smoke test FAILED."
        exit 1
    fi
fi
cmd_open

exit 0
