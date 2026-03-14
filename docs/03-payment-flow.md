# Payment Flow — 2-Party VTXO Transfer

## Tested Flow: Wallet A ↔ Wallet B

### Setup
- **Wallet A**: Arkade Browser Wallet (self-hosted)
- **Wallet B**: `ark` CLI inside arkd container

### Step 1: Fund via Boarding

```bash
# Get boarding address
docker exec arkd ark receive
# → boarding_address: bcrt1p...

# Send regtest coins
nigiri faucet <boarding_address> 0.05
nigiri rpc generatetoaddress 3 $(nigiri rpc getnewaddress)

# Check balance (funds appear as locked onchain)
docker exec arkd ark balance
```

### Step 2: Settle (Boarding → VTXO)

```bash
docker exec arkd ark settle --password password
# → txid: 1565d671...

# Mine a block to confirm settlement
nigiri rpc generatetoaddress 1 $(nigiri rpc getnewaddress)

# Balance shows offchain
docker exec arkd ark balance
# → offchain_balance.total: 5000000
# → next_expiration: "3 hours"
```

### Step 3: Send Off-chain (B → A)

```bash
docker exec arkd ark send \
  --password password \
  --to tark1q... \    # Wallet A's offchain address
  --amount 1000000
# → txid: ddecb9cf...
```

**Result in Wallet A (browser):**
- Amount: 1,000,000 sats
- Status: Preconfirmed
- Fees: 0 sats
- Latency: < 5 seconds

### Step 4: Send Off-chain (A → B)

Send from browser wallet to Wallet B's offchain address.

## Observations

### Timing
| Operation | Duration |
|-----------|----------|
| Boarding → visible in balance | ~10s (needs blocks + polling) |
| Settle (boarding → VTXO) | ~10s (next round) |
| Off-chain send | < 5s (preconfirmed) |
| Preconfirmed → Settled | next on-chain round |

### Fees
- Off-chain transfer: **0 sats**
- Settlement (boarding → VTXO): on-chain fee paid by operator
- Operator provides liquidity for commitment transactions

### VTXO Expiry
- Regtest: ~3.3 hours (`vtxoTreeExpiry=20s`)
- Production: ~7 days (604672s)
- Must renew before expiry or redeem on-chain

## VTXO Data Structure

```json
{
  "outpoint": {
    "txid": "ddecb9cfe5638d4ae0e4962331322bf3077b4021663798936f3848353d290b48",
    "vout": 0
  },
  "createdAt": "1773474943",
  "expiresAt": "1773486691",
  "amount": "1000000",
  "script": "5120bda9c78aae8eb425b82ac82d664443d6d84264adab8974fbbb70796347d6aab3",
  "isPreconfirmed": true,
  "isSwept": false,
  "isUnrolled": false,
  "isSpent": true,
  "spentBy": "c44e3d78f4639c5e8f46eb11b4bb304aad87ca7ab38482f751f9db18cf717430",
  "commitmentTxids": ["1565d671b3d0eb4a08f9f447e219e0f925c896706a79fe8b76a5d6da554e7631"],
  "arkTxid": "6d8d83e8b538811757d8c1fc9aa4d5684b299aa7a5ba1c672384fee0d0bb45f5",
  "assets": []
}
```

### VTXO Status Lifecycle
```
Preconfirmed ──▶ Settled ──▶ Spent
                    │
                    ├──▶ Renewed (before expiry)
                    └──▶ Redeemed (on-chain exit)
```

## API Endpoints Used

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/v1/info` | GET | Server info, pubkeys, config |
| `/v1/indexer/vtxos?outpoints=txid:vout` | GET | Query specific VTXOs |
| `/v1/indexer/virtualTx/{txids}` | GET | Get virtual transaction PSBTs |
| `/v1/indexer/vtxo/{txid}/{vout}/chain` | GET | VTXO chain history |
| `/v1/tx/submit` | POST | Submit transaction |
| `/v1/batch/registerIntent` | POST | Register round intent |
