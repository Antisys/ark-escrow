# Lightning Integration & Custody Analysis

## Architecture Decision: Embedded Wallet (Option A)

The escrow service embeds the Arkade SDK directly in its web frontend. Users get a browser-based Ark wallet automatically on first visit — invisible to them. They interact only with Lightning invoices and deal status.

### User Flow

```
1. User opens escrow.example.com
2. SDK creates Ark wallet in browser (automatic, one-time)
   → Keys in IndexedDB, Service Worker manages VTXO state
   → User sees nothing wallet-related
3. User funds via Lightning (Boltz Swap into own browser wallet)
   → User scans LN QR code, pays from any LN wallet
   → Boltz converts LN → Ark VTXO in user's browser wallet
4. User clicks "Start Deal"
   → SDK builds Escrow VTXO locally with 4 tapscript leaves
   → Sends directly to escrow P2TR address
5. User sees: "Payment secured ✓"
```

### Why This Is Non-Custodial

The funds **never pass through the service**:

```
NON-CUSTODIAL (our approach):
  LN → Boltz → User's Browser Wallet → Escrow VTXO (direct)
                     ↑                        ↑
              User controls keys      Bitcoin Script locks funds
              (IndexedDB)             (no single party can move)

CUSTODIAL (what we avoid):
  LN → Service Wallet → Escrow VTXO
            ↑
       Service has custody
```

The escrow service only provides:
- Its public key (for the tapscript)
- Deal metadata (buyer, seller, amount)
- Release/refund signatures when needed

It never touches, holds, or routes the funds.

## Boltz Swap Fees (Mainnet)

| Direction | Service Fee | Miner Fee | Total Example (500k sats) |
|-----------|------------|-----------|---------------------------|
| LN → Ark (Submarine) | 0.1% | 302 sats | ~802 sats |
| Ark → LN (Reverse) | 0.5% | 530 sats | ~3,030 sats |

### Cost Comparison

| Method | Escrow Cost | Speed |
|--------|------------|-------|
| Ark Escrow (both parties have Ark) | **0 sats** | Seconds |
| Ark Escrow + LN onramp/offramp | ~0.6-1.4% | Seconds |
| PayPal Escrow | 3-5% | Days |
| Traditional Escrow | 1-5% + fees | Days-Weeks |

## Boltz Custody Analysis

Boltz swaps are **atomic** (HTLC-based) but have a brief liveness risk:

| Phase | Who has funds? | Risk |
|-------|---------------|------|
| User pays LN invoice | Boltz receives LN payment | Boltz has LN sats for seconds |
| Boltz locks HTLC on-chain | HTLC script (trustless) | None — atomic |
| User claims with preimage | User has funds | None |

**Boltz cannot steal**: without the user's preimage, Boltz cannot claim the HTLC.
After timeout, funds return automatically. The risk is **liveness** (Boltz goes offline),
not **custody** (Boltz runs away with funds).

## Hybrid Model: Minimize Boltz Exposure

```
Onramp:  LN → Boltz → User's Ark Wallet     (one-time, 0.1% fee)
Escrow:  Ark VTXO → Escrow VTXO → Ark VTXO  (free, fully non-custodial)
Offramp: Ark → Boltz → LN                    (only at cashout, 0.5% fee)
```

The escrow itself (lock, dispute, release, refund) happens entirely on Ark with
**zero fees and zero custody**. Boltz is only used at the edges for Lightning
compatibility.

## Why Users Need a Wallet (Even If Hidden)

Without a wallet, the user cannot:
- Generate keys for the tapscript
- Sign the escrow VTXO transaction
- Verify the tapscript leaves are correct
- Claim timeout refunds unilaterally

If the service built the VTXO on behalf of the user, it would be **custodial** —
the service would control the keys. The embedded wallet solves this by putting
key management in the browser, invisible to the user.

The Arkade SDK already supports this pattern via `ServiceWorkerWallet.setup()`.
