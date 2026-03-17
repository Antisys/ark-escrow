# Liquid Ark Escrow Protocol

A non-custodial escrow protocol on Liquid Network with Lightning Network funding. Enables trustless buyer-seller transactions where funds are locked in a taproot escrow until goods are delivered, with timeout refunds and oracle-mediated dispute resolution.

## Overview

The escrow locks L-BTC in a 4-leaf taproot script on Liquid. The buyer funds the escrow by paying a Lightning invoice, which triggers an atomic swap into the on-chain escrow output. The escrow can be released to the seller (normal case), refunded to the buyer (timeout), or resolved by an oracle (dispute).

No party has unilateral custody of the funds. Both buyer and seller can recover their funds independently using only a recovery kit and an elementsd node, even if the escrow service disappears.

## Participants

| Role | Description |
|------|-------------|
| **Buyer** | Purchases goods/services. Funds the escrow via Lightning. Generates a secret used for release. |
| **Seller** | Offers goods/services. Creates the deal. Claims funds via the release leaf when buyer releases. |
| **Oracle** | Trusted third party for dispute resolution only. Has no access to funds without cooperation from buyer or seller. |
| **Service** | Operates the escrow infrastructure (LND node, elementsd, deal storage). Facilitates the LN-to-Liquid atomic swap. Not custodial — both parties can bypass the service via recovery kits. |

## Escrow Script

The escrow is a **taproot output** (P2TR) with an unspendable internal key and a 4-leaf tapscript tree. Each leaf represents a different spending condition:

```
                    [Taproot Output]
                   (unspendable key)
                         |
                    [Tap Tree Root]
                    /            \
               /                      \
        [Branch]                    [Branch]
        /      \                    /      \
   Leaf 0     Leaf 1           Leaf 2     Leaf 3
  (Release)  (Timeout)      (Dispute     (Dispute
                             →Seller)     →Buyer)
```

### Leaf 0 — Release (seller claims with buyer's preimage)

```
OP_SHA256 <secret_hash> OP_EQUAL OP_VERIFY <seller_pubkey> OP_CHECKSIG
```

**Witness:** `[seller_signature, preimage, leaf_script, control_block]`

The seller can claim when the buyer reveals their 32-byte secret (whose SHA256 hash was committed at deal creation). This is the normal happy path — the buyer releases the secret to authorize payment after receiving goods.

### Leaf 1 — Timeout (buyer refunds after CSV delay)

```
<timeout_blocks> OP_CHECKSEQUENCEVERIFY OP_DROP <buyer_pubkey> OP_CHECKSIG
```

**Witness:** `[buyer_signature, leaf_script, control_block]`
**Sequence:** BIP68-encoded relative locktime

The buyer can reclaim funds after `timeout_blocks` confirmations have elapsed since the funding transaction. This protects the buyer if the seller never ships or disappears.

### Leaf 2 — Dispute: seller wins (oracle + seller multisig)

```
<oracle_pubkey> OP_CHECKSIGVERIFY <seller_pubkey> OP_CHECKSIG
```

**Witness:** `[seller_signature, oracle_signature, leaf_script, control_block]`

The oracle and seller cooperate to claim when a dispute is resolved in the seller's favor. No preimage or timeout needed.

### Leaf 3 — Dispute: buyer wins (oracle + buyer multisig)

```
<oracle_pubkey> OP_CHECKSIGVERIFY <buyer_pubkey> OP_CHECKSIG
```

**Witness:** `[buyer_signature, oracle_signature, leaf_script, control_block]`

The oracle and buyer cooperate to claim when a dispute is resolved in the buyer's favor.

### Script Properties

