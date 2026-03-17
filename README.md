# Liquid Ark Escrow

Non-custodial escrow on Liquid Network with Lightning Network funding.

Locks L-BTC in a 4-leaf taproot script: release (seller + preimage), timeout refund (buyer + CSV), and two dispute paths (oracle + party). Both buyer and seller can recover funds independently using recovery kits, even if the escrow service disappears.

```
Buyer (Lightning)              Liquid Network                    Seller
     |                              |                               |
     |-- pay HODL invoice -------->|                               |
     |                        funds locked in                      |
     |                        4-leaf taproot escrow                |
     |                              |                               |
     |                        Leaf 0: seller sig + preimage  ----->|  (release)
     |  <-- Leaf 1: buyer sig + CSV timeout                        |  (refund)
     |                        Leaf 2: oracle + seller sig    ----->|  (dispute)
     |  <-- Leaf 3: oracle + buyer sig                             |  (dispute)
```

See [PROTOCOL.md](PROTOCOL.md) for the full protocol specification.

## Quick Start (regtest)

### Prerequisites

- [Go](https://go.dev/dl/) 1.22+
- [Nigiri](https://github.com/vulpemventures/nigiri) (provides elementsd, LND, CLN on regtest)

### Setup

```bash
# Start the regtest environment
nigiri start --liquid

# Clone and build
git clone https://github.com/Antisys/ark-escrow.git
cd ark-escrow
go build -o escrow ./cmd/escrow
```

### Usage

```bash
# Seller creates a deal
./escrow create --amount 50000 --title "Widget" --oracle-pubkey <oracle_hex>

# Buyer joins with the join token
./escrow join --token '<json_token>'

# Fund via Lightning
./escrow fund --deal <deal_id>

# Seller ships
./escrow ship --deal <deal_id>

# Release to seller (or refund/dispute)
./escrow release --deal <deal_id> --seller-invoice <bolt11>

# Self-custody recovery (no service needed)
./escrow recover --kit <recovery_kit> --dest-address <address>
```

## Commands

| Command | Description |
|---------|-------------|
| `create` | Seller creates a deal |
| `join` | Buyer joins, generates secret, computes escrow address |
| `fund` | Fund escrow via LN atomic swap |
| `ship` | Seller marks shipped |
| `release` | Release to seller (preimage + seller sig) |
| `refund` | Refund to buyer after CSV timeout |
| `dispute` | Oracle resolves dispute |
| `status` | Show deal state |
| `recoverykit` | Export recovery kit |
| `recover` | Claim from recovery kit without service |

## Configuration

| Flag | Env | Default |
|------|-----|---------|
| `--datadir` | `ESCROW_DATADIR` | `~/.ark-escrow/deals` |
| `--lnd-url` | `ESCROW_LND_URL` | `https://localhost:18080` |
| `--lnd-macaroon` | `ESCROW_LND_MACAROON` | -- |
| `--elementsd-url` | `ESCROW_ELEMENTSD_URL` | `http://admin1:123@localhost:18884` |
| `--oracle-pubkey` | `ESCROW_ORACLE_PUBKEY` | -- |
| `--network-hrp` | `ESCROW_NETWORK_HRP` | `ert` |

## Tests

```bash
# Unit + property-based tests (35+ tests, randomized)
go test ./pkg/escrow/... -v

# E2E tests (requires nigiri running with --liquid)
./scripts/run-e2e.sh

# E2E with verbose output (shows protocol details)
./scripts/run-e2e.sh -v
```

The E2E test suite runs 6 scenarios end-to-end on Liquid regtest:

| Scenario | What it tests |
|----------|---------------|
| Release | Happy path: fund, ship, release to seller via LN |
| Refund | Timeout: fund, mine past CSV, refund to buyer via LN |
| Buyer recovery | Service disappears, buyer claims with recovery kit only |
| Seller recovery | Service disappears, seller claims with kit + buyer's secret |
| Dispute (seller wins) | Oracle + seller claim via dispute leaf |
| Dispute (buyer wins) | Oracle + buyer claim via dispute leaf |

All amounts and timeouts are randomized on each run.

## License

MIT
