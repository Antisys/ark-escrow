# OpenSats Grant Proposal: Liquid Ark Escrow

## Project Name

Liquid Ark Escrow — Non-custodial escrow on Liquid with Lightning funding

## One-Line Description

A trustless escrow protocol that locks L-BTC in taproot scripts on Liquid, funded via Lightning, with self-custody recovery guarantees — no federation, no custodian, just Bitcoin script.

## Problem

Peer-to-peer Bitcoin commerce needs escrow. Current solutions fall into two categories:

1. **Custodial services** — a third party holds funds. Users must trust they won't steal or lose them. Every custodial escrow is a honeypot.

2. **Federation-based** (e.g., Fedimint escrow modules) — better trust model, but requires running a multi-party federation. The federation members collectively custody the funds. Setup complexity limits adoption.

Neither option gives users the strongest Bitcoin guarantee: **your keys, your coins, verifiable on-chain.**

## Solution

Liquid Ark Escrow locks funds in a **4-leaf taproot script** directly on the Liquid sidechain. No federation, no custodian — the spending conditions are enforced by Bitcoin script:

- **Release**: seller signature + buyer's SHA256 preimage (buyer confirms delivery)
- **Timeout refund**: buyer signature after CSV delay (seller never shipped)
- **Dispute (seller wins)**: oracle + seller signatures
- **Dispute (buyer wins)**: oracle + buyer signatures

The escrow address is derived deterministically from the three participants' public keys. Both buyer and seller can independently reconstruct the script and claim their funds using only a recovery kit and an `elementsd` node — even if the escrow service completely disappears.

Funding uses an atomic Lightning-to-Liquid swap via HODL invoices: the buyer pays Lightning, the service deposits L-BTC into the escrow. The buyer never needs to hold L-BTC directly.

## What Exists Today

A **working proof-of-concept** with full end-to-end functionality:

- CLI tool with 10 commands covering the complete deal lifecycle
- All 4 claim paths implemented and tested (release, refund, dispute-seller, dispute-buyer)
- Self-custody recovery for both buyer and seller (no service needed)
- Atomic LN-to-Liquid funding via HODL invoices
- Recovery kits with base58check encoding and integrity validation
- 35+ randomized property-based unit tests
- 6 E2E test scenarios on Liquid regtest (with real LND + CLN nodes)
- Protocol specification document
- GitHub CI with automated test runs

**Repository:** https://github.com/Antisys/ark-escrow

The cryptographic and protocol logic is complete and verified. What's missing is the user-facing application layer.

## What We'll Build

A **browser-based escrow application** — similar to existing Lightning escrow services but with on-chain Liquid taproot guarantees instead of custodial or federation-based trust.

### Milestone 1: Backend API + Web App Foundation (Month 1-2)

- REST API wrapping the escrow CLI operations (deal CRUD, funding, claims)
- WebSocket support for real-time deal status updates
- SvelteKit frontend: deal creation wizard, buyer join flow, deal detail view
- Client-side secret generation (buyer's preimage never leaves the browser)
- LNURL-auth for wallet-based authentication (no accounts, no passwords)
- Lightning invoice display with QR codes for funding

### Milestone 2: Payout + Recovery UX (Month 3-4)

- Lightning Address and BOLT11 invoice support for payouts
- In-browser recovery kit management (export, import, verify)
- Browser-based recovery: reconstruct escrow and claim on-chain without the service
- Deal expiry automation (background monitoring + auto-refund on timeout)
- Admin interface for oracle dispute resolution

### Milestone 3: Production Hardening (Month 5-6)

- Dynamic fee estimation (replace hardcoded fees)
- Confidential transaction support (hide escrow amounts on-chain)
- TLS certificate validation for LND connections
- Mainnet deployment on Liquid
- User documentation and integration guide

## Budget

**Requesting: $30,000 over 6 months**

| Category | Amount |
|----------|--------|
| Development (6 months, part-time) | $25,000 |
| Infrastructure (servers, Liquid node, domain) | $3,000 |
| Testing and security review | $2,000 |

## Why Liquid?

- **Faster confirmations** (1 minute vs 10) — escrow funding and claims settle quickly
- **Confidential transactions** — escrow amounts can be hidden from chain observers
- **Lower fees** — practical for small-value escrow (marketplace transactions)
- **Taproot support** — same script capabilities as Bitcoin, with the above benefits
- **Federation-secured** — Liquid's federated peg provides the bridge to Bitcoin

## Why This Matters for Bitcoin

Every peer-to-peer Bitcoin transaction that uses custodial escrow is a step backward. Users shouldn't have to choose between convenience and self-custody. This project demonstrates that taproot scripts on Liquid can replace custodians entirely — the escrow rules are public, verifiable, and enforced by consensus, not trust.

The recovery kit model is key: even if the service operator disappears, gets hacked, or turns malicious, users can always claim their funds independently. This is the self-custody guarantee that custodial escrow can never provide.

## About Me

I have a working prototype deployed at trustbro.trade (Lightning escrow using Fedimint). Building this Liquid version was motivated by the limitations of the federation model — requiring a running federation adds operational complexity that limits adoption. The taproot script approach is simpler, more portable, and gives stronger on-chain guarantees.

## Links

- **Repository:** https://github.com/Antisys/ark-escrow
- **Protocol Spec:** https://github.com/Antisys/ark-escrow/blob/master/PROTOCOL.md
- **Existing work (Fedimint version):** https://trustbro.trade
