# Test Protocol — Ark Escrow Agent

**Date:** 2026-03-14
**Go Version:** 1.26.0
**Platform:** Linux x86_64
**Result: 42/42 PASS**

## Summary

| Package | Tests | Status | Duration |
|---------|-------|--------|----------|
| `pkg/escrow` (script, keys, signer) | 21 | ALL PASS | 0.017s |
| `internal/store` (SQLite) | 10 | ALL PASS | 0.160s |
| `internal/api` (REST API) | 19 | ALL PASS | 0.259s |
| **Total** | **42** | **ALL PASS** | **0.436s** |

---

## pkg/escrow — Script Builder (7 tests)

| Test | Pathway | Result |
|------|---------|--------|
| `TestNewEscrowVtxoScript` | 4-leaf tapscript creation: 3 MultisigClosure + 1 CSVMultisigClosure | PASS |
| `TestEscrowAddress` | P2TR address generation from tapscript tree (OP_1 + 32-byte key) | PASS |
| `TestEscrowAddressDeterministic` | Same keys → same address (reproducible) | PASS |
| `TestEscrowAddressDifferentKeys` | Different keys → different address (collision resistance) | PASS |
| `TestValidateEscrowScript` | Valid escrow script passes validation | PASS |
| `TestValidateEscrowScriptMissingServer` | Script without server pubkey rejected | PASS |
| `TestEncodeDecodeTapscripts` | Encode → Decode roundtrip preserves address | PASS |

## pkg/escrow — Key Management (8 tests)

| Test | Pathway | Result |
|------|---------|--------|
| `TestGenerateEscrowKeyPair` | Fresh secp256k1 keypair generation | PASS |
| `TestGenerateEscrowKeyPairUniqueness` | Two generated keys differ | PASS |
| `TestEncryptDecryptRoundtrip` | AES-256-GCM encrypt → decrypt preserves key | PASS |
| `TestEncryptDecryptWrongKey` | Decryption with wrong master key fails | PASS |
| `TestEncryptInvalidMasterKeyLength` | Reject non-32-byte master key (too short) | PASS |
| `TestEncryptInvalidMasterKeyLength` | Reject non-32-byte master key (too long) | PASS |
| `TestDecryptInvalidMasterKeyLength` | Reject non-32-byte master key on decrypt | PASS |
| `TestDecryptTruncatedCiphertext` | Reject truncated ciphertext | PASS |
| `TestEncryptedOutputDiffers` | Same key → different ciphertext (random nonce) | PASS |

## pkg/escrow — Signer (6 tests)

| Test | Pathway | Result |
|------|---------|--------|
| `TestSignForLeaf_Release` | Schnorr sign for Leaf 1 (escrow→seller) + verify | PASS |
| `TestSignForLeaf_Refund` | Schnorr sign for Leaf 2 (escrow→buyer) + verify | PASS |
| `TestSignForLeaf_MutualRelease` | Schnorr sign for Leaf 0 (mutual) | PASS |
| `TestSignForLeaf_InvalidLeafIndex` | Reject negative, out-of-range, and extreme indices | PASS |
| `TestSignForLeaf_NilPrivateKey` | Reject signing without private key | PASS |
| `TestGetLeafClosure` | Retrieve closures by index, reject invalid indices | PASS |

## internal/store — SQLite Persistence (10 tests)

| Test | Pathway | Result |
|------|---------|--------|
| `TestCreateAndGetDeal` | Create deal → retrieve → verify all fields + encrypted key | PASS |
| `TestGetDealNotFound` | Query nonexistent deal returns error | PASS |
| `TestUpdateStatus` | Status transition created → funded | PASS |
| `TestUpdateStatusNotFound` | Update nonexistent deal returns error | PASS |
| `TestUpdateStatusLifecycle` | Full lifecycle: created → funded → released | PASS |
| `TestSetVtxoOutpoint` | Set VTXO reference + auto-set status to funded | PASS |
| `TestListDeals` | Filter deals by status, correct counts | PASS |
| `TestDuplicateDealID` | Reject duplicate deal ID (primary key) | PASS |
| `TestNewSQLiteStoreInvalidMasterKey` | Reject non-32-byte master key | PASS |
| `TestScriptRoundtripThroughDB` | Script encode → DB store → DB read → decode → same address | PASS |

