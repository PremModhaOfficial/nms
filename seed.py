import json
import requests
import time
import sys
import os

BASE_URL = "http://localhost:8080"
# Use environment variables or defaults
ADMIN_USER = os.getenv("NMS_ADMIN_USER", "admin")
ADMIN_PASS = os.getenv("ADMIN_PASSWORD", "admin")

# Wait for server to start
print(f"Waiting for server {BASE_URL} to start...")
for i in range(10):
    try:
        requests.get(f"{BASE_URL}/login") # Check if reachable
        print("Server is reachable!")
        break
    except requests.exceptions.ConnectionError:
        time.sleep(1)
else:
    print("Server failed to start or is unreachable.")
    sys.exit(1)

# 1. Login to get JWT
print(f"Logging in as {ADMIN_USER}...")
login_res = requests.post(f"{BASE_URL}/login", json={
    "username": ADMIN_USER,
    "password": ADMIN_PASS
})

if login_res.status_code != 200:
    print(f"Login failed ({login_res.status_code}): {login_res.text}")
    sys.exit(1)

token = login_res.json()['token']
headers = {"Authorization": f"Bearer {token}"}
print("Login successful.")

# Use poll_input_win_a.json if it exists, otherwise fallback
seed_file = 'poll_input_win_a.json' if os.path.exists('poll_input_win_a.json') else 'poll_input.json'
print(f"Reading seed data from {seed_file}...")

with open(seed_file, 'r') as f:
    data = json.load(f)

for item in data:
    # 2. Create Credential Profile
    cred_payload = {
        "name": item.get('request_id', 'imported') + "_creds",
        "protocol": "winrm",
        "payload": json.dumps(item['credentials'])
    }
    
    print(f"Creating credential for {item.get('request_id', 'target')}...")
    res = requests.post(f"{BASE_URL}/api/v1/credentials", json=cred_payload, headers=headers)
    if res.status_code not in [201, 200]:
        print(f"Failed to create credential: {res.text}")
        continue
    
    cred_id = res.json()['id']
    print(f"Credential created with ID: {cred_id}")

    # 3. Create Discovery Profile
    disc_payload = {
        "name": item.get('request_id', 'imported') + "_discovery",
        "target": item['target'],
        "port": item['port'],
        "credential_profile_id": cred_id,
        "auto_provision": True,
        "auto_run": True
    }

    print(f"Creating discovery profile for {item.get('request_id', 'target')}...")
    res = requests.post(f"{BASE_URL}/api/v1/discovery_profiles", json=disc_payload, headers=headers)
    if res.status_code not in [201, 200]:
        print(f"Failed to create discovery profile: {res.text}")
        continue
    
    disc_id = res.json()['id']
    print(f"Discovery profile created with ID: {disc_id}")

print("Done processing.")
