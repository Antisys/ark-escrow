# Liquid Ark Escrow

Non-custodial escrow on Liquid Network with Lightning Network funding.

## Why This Exists

People under financial repression need to trade without intermediaries that can be seized, censored, or coerced. Every custodial escrow service is a single point of failure — governments can shut it down, freeze funds, or compel the operator to steal from users.

This project replaces the custodian with **Bitcoin script**. Funds are locked in a taproot output on the Liquid sidechain with four spending conditions enforced by consensus, not trust. Both buyer and seller can recover their funds independently — even if the escrow service disappears completely. No federation to compromise. No custodian to subpoena. Just cryptographic guarantees.

## How It Works

Funds are locked in a **4-leaf taproot script** on Liquid. Each leaf is a different spending condition:

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

- **Release**: buyer confirms delivery by revealing a secret, seller claims payment
- **Timeout refund**: seller never ships, buyer reclaims after a time delay
- **Dispute**: an independent oracle resolves disagreements (cannot act alone — always needs the winning party's signature)

The buyer funds the escrow by paying a Lightning invoice. No need to hold L-BTC directly.

**Self-custody guarantee**: both parties receive a recovery kit — a compact blob containing their private key and all escrow parameters. With just this kit and an elementsd node, they can reconstruct the tapscript tree and claim their funds on-chain. No service, no internet connection to the escrow operator, no Lightning channel required.

See [PROTOCOL.md](PROTOCOL.md) for the full protocol specification.

## Quick Start (regtest)

### Prerequisites

- [Go](https://go.dev/dl/) 1.22+
- [Docker](https://docs.docker.com/get-docker/)
- [Nigiri](https://github.com/vulpemventures/nigiri) — sets up a complete Bitcoin + Liquid + Lightning regtest environment in one command

Install Nigiri:
```bash
curl https://getnigiri.vulpemventures.com | bash
```

### Setup

```bash
# Start the regtest environment (Bitcoin + Liquid + LND + CLN)
nigiri start --liquid

# Clone and build
git clone https://github.com/Antisys/ark-escrow.git
cd ark-escrow
go build -o escrow ./cmd/escrow
```

### Run the E2E Tests

The E2E test suite runs all 6 escrow scenarios end-to-end on Liquid regtest with real Lightning nodes:

```bash
./scripts/run-e2e.sh
```

This automatically detects the running Nigiri environment, extracts the LND macaroon, and executes the full test suite. No manual configuration needed.

For verbose output showing the protocol details (tapscript construction, witness assembly, on-chain claims):

```bash
./scripts/run-e2e.sh -v
```

### Run the Unit Tests

No infrastructure needed — these run anywhere with Go installed:

```bash
go test ./pkg/escrow/... -v
```

65 tests including randomized property-based tests with random keys, amounts (10k–200k sats), and timeouts (10–200 blocks) on every run.

### CLI Usage

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

## E2E Test Scenarios

| Scenario | What it tests |
|----------|---------------|
| Release | Happy path: fund, ship, release to seller via LN |
| Refund | Timeout: fund, mine past CSV, refund to buyer via LN |
| Buyer recovery | Service disappears, buyer claims with recovery kit only |
| Seller recovery | Service disappears, seller claims with kit + buyer's secret |
| Dispute (seller wins) | Oracle + seller claim via dispute leaf |
| Dispute (buyer wins) | Oracle + buyer claim via dispute leaf |

Additionally, the security test verifies that a buyer **cannot** claim funds before the CSV timeout expires — proving the escrow actually protects the seller during the deal.

## Configuration

| Flag | Env | Default |
|------|-----|---------|
| `--datadir` | `ESCROW_DATADIR` | `~/.ark-escrow/deals` |
| `--lnd-url` | `ESCROW_LND_URL` | `https://localhost:18080` |
| `--lnd-macaroon` | `ESCROW_LND_MACAROON` | -- |
| `--elementsd-url` | `ESCROW_ELEMENTSD_URL` | `http://admin1:123@localhost:18884` |
| `--oracle-pubkey` | `ESCROW_ORACLE_PUBKEY` | -- |
| `--network-hrp` | `ESCROW_NETWORK_HRP` | `ert` |

## Security Properties

| Property | How it works |
|----------|-------------|
| **No custodian** | Funds locked in taproot script, not held by any party |
| **Buyer protection** | Automatic refund after timeout, no seller cooperation needed |
| **Seller protection** | Funds locked for the deal duration, buyer cannot reclaim early |
| **Censorship resistant** | Recovery kits allow claiming without the service or internet |
| **Oracle limited** | Oracle cannot claim alone — always needs the winning party's signature |
| **Verifiable on-chain** | Escrow address is deterministic — anyone can verify the script |

## License

MIT