## internal/api — REST API (19 tests)

| Test | Pathway | Result |
|------|---------|--------|
| **Create Deal** | | |
| `TestCreateDeal_HappyPath` | POST /deals with valid keys → 201 + deal_id + escrow_pubkey + 4 tapscripts + P2TR address | PASS |
| `TestCreateDeal_ZeroAmount` | POST /deals with amount=0 → 400 | PASS |
| `TestCreateDeal_InvalidBuyerKey` | POST /deals with bad hex → 400 | PASS |
| `TestCreateDeal_InvalidSellerKey` | POST /deals with short hex → 400 | PASS |
| `TestCreateDeal_EmptyBody` | POST /deals with invalid JSON → 400 | PASS |
| `TestCreateDeal_DefaultTimeout` | POST /deals without timeout_blocks → defaults to 144 | PASS |
| **Get Deal** | | |
| `TestGetDeal_HappyPath` | GET /deals/{id} → 200 + all fields | PASS |
| `TestGetDeal_NotFound` | GET /deals/nonexistent → 404 | PASS |
| **Fund Deal** | | |
| `TestFundDeal_HappyPath` | POST /deals/{id}/fund → 200, status=funded | PASS |
| `TestFundDeal_NotFound` | POST /deals/nonexistent/fund → 404 | PASS |
| `TestFundDeal_AlreadyFunded` | POST /deals/{id}/fund twice → 409 conflict | PASS |
| **Release** | | |
| `TestRelease_HappyPath` | POST /deals/{id}/release (funded) → 200, status=released | PASS |
| `TestRelease_NotFunded` | POST /deals/{id}/release (created) → 409 | PASS |
| `TestRelease_AlreadyReleased` | POST /deals/{id}/release twice → 409 | PASS |
| **Refund** | | |
| `TestRefund_HappyPath` | POST /deals/{id}/refund (funded) → 200, status=refunded | PASS |
| `TestRefund_NotFunded` | POST /deals/{id}/refund (created) → 409 | PASS |
| `TestRefund_AfterRelease` | POST /deals/{id}/refund (released) → 409 (can't refund after release) | PASS |
| **Full Lifecycle** | | |
| `TestFullLifecycle_Release` | create → get(created) → fund → get(funded) → release → get(released) | PASS |
| `TestFullLifecycle_Refund` | create → fund → refund → get(refunded) | PASS |

## Test Coverage by Component

| Component | Covered Pathways |
|-----------|-----------------|
| **Tapscript Builder** | 4-leaf creation, P2TR address, determinism, validation, encode/decode |
| **Key Management** | Generation, uniqueness, encrypt/decrypt, wrong key, invalid lengths, nonce randomness |
| **Signer** | All 3 signing leaves (mutual/release/refund), signature verification, error cases |
| **SQLite Store** | CRUD, status transitions, VTXO outpoints, filtering, duplicates, script roundtrip |
| **REST API** | All 5 endpoints, happy paths, validation errors, state conflicts, full lifecycles |

## State Machine Coverage

```
                 ┌──────────┐
                 │ created  │
                 └────┬─────┘
                      │ fund
                 ┌────▼─────┐
                 │  funded   │
                 └──┬────┬──┘
          release │      │ refund
            ┌─────▼─┐  ┌─▼──────┐
            │released│  │refunded│
            └───────┘  └────────┘

Tested transitions:
  ✓ created → funded (TestFundDeal_HappyPath)
  ✓ funded → released (TestRelease_HappyPath)
  ✓ funded → refunded (TestRefund_HappyPath)
  ✓ created → funded → released (TestFullLifecycle_Release)
  ✓ created → funded → refunded (TestFullLifecycle_Refund)

Tested rejections:
  ✓ created → released (TestRelease_NotFunded)
  ✓ created → refunded (TestRefund_NotFunded)
  ✓ funded → funded (TestFundDeal_AlreadyFunded)
  ✓ released → released (TestRelease_AlreadyReleased)
  ✓ released → refunded (TestRefund_AfterRelease)
```
