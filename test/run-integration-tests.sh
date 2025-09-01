#!/bin/bash

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}RSS Reader Integration Tests${NC}"
echo "================================"

# Check if Caddy is installed
if ! command -v caddy &> /dev/null; then
    echo -e "${RED}Error: Caddy is not installed${NC}"
    echo "Please install Caddy first:"
    echo "  macOS: brew install caddy"
    echo "  Linux: See https://caddyserver.com/docs/install"
    exit 1
fi

# Start Caddy in the background
echo -e "${YELLOW}Starting Caddy test server...${NC}"
cd fixtures
caddy run --config Caddyfile &
CADDY_PID=$!
cd ..

# Wait for Caddy to start
sleep 2

# Check if Caddy started successfully
if ! kill -0 $CADDY_PID 2>/dev/null; then
    echo -e "${RED}Failed to start Caddy${NC}"
    exit 1
fi

echo -e "${GREEN}Caddy started (PID: $CADDY_PID)${NC}"

# Run integration tests
echo -e "${YELLOW}Running integration tests...${NC}"
cd integration
go test -v -timeout 30s
TEST_RESULT=$?
cd ..

# Stop Caddy
echo -e "${YELLOW}Stopping Caddy...${NC}"
kill $CADDY_PID 2>/dev/null
wait $CADDY_PID 2>/dev/null

# Report results
if [ $TEST_RESULT -eq 0 ]; then
    echo -e "${GREEN}✓ All integration tests passed!${NC}"
else
    echo -e "${RED}✗ Some integration tests failed${NC}"
    exit $TEST_RESULT
fi