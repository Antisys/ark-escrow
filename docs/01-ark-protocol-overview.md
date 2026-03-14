# Ark Protocol Overview

## What is Ark?

Ark is a Bitcoin Layer 2 scaling protocol that enables fast, low-cost off-chain transactions while maintaining Bitcoin's security guarantees. It uses **Virtual Transaction Outputs (VTXOs)** — off-chain representations of Bitcoin value that can be transferred instantly between participants.

## Key Concepts

### VTXO (Virtual Transaction Output)
A VTXO is an off-chain output that represents Bitcoin value. It has:
- An **outpoint** (txid:vout) referencing the commitment transaction
- An **amount** in satoshis
- An **expiry time** after which it must be renewed or redeemed on-chain
- A **script** (P2TR) defining spending conditions
- A **status**: `Preconfirmed` → `Settled` → `Spent`

### Rounds
The Ark server (ASP - Ark Service Provider) runs periodic **rounds** where it:
1. Collects intents from users (send/receive)
2. Creates a commitment transaction (on-chain)
3. Builds a VTXO tree with all participants' outputs
4. Settles the round on-chain

### Boarding
To enter the Ark protocol, users send Bitcoin to a **boarding address** — a special P2TR address that can be claimed either:
- **Cooperatively**: by both user + server (instant, via Ark round)
- **Unilaterally**: by user alone after a timeout (on-chain, slow)

### Settlement
Converting boarding funds (on-chain) into VTXOs (off-chain) via a round. After settlement, funds can be transferred instantly off-chain.

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Wallet A  │────▶│    arkd     │◀────│   Wallet B  │
│  (Browser)  │     │  (Server)   │     │   (CLI)     │
└─────────────┘     └──────┬──────┘     └─────────────┘
                           │
                    ┌──────┴──────┐
                    │ arkd-wallet │
                    │  (Signer)   │
                    └──────┬──────┘
                           │
                    ┌──────┴──────┐
                    │  Bitcoin    │
                    │  (regtest)  │
                    └─────────────┘
```

### Components
- **arkd**: Main Ark server — manages rounds, VTXOs, protocol logic
- **arkd-wallet**: Separate signer/wallet service — holds operator keys, provides liquidity
- **NBXplorer**: Bitcoin transaction indexer used by arkd-wallet
- **Chopsticks/Esplora**: Block explorer API for UTXO queries

## VTXO Lifecycle

```
On-chain BTC
    │
    ▼ (send to boarding address)
Boarding UTXO (on-chain, locked)
    │
    ▼ (settle in round)
VTXO [Preconfirmed] (off-chain, instant)
    │
    ▼ (round settles on-chain)
VTXO [Settled] (off-chain, confirmed)
    │
    ├──▶ Send to another user → new VTXO
    ├──▶ Renew before expiry → new VTXO
    └──▶ Redeem on-chain → regular UTXO
```
