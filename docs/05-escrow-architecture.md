# Escrow Architecture — 3-Party VTXO Flow

## Problem

A buyer wants to pay a seller for goods/services, but neither trusts the other. An escrow agent mediates: funds are locked until the agent confirms delivery, then released to the seller. If the deal fails, funds return to the buyer.

## How Ark Makes This Possible

Ark VTXOs are **Taproot outputs** with multiple **tapleaf closures** — each leaf defines a different spending path. The standard VTXO has two paths:

```
Default VTXO:
  Leaf 0: [Owner + Server] CHECKSIG          ← collaborative spend
  Leaf 1: [Owner] CHECKSIG after CSV delay   ← unilateral exit
```

For escrow, we add **custom closures** with conditions involving three parties:

```
Escrow VTXO:
  Leaf 0: [Buyer + Seller + Server] CHECKSIG           ← mutual agreement (instant release)
  Leaf 1: [Seller + Escrow + Server] CHECKSIG           ← escrow approves delivery
  Leaf 2: [Buyer + Escrow + Server] CHECKSIG             ← escrow approves refund
  Leaf 3: [Buyer] CHECKSIG after CSV timeout              ← buyer safety exit (fallback)
```

## Architecture Overview

```
                    ┌──────────────┐
                    │ Escrow Agent │
                    │  (arbiter)   │
                    └──────┬───────┘
                           │ signs release/refund
                           │
┌──────────┐    lock     ┌─┴──────────┐    release    ┌──────────┐
│  Buyer   │────────────▶│  Escrow    │──────────────▶│  Seller  │
│          │             │   VTXO     │               │          │
│          │◀────────────│ (3-party)  │               │          │
└──────────┘   refund    └────────────┘               └──────────┘
                               │
                          ┌────┴────┐
                          │  arkd   │
                          │ (ASP)   │
                          └─────────┘
```

## Closure Design

### Using Existing Ark Primitives

The arkd codebase already provides all necessary closure types in `pkg/ark-lib/script/closure.go`:

| Closure Type | Purpose in Escrow |
|---|---|
| `MultisigClosure` | Collaborative spend (all parties agree) |
| `CSVMultisigClosure` | Timeout-based exit (buyer fallback) |
| `ConditionMultisigClosure` | Conditional release (hashlock + multisig) |

### Option A: Pure Multisig Escrow (Simplest)

Three spending paths using `MultisigClosure` and `CSVMultisigClosure`:

```go
escrowVtxo := &script.TapscriptsVtxoScript{
    Closures: []script.Closure{
        // Leaf 0: Mutual release — Buyer + Seller agree (no escrow needed)
        &script.MultisigClosure{
            PubKeys: []*secp256k1.PublicKey{buyerKey, sellerKey, serverKey},
        },
        // Leaf 1: Escrow releases to Seller — Seller + Escrow + Server
        &script.MultisigClosure{
            PubKeys: []*secp256k1.PublicKey{sellerKey, escrowKey, serverKey},
        },
        // Leaf 2: Escrow refunds to Buyer — Buyer + Escrow + Server
        &script.MultisigClosure{
            PubKeys: []*secp256k1.PublicKey{buyerKey, escrowKey, serverKey},
        },
        // Leaf 3: Buyer unilateral exit after timeout
        &script.CSVMultisigClosure{
            MultisigClosure: script.MultisigClosure{
                PubKeys: []*secp256k1.PublicKey{buyerKey},
            },
            Locktime: script.RelativeLocktime{
                Type:  script.LocktimeTypeBlock,
                Value: 144, // ~1 day
            },
        },
    },
}
```

**Pros:** Simple, uses existing closure types, no protocol changes needed.
**Cons:** Requires escrow agent to be online for signing.

### Option B: Hash-Locked Escrow (Trustless Release)

Uses `ConditionMultisigClosure` with a hashlock. The escrow agent reveals a preimage to release funds, without needing to sign:

```go
// Escrow generates secret preimage, gives hash to both parties
preimageHash := sha256.Sum256(preimage)

escrowVtxo := &script.TapscriptsVtxoScript{
    Closures: []script.Closure{
        // Leaf 0: Release to Seller — knows preimage + Seller signs
        &script.ConditionMultisigClosure{
            Condition: buildHashlockScript(preimageHash),
            MultisigClosure: script.MultisigClosure{
                PubKeys: []*secp256k1.PublicKey{sellerKey, serverKey},
            },
        },
        // Leaf 1: Refund to Buyer after timeout
        &script.CSVMultisigClosure{
            MultisigClosure: script.MultisigClosure{
                PubKeys: []*secp256k1.PublicKey{buyerKey, serverKey},
            },
            Locktime: script.RelativeLocktime{
                Type:  script.LocktimeTypeBlock,
                Value: 1008, // ~1 week
            },
        },
    },
}

func buildHashlockScript(hash [32]byte) []byte {
    script, _ := txscript.NewScriptBuilder().
        AddOp(txscript.OP_SHA256).
        AddData(hash[:]).
        AddOp(txscript.OP_EQUAL).
        Script()
    return script
}
```

**Pros:** Escrow doesn't need to sign, only reveal preimage. More trust-minimized.
**Cons:** Escrow must keep preimage secret. No partial release.

### Option C: 2-of-3 Multisig Escrow (Most Flexible)

Any two of three parties can release funds. Requires a custom closure or three separate 2-of-3 leaves:

