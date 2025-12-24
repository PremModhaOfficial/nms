#!/bin/bash
# ══════════════════════════════════════════════════════════════════════════════
# NMS Server Startup Script
# ══════════════════════════════════════════════════════════════════════════════
# This script compiles the app, sets up secure environment variables,
# and starts the NMS app.
#
# Usage:
#   ./start.sh                       # Uses default dev credentials (admin/admin)
#   ADMIN_PASSWORD=xyz ./start.sh    # Sets a custom password
# ══════════════════════════════════════════════════════════════════════════════

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}                    NMS Server Startup                         ${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"

# ══════════════════════════════════════════════════════════════════════════════
# Create bin directory
# ══════════════════════════════════════════════════════════════════════════════
mkdir -p bin

# ══════════════════════════════════════════════════════════════════════════════
# Compile the app and plugins
# ══════════════════════════════════════════════════════════════════════════════
echo -e "${YELLOW}[1/3] Compiling server and plugins...${NC}"
go build -o bin/nms-app cmd/app/main.go
mkdir -p plugins
(cd plugin-code/winrm && go build -o ../../plugins/winrm main.go)
echo -e "${GREEN}[✓] Compiled successfully${NC}"

# ══════════════════════════════════════════════════════════════════════════════
# Set up environment variables
# ══════════════════════════════════════════════════════════════════════════════
echo -e "${YELLOW}[2/3] Setting up environment...${NC}"

# Admin username
export NMS_ADMIN_USER="${NMS_ADMIN_USER:-admin}"

# Password handling
if [ -n "$ADMIN_PASSWORD" ]; then
    # Generate bcrypt hash for custom password
    echo -e "${YELLOW}    Generating password hash for custom password...${NC}"
    export NMS_ADMIN_HASH=$(go run "$SCRIPT_DIR/scripts/hashpassword.go" "$ADMIN_PASSWORD")
    echo -e "${GREEN}[✓] Password hash generated${NC}"
elif [ -z "$NMS_ADMIN_HASH" ]; then
    # Default: hash of "admin"
    export NMS_ADMIN_HASH='$2a$10$BST/uOdLLXUyqO4fN.b9cuwVwoXEJWWFzpc4iirHiu3GcgbuJqtdu'
    echo -e "${YELLOW}    Using default password (admin/admin)${NC}"
fi

# JWT secret
export JWT_SECRET="${JWT_SECRET:-$(openssl rand -hex 32 2>/dev/null || echo 'dev-jwt-secret-change-in-production')}"

# Encryption key for credentials
export ENCRYPTION_KEY="${ENCRYPTION_KEY:-1234567890123456789012345678901212345678901234567890123456789012}"

echo -e "${GREEN}[✓] Environment configured${NC}"
echo -e "${YELLOW}    Admin user: $NMS_ADMIN_USER${NC}"

# ══════════════════════════════════════════════════════════════════════════════
# Start the app
# ══════════════════════════════════════════════════════════════════════════════
echo -e "${YELLOW}[3/3] Starting server...${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo ""

exec bin/nms-app
