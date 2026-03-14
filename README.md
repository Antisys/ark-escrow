# Ark Escrow

3-Party escrow system built on the [Ark Protocol](https://github.com/arkade-os/arkd) — a Bitcoin Layer 2 for instant off-chain transactions.

## Status

**Phase 1: Foundation** — completed
- [x] arkd deployed on regtest (Docker Compose)
- [x] Arkade Wallet self-hosted with HTTPS
- [x] 2-party payment flow tested (send/receive VTXOs)
- [x] VTXO structure documented

**Phase 2: Escrow Design** — in progress
- [ ] 3-party VTXO flow architecture
- [ ] Custom tapscript leaves for escrow conditions
- [ ] Escrow agent key management

## Documentation

- [Ark Protocol Overview](docs/01-ark-protocol-overview.md)
- [Deployment Guide](docs/02-deployment-guide.md)
- [Payment Flow & VTXO Structure](docs/03-payment-flow.md)
- [Known Issues & Workarounds](docs/04-known-issues.md)

## Infrastructure

- [`infra/Caddyfile`](infra/Caddyfile) — HTTPS reverse proxy config
- [`infra/esplora-proxy.mjs`](infra/esplora-proxy.mjs) — Bech32m address converter for regtest
- [`infra/generate-cert.sh`](infra/generate-cert.sh) — Self-signed TLS cert generator

## Quick Start

See [Deployment Guide](docs/02-deployment-guide.md) for full setup instructions.

```bash
# Prerequisites: Docker, Nigiri
nigiri start
cd ark && docker compose -f docker-compose.regtest.yml up --build -d
docker exec arkd arkd wallet create --password password
docker exec arkd arkd wallet unlock --password password
```