```go
escrowVtxo := &script.TapscriptsVtxoScript{
    Closures: []script.Closure{
        // Leaf 0: Buyer + Seller agree (escrow not needed)
        &script.MultisigClosure{
            PubKeys: []*secp256k1.PublicKey{buyerKey, sellerKey, serverKey},
        },
        // Leaf 1: Seller + Escrow agree (buyer disputes, escrow sides with seller)
        &script.MultisigClosure{
            PubKeys: []*secp256k1.PublicKey{sellerKey, escrowKey, serverKey},
        },
        // Leaf 2: Buyer + Escrow agree (seller no-show, escrow refunds)
        &script.MultisigClosure{
            PubKeys: []*secp256k1.PublicKey{buyerKey, escrowKey, serverKey},
        },
        // Leaf 3: Buyer unilateral exit (ultimate fallback)
        &script.CSVMultisigClosure{
            MultisigClosure: script.MultisigClosure{
                PubKeys: []*secp256k1.PublicKey{buyerKey},
            },
            Locktime: script.RelativeLocktime{
                Type:  script.LocktimeTypeBlock,
                Value: 2016, // ~2 weeks
            },
        },
    },
}
```

**Pros:** Most flexible, handles all dispute scenarios. Standard multisig.
**Cons:** Server must co-sign all paths (but this is standard in Ark).

## Recommended: Option C (2-of-3 with Server)

Option C is the best balance of flexibility and simplicity:

- **Happy path** (Leaf 0): Buyer confirms receipt, both sign → Seller gets paid instantly
- **Dispute → Seller wins** (Leaf 1): Escrow verifies delivery, signs with Seller
- **Dispute → Buyer wins** (Leaf 2): Escrow confirms non-delivery, signs refund
- **Dead escrow** (Leaf 3): Buyer can always exit after timeout (trustless fallback)

The **server (ASP)** co-signs every path — this is inherent to Ark's design. The server ensures the VTXO tree remains valid.

## Implementation Plan

### Phase 1: Escrow Service (Backend)

A new service that:
1. Generates escrow keypair per deal
2. Accepts deal terms (buyer, seller, amount, conditions)
3. Constructs the escrow VTXO tapscript
4. Coordinates signing for release/refund

```
/escrow
  POST /create        ← create escrow deal, return escrow pubkey + script
  POST /release       ← escrow signs release to seller
  POST /refund        ← escrow signs refund to buyer
  GET  /status/:id    ← deal status
```

### Phase 2: Custom VTXO Registration

The buyer must register an **intent** with arkd that produces a VTXO with the escrow tapscript instead of the default script. This requires:

1. **Client-side**: Build the escrow `TapscriptsVtxoScript`, compute the P2TR address
2. **Intent registration**: Register a boarding or send intent targeting the escrow P2TR address
3. **Server validation**: arkd must accept the custom script (it validates forfeit closures contain the server pubkey)

**Key constraint**: Every closure must include the server's pubkey for the forfeit mechanism. This is already satisfied in our design.

### Phase 3: Spending the Escrow VTXO

To release funds (e.g., escrow approves delivery):
1. Escrow service signs the transaction (Leaf 1)
2. Seller signs the transaction
3. Server co-signs (standard Ark flow)
4. Transaction submitted to arkd → new VTXO for Seller

### Phase 4: Integration with Wallet UI

- Escrow creation flow in the Arkade Wallet
- Deal status tracking
- Sign/approve UI for all parties

## VTXO Script Validation

arkd validates VTXO scripts on registration. Key rules from `pkg/ark-lib/script/vtxo_script.go`:

```go
// ForfeitClosures: Must contain server pubkey
// → MultisigClosure, CLTVMultisigClosure, ConditionMultisigClosure
//   All our escrow leaves include serverKey ✓

// ExitClosures: Must be CSV-based
// → CSVMultisigClosure, ConditionCSVMultisigClosure
//   Our Leaf 3 (buyer exit) uses CSVMultisigClosure ✓
```

## Security Considerations

1. **Escrow key management**: Escrow agent's private key must be highly secured (HSM, multi-sig, or threshold)
2. **Timeout selection**: Buyer exit timeout must be shorter than VTXO expiry to prevent funds getting locked
3. **Server liveness**: Server must be online for collaborative paths. Buyer exit (Leaf 3) is the trustless fallback
4. **Escrow collusion**: Escrow + Seller could collude. Mitigation: reputation system, multiple escrow agents, or hash-locked variant (Option B)
5. **VTXO expiry**: Escrow deals must settle before VTXO expires. For production (~7 days), this limits deal duration

## Data Flow

```
1. Buyer creates deal → Escrow service generates keypair
2. Escrow returns: {escrowPubkey, scriptHash, dealId}
3. Buyer constructs escrow VTXO tapscript (all 4 leaves)
4. Buyer sends BTC to escrow VTXO via Ark round
5. Seller delivers goods/services
6. Release path:
   a. Happy: Buyer + Seller sign Leaf 0 → instant
   b. Dispute: Escrow signs Leaf 1 (release) or Leaf 2 (refund)
   c. Timeout: Buyer claims Leaf 3 after CSV delay
```

## File Structure (Proposed)

```
ark-escrow/
├── cmd/
│   └── escrow-agent/          # Escrow agent service
│       └── main.go
├── pkg/
│   ├── escrow/
│   │   ├── script.go          # Escrow VTXO tapscript builder
│   │   ├── deal.go            # Deal lifecycle management
│   │   └── signer.go          # Escrow signing logic
│   └── client/
│       └── client.go          # Client SDK for escrow integration
├── internal/
│   ├── api/                   # gRPC + REST API
│   └── store/                 # Deal persistence
├── docs/
├── infra/
└── test/
    └── e2e/                   # End-to-end escrow flow tests
```
