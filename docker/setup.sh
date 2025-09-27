#!/bin/sh
set -e

docker-compose -p garage up -d
sleep 2

GARAGE_ZONE="foo"
GARAGE_CAPACITY="1G"

echo "[+] Checking if layout needs to be bootstrapped..."
NEEDS_BOOTSTRAP=$(docker exec garage /garage status | grep "NO ROLE ASSIGNED" || true)

if [ -z "$NEEDS_BOOTSTRAP" ]; then
  echo "[✓] Layout already applied. Skipping bootstrap."
  exit 0
fi

echo "[+] Fetching Garage node ID..."
NODE_ID=$(docker exec garage /garage status | awk '/HEALTHY NODES/ {getline; getline; print $1}')
echo "[+] Found node ID: $NODE_ID"

echo "[+] Assigning layout (zone: $GARAGE_ZONE, capacity: $GARAGE_CAPACITY)..."
docker exec garage /garage layout assign -z "$GARAGE_ZONE" -c "$GARAGE_CAPACITY" "$NODE_ID"

echo "[+] Applying layout..."
docker exec garage /garage layout apply --version 1

echo "[✓] Layout applied successfully"
