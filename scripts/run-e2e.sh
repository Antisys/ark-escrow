#!/bin/bash
set -e

# Run E2E tests against a Nigiri regtest environment.
# Prerequisites: nigiri (https://github.com/vulpemventures/nigiri)
#
# Usage:
#   nigiri start --liquid   # start the regtest environment
#   ./scripts/run-e2e.sh    # run the escrow E2E tests

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

# Check nigiri is running
if ! docker ps --format '{{.Names}}' | grep -q '^lnd$'; then
  echo -e "${RED}Error: LND container not running. Start nigiri first:${NC}"
  echo "  nigiri start --liquid"
  exit 1
fi

if ! docker ps --format '{{.Names}}' | grep -q '^cln$'; then
  echo -e "${RED}Error: CLN container not running. Start nigiri first:${NC}"
  echo "  nigiri start --liquid"
  exit 1
fi

if ! docker ps --format '{{.Names}}' | grep -q '^liquid$'; then
  echo -e "${RED}Error: Liquid (elementsd) container not running. Start nigiri first:${NC}"
  echo "  nigiri start --liquid"
  exit 1
fi

# Extract LND admin macaroon
MACAROON=$(docker exec lnd xxd -p -c 1000 /data/.lnd/data/chain/bitcoin/regtest/admin.macaroon 2>/dev/null)
if [ -z "$MACAROON" ]; then
  echo -e "${RED}Error: Could not extract LND macaroon${NC}"
  exit 1
fi

echo -e "${GREEN}Nigiri regtest environment detected${NC}"
echo "  LND:       https://localhost:18080"
echo "  elementsd: http://localhost:18884"
echo "  CLN:       docker exec cln lightning-cli"
echo ""

# Build and run
echo "Building escrow-test..."
go build -o escrow-test ./cmd/escrow-test

echo "Running E2E tests..."
echo ""
ESCROW_LND_MACAROON="$MACAROON" ./escrow-test "$@"

# Clean up binary
rm -f escrow-test
