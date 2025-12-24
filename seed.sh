#!/bin/bash

# Base URL
URL="http://localhost:8080/api/v1"

# Function to check if app is up
wait_for_server() {
  echo "Waiting for server..."
  for i in {1..10}; do
    if curl -s "$URL/credentials" > /dev/null; then
      echo "Server is up!"
      return 0
    fi
    sleep 1
  done
  echo "Server failed to start."
  exit 1
}

wait_for_server

# Data for req_win_a
REQ_ID="req_win_a"
TARGET="127.0.0.1"
PORT=15985
USER="vboxuser"
PASS="admin"

# Create Credential
echo "Creating Credential for $REQ_ID..."
CRED_PAYLOAD=$(jq -n \
                  --arg name "${REQ_ID}_creds" \
                  --arg proto "winrm" \
                  --arg user "$USER" \
                  --arg pass "$PASS" \
                  '{name: $name, description: "Imported via curl", protocol: $proto, payload: ({"username":$user,"password":$pass} | tostring)}' \
                )

CRED_RES=$(curl -s -X POST "$URL/credentials" -H "Content-Type: application/json" -d "$CRED_PAYLOAD")
echo "Response: $CRED_RES"
CRED_ID=$(echo "$CRED_RES" | jq -r '.id')

if [ "$CRED_ID" == "null" ]; then
  echo "Failed to create credential."
  exit 1
fi

echo "Credential ID: $CRED_ID"

# Create Discovery Profile
echo "Creating Discovery Profile for $REQ_ID..."
DISC_PAYLOAD=$(jq -n \
                  --arg name "${REQ_ID}_discovery" \
                  --arg target "$TARGET" \
                  --argjson port "$PORT" \
                  --argjson cred_id "$CRED_ID" \
                  '{name: $name, target: $target, port: $port, credential_profile_id: $cred_id, auto_provision: true, auto_run: true}' \
                )

DISC_RES=$(curl -s -X POST "$URL/discovery" -H "Content-Type: application/json" -d "$DISC_PAYLOAD")
echo "Response: $DISC_RES"
DISC_ID=$(echo "$DISC_RES" | jq -r '.id')

echo "Discovery Profile ID: $DISC_ID"
echo "Done."
