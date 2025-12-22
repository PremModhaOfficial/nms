import json
import requests
import time
import sys

# Wait for server to start
print("Waiting for server to start...")
for i in range(10):
    try:
        requests.get("http://localhost:8080/api/v1/credentials")
        print("Server is up!")
        break
    except requests.exceptions.ConnectionError:
        time.sleep(1)
else:
    print("Server failed to start.")
    sys.exit(1)

with open('poll_input.json', 'r') as f:
    data = json.load(f)

for item in data:
    # 1. Create Credential Profile
    cred_payload = {
        "name": item['request_id'] + "_creds",
        "description": "Imported from poll_input.json",
        "protocol": "winrm",
        "payload": json.dumps(item['credentials'])
    }
    
    print(f"Creating credential for {item['request_id']}...")
    res = requests.post("http://localhost:8080/api/v1/credentials", json=cred_payload)
    if res.status_code != 201:
        print(f"Failed to create credential: {res.text}")
        continue
    
    cred_id = res.json()['id']
    print(f"Credential created with ID: {cred_id}")

    # 2. Create Discovery Profile
    disc_payload = {
        "name": item['request_id'] + "_discovery",
        "target": item['target'],
        "port": item['port'],
        "credential_profile_id": cred_id,
        "auto_provision": True,
        "auto_run": True
    }

    print(f"Creating discovery profile for {item['request_id']}...")
    res = requests.post("http://localhost:8080/api/v1/discovery", json=disc_payload)
    if res.status_code != 201:
        print(f"Failed to create discovery profile: {res.text}")
        continue
    
    disc_id = res.json()['id']
    print(f"Discovery profile created with ID: {disc_id}")

print("Done processing.")
