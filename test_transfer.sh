#!/bin/bash
set -e

# Cleanup function
cleanup() {
    echo "Cleaning up..."
    kill $SERVER_PID 2>/dev/null || true
    rm -rf test_data received
}
trap cleanup EXIT

# Build
echo "Building helios..."
go build -o helios ./cmd/helios

# Create test data
echo "Creating test data..."
mkdir -p test_data
dd if=/dev/urandom of=test_data/large_file.bin bs=1M count=20 status=none
# Create a small file too
echo "Hello Helios!" > test_data/small_file.txt

# Start server
echo "Starting server..."
./helios serve --output ./received --no-tui &
SERVER_PID=$!
sleep 2 # Give server time to start

# Run client
echo "Sending files..."
./helios send test_data --to localhost:4433 --no-tui

# Verify
echo "Verifying transfer..."
sleep 5
ls -lR received
echo "Checksums (Source):"
find test_data -type f -exec shasum {} \; | sort
echo "Checksums (Received):"
find received -type f -exec shasum {} \; | sort

if diff -r test_data received; then
    echo "✅ SUCCESS: Files match perfectly!"
else
    echo "❌ FAILURE: Files differ!"
    exit 1
fi