- **Unspendable internal key:** No key-path spend is possible. All spending must go through one of the four script leaves.
- **Elements-specific:** Uses `TapLeaf/elements` tagged hashes (not Bitcoin's `TapLeaf`), `HashForWitnessV1` with the Liquid genesis block hash, and Elements taproot control blocks.
- **Deterministic:** Given the same three public keys, secret hash, and timeout, the escrow address is always the same. This enables independent reconstruction from a recovery kit.

## Deal Lifecycle

```
CREATED ──→ JOINED ──→ FUNDED ──→ SHIPPED ──→ RELEASED
                          │
                          ├──────────────────────→ REFUNDED  (after CSV timeout)
                          │
                          └──────────────────────→ DISPUTED  (oracle resolution)
```

| State | Transition | Action |
|-------|-----------|--------|
| **CREATED** | Seller creates deal | Seller generates keypair, specifies amount/title/timeout/oracle |
| **JOINED** | Buyer joins | Buyer generates keypair + secret, computes escrow address |
| **FUNDED** | Escrow funded via LN | Atomic swap: LN payment → L-BTC locked in escrow VTXO |
| **SHIPPED** | Seller marks shipped | State flag only, no on-chain action |
| **RELEASED** | Buyer releases to seller | On-chain claim via release leaf + LN payout to seller |
| **REFUNDED** | Buyer reclaims after timeout | On-chain claim via timeout leaf + LN payout to buyer |
| **DISPUTED** | Oracle resolves dispute | On-chain claim via dispute leaf + LN payout to winner |

## Funding: Lightning → Liquid Atomic Swap

The buyer pays Lightning; the service deposits L-BTC into the escrow. This is an atomic swap using HODL invoices.

### Atomic HTLC Swap

The service locks L-BTC in an intermediate HTLC on Liquid (claimable with preimage, refundable on timeout). This ensures atomicity: if the service crashes between steps, no funds are lost.

```
1. Service generates preimage P, hash H = SHA256(P)
2. Service locks L-BTC in Liquid HTLC:
   - Claim leaf: reveal P + service sig → escrow address
   - Refund leaf: CSV timeout → service reclaims
3. Service creates HODL invoice on LND with hash H
4. Buyer pays HODL invoice → LND holds HTLC
5. Service detects ACCEPTED state on LND
6. Service claims Liquid HTLC with preimage → funds move to escrow address
7. Service settles HODL invoice (reveals P, receives BTC on Lightning)
```

**Failure modes:**
- Crash between steps 2–3: Liquid HTLC times out, service reclaims L-BTC. No loss.
- Crash between steps 6–7: Escrow VTXO exists on-chain (funds are safe). HODL invoice times out on LN. Service absorbs the LN cost but can detect this on restart.

## Claim Paths

Each claim path involves two steps: (1) spend the escrow VTXO on-chain, (2) pay the recipient via Lightning.

### Release (normal case)

1. **On-chain:** Build a Liquid transaction spending the escrow VTXO via leaf 0.
   - Seller signs with their private key (Elements sighash with genesis hash + leaf hash)
   - Buyer's 32-byte secret is included as witness data
   - `OP_SHA256 <secret> == <secret_hash>` is verified by the script
2. **Lightning:** Service pays the seller's invoice from LND

### Refund (timeout)

1. **On-chain:** Build a Liquid transaction with BIP68 sequence number spending via leaf 1.
   - Buyer signs with their private key
   - CSV locktime must have elapsed (confirmed blocks since funding tx)
2. **Lightning:** Service pays the buyer's refund invoice from LND

### Dispute (oracle resolution)

1. **On-chain:** Build a Liquid transaction spending via leaf 2 (seller wins) or leaf 3 (buyer wins).
   - Oracle signs with their private key
   - Winner (seller or buyer) signs with their private key
   - 2-of-2 Schnorr signatures: `oracle_sig + winner_sig`
2. **Lightning:** Service pays the winner's invoice from LND

## Recovery Kit

A recovery kit is a self-contained blob that allows a user to claim escrowed funds without the service. It contains everything needed to reconstruct the tapscript tree and build a claim transaction.

### Contents

| Field | Description |
|-------|-------------|
| `role` | "buyer" or "seller" |
| `privkey` | User's 32-byte private key (hex) |
| `secret` | Buyer's preimage (buyer kit only) |
| `seller_pubkey` | Seller's x-only public key |
| `buyer_pubkey` | Buyer's x-only public key |
| `oracle_pubkey` | Oracle's x-only public key |
| `secret_hash` | SHA256 hash of buyer's secret |
| `timeout_blocks` | CSV locktime in blocks |
| `fund_txid` | Funding transaction ID |
| `fund_vout` | Funding output index |
| `amount` | Escrow amount in satoshis |
| `network_hrp` | Bech32 HRP ("ert" for regtest, "ex" for mainnet) |

### Encoding

```
arkescrow<base58check(json_data, version=0x42)>
```

The kit is JSON-serialized, then base58check-encoded with a version byte and 4-byte checksum for corruption detection, prefixed with `arkescrow`.

### Buyer Recovery

The buyer can claim via the **timeout leaf** (leaf 1) after the CSV locktime expires. Requires only:
- The recovery kit
- Access to an elementsd node
- Enough blocks elapsed since funding

No service, no Lightning, no seller cooperation needed.

### Seller Recovery

The seller can claim via the **release leaf** (leaf 0) at any time, but needs the buyer's secret. Requires:
- The seller recovery kit
- The buyer's 32-byte secret (revealed separately by the buyer)
- Access to an elementsd node

No CSV timeout needed — the seller can claim immediately once they have the secret.

### Validation

The recovery kit validates:
- Role is "buyer" or "seller"
- Private key is 32 bytes and corresponds to the role's public key (x-only comparison)
- All three public keys, secret hash, timeout, and amount are present
- Base58check checksum is correct (detects accidental corruption)

## Transaction Construction

All transactions use **PSETv2** (Partially Signed Elements Transaction) with explicit fee outputs.

### Structure

```
Input:  escrow UTXO (txid:vout)
Output 0: value to destination address
Output 1: explicit fee (no script)
```

### Signing

1. Compute the leaf script and control block from the escrow's tapscript tree
2. Create a PSET with the escrow UTXO as input and destination + fee as outputs
3. Compute the Elements-specific sighash: `HashForWitnessV1(input_index, prevout_scripts, prevout_assets, prevout_values, SigHashDefault, genesis_hash, &leaf_hash, nil)`
4. Sign with Schnorr (`schnorr.Sign(key, sighash)`)
5. Assemble the tapscript witness: `[signatures...] [preimage if release] [leaf_script] [control_block]`
6. Extract and broadcast via `sendrawtransaction`

### Witness Stack Order

| Leaf | Witness (bottom → top of stack) |
|------|------|
| Release | `[seller_sig, preimage, leaf_script, control_block]` |
| Timeout | `[buyer_sig, leaf_script, control_block]` |
| Dispute | `[winner_sig, oracle_sig, leaf_script, control_block]` |

The witness pushes items onto the stack in order, so the first item (`seller_sig` / `buyer_sig` / `winner_sig`) ends up at the bottom. The script executes top-down, consuming items from the top first.

## Security Model

### Trust Assumptions

| Property | Guarantee |
|----------|-----------|
| **Seller protection** | Funds locked for `timeout_blocks`. Buyer cannot reclaim early. |
| **Buyer protection** | Funds automatically refundable after timeout. No seller cooperation needed. |
| **Non-custodial** | Neither party, oracle, nor service can unilaterally spend the escrow. |
| **Self-custody** | Both parties can recover funds using only a recovery kit + elementsd. |
| **Oracle limited** | Oracle can only resolve disputes, never claim funds alone. Requires cooperation of the winning party. |
| **Service dispensable** | Service facilitates the LN-Liquid bridge but is not required for on-chain claims or recovery. |

### Attack Vectors

| Attack | Mitigation |
|--------|------------|
| Buyer refuses to release after receiving goods | Seller waits for oracle dispute resolution (leaf 2) |
| Seller never ships | Buyer waits for CSV timeout, then claims refund (leaf 1) |
| Service disappears | Both parties use recovery kits to claim on-chain without the service |
| Oracle colludes with seller | Oracle cannot claim alone — needs seller's signature too. Buyer can still refund via timeout. |
| Oracle colludes with buyer | Oracle cannot claim alone — needs buyer's signature too. Seller has the goods. |
| Preimage leaked early | Only the seller can use the preimage (release leaf requires seller's signature) |
| Recovery kit stolen | Kit contains private key — must be stored securely (same as any wallet backup) |

## CLI Commands

| Command | Description |
|---------|-------------|
| `escrow create` | Seller creates a deal (amount, title, timeout, oracle pubkey) |
| `escrow join` | Buyer joins a deal (generates keypair + secret, computes escrow address) |
| `escrow fund` | Fund escrow via Lightning atomic swap |
| `escrow ship` | Seller marks deal as shipped |
| `escrow release` | Release escrow to seller (claim + LN payout) |
| `escrow refund` | Refund escrow to buyer after timeout (claim + LN payout) |
| `escrow dispute` | Oracle resolves dispute (claim + LN payout to winner) |
| `escrow status` | Display deal state |
| `escrow recoverykit` | Export recovery kit for buyer or seller |
| `escrow recover` | Claim funds using only a recovery kit (no service needed) |

## Known Limitations

- **Regtest only:** TLS verification is disabled for LND. Transaction fees are hardcoded (500 sats). Block generation is used for confirmations.
- **No oracle protocol:** The oracle is a keypair passed as a CLI flag. There is no oracle discovery, reputation, or automated dispute resolution.
- **No fee estimation:** Claim transaction fees are hardcoded rather than dynamically estimated.
- **Single-use escrow:** Each deal creates a new escrow output. There is no batching or output reuse.
- **No confidential transactions:** Escrow amounts are visible on-chain. A production version could use Liquid's confidential transaction features.

## Test Coverage

The implementation includes:
- **35+ unit tests** with randomized property-based testing (random keys, amounts 10k–200k sats, timeouts 10–200 blocks)
- **6 E2E test scenarios** on Liquid regtest with real LND and CLN nodes:
  - Release path (happy path)
  - Refund path (CSV timeout)
  - Buyer recovery without service (timeout leaf from recovery kit)
  - Seller recovery without service (release leaf from recovery kit + buyer's secret)
  - Dispute → seller wins
  - Dispute → buyer wins
- **Security tests:** Early refund rejection (before CSV), recovery kit integrity verification
