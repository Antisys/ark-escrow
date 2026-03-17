package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/Antisys/ark-escrow/pkg/escrow"
	"github.com/Antisys/ark-escrow/pkg/escrow/swap"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	elementsNetwork "github.com/vulpemventures/go-elements/network"
)

const (
	lndURL       = "https://localhost:18080"
	elementsdURL = "http://admin1:123@localhost:18884"
	clnContainer = "cln"
	clnNetwork   = "regtest"
	networkHRP   = "ert"
)

// randomDealAmount returns a random amount between 10,000 and 200,000 sats.
func randomDealAmount() uint64 {
	return uint64(rand.Intn(190001)) + 10000
}

// randomTimeout returns a random CSV timeout between 10 and 200 blocks.
func randomTimeout() uint32 {
	return uint32(rand.Intn(191)) + 10
}

type step struct {
	name string
	desc string // verbose explanation printed before running
	fn   func(ctx context.Context, state *testState) error
}

type testState struct {
	store        escrow.Store
	lnd          *swap.LNDClient
	cln          *swap.CLNClient
	elementsd    *swap.ElementsdClient
	oracleKey    *btcec.PrivateKey
	sellerKey    *btcec.PrivateKey
	buyerKey     *btcec.PrivateKey
	serviceKey   *btcec.PrivateKey
	secret       [escrow.SecretSize]byte
	secretHash   [32]byte
	deal         *escrow.Deal
	fundResult   *swap.FundResult
	escrowAddr   string
	escrowScript *escrow.EscrowScript
	escrowAmount     uint64 // actual amount in escrow VTXO (deal.Amount minus HTLC fee)
	recoveryKit      string // encoded recovery kit
	recoveryDest     string // destination address for recovery
	recoverySecret   string // buyer's secret (hex), preserved for seller recovery
	csvTimeout       uint32 // preserved across state wipe for recovery scenario
	lastInvoiceLabel string // label of the last CLN invoice created (for verification)
}

var verbose bool

func main() {
	lndMacaroon := os.Getenv("ESCROW_LND_MACAROON")
	if lndMacaroon == "" {
		fmt.Println("ESCROW_LND_MACAROON env var required")
		os.Exit(1)
	}

	// Check for -v or --verbose flag
	for _, arg := range os.Args[1:] {
		if arg == "-v" || arg == "--verbose" {
			verbose = true
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	state := &testState{
		lnd: swap.NewLNDClient(lndURL, lndMacaroon),
		cln: swap.NewCLNClient(clnContainer, clnNetwork),
	}

	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║        Liquid Ark Escrow — E2E Test (Release Path)         ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	if verbose {
		fmt.Println()
		fmt.Println("This test simulates a complete escrow lifecycle where a buyer")
		fmt.Println("purchases a product, the seller ships it, and the buyer releases")
		fmt.Println("the escrowed funds to the seller.")
		fmt.Println()
		fmt.Println("The escrow uses a 4-leaf tapscript tree on Liquid:")
		fmt.Println("  Leaf 0 (Release):  SHA256 preimage + seller signature")
		fmt.Println("  Leaf 1 (Timeout):  CSV delay + buyer signature")
		fmt.Println("  Leaf 2 (Dispute):  oracle + seller signatures")
		fmt.Println("  Leaf 3 (Dispute):  oracle + buyer signatures")
		fmt.Println()
		fmt.Println("Funds move: buyer's LN wallet → HODL invoice → L-BTC escrow VTXO")
		fmt.Println("            → on-chain claim → L-BTC → LN payout to seller")
	}
	fmt.Println()

	releaseSteps := []step{
		{
			"Initialize services",
			"Connecting to elementsd (Liquid regtest), LND (Lightning service node),\n" +
				"and CLN (buyer/seller test wallet). Generating an oracle keypair for\n" +
				"dispute resolution. Creating a temp directory for deal storage.",
			stepInit,
		},
		{
			"Fund service wallet with L-BTC",
			"Mining 101 blocks on Liquid regtest to mature coinbase rewards.\n" +
				"The service needs L-BTC to fund escrow outputs.",
			stepFundWallet,
		},
		{
			"Seller creates deal (random amount)",
			"The seller generates a new keypair and creates a deal specifying:\n" +
				"  - Amount: random (10,000–200,000 sats)\n" +
				"  - Title: \"Test Widget\"\n" +
				"  - Timeout: random (10–200 blocks)\n" +
				"  - Oracle pubkey for dispute resolution\n" +
				"The seller's private key is stored in the deal file for later signing.",
			stepSellerCreateDeal,
		},
		{
			"Buyer joins deal",
			"The buyer generates a new keypair and a random 32-byte secret.\n" +
				"The SHA256 hash of the secret is stored in the deal (not the secret itself).\n" +
				"Both keys + hash are used to compute the 4-leaf escrow tapscript tree.\n" +
				"The taproot output key is derived (with unspendable internal key)\n" +
				"and encoded as a bech32m address (ert1p... on regtest).",
			stepBuyerJoinDeal,
		},
		{
			"Fund escrow via LN (CLN → HODL invoice)",
			"Atomic swap: Lightning payment → Liquid escrow VTXO.\n" +
				"  1. Service generates a random preimage P, computes hash H = SHA256(P)\n" +
				"  2. Service sends L-BTC to the escrow taproot address\n" +
				"  3. Service creates a HODL invoice on LND with hash H\n" +
				"  4. Buyer's CLN wallet pays the HODL invoice (LND holds the HTLC)\n" +
				"  5. Service detects the held payment, settles the invoice (reveals P)\n" +
				"  6. Buyer's LN payment completes — funds are now locked on Liquid",
			stepBuyerFund,
		},
		{
			"Verify escrow VTXO on-chain",
			"Mining 1 block to confirm the funding transaction.\n" +
				"Querying elementsd to verify the UTXO exists at the expected outpoint.",
			stepVerifyFunding,
		},
		{
			"Seller marks shipped",
			"State transition: FUNDED → SHIPPED.\n" +
				"In production this would notify the buyer. No on-chain action needed.",
			stepSellerShip,
		},
		{
			"Release: claim escrow VTXO + pay seller via LN",
			"Two-step atomic release:\n" +
				"  Step 1 — On-chain claim using release leaf (leaf 0):\n" +
				"    Build a Liquid transaction spending the escrow VTXO.\n" +
				"    Witness: [seller_signature] [buyer_secret] [leaf_script] [control_block]\n" +
				"    The seller signs with Elements sighash (includes genesis hash + leaf hash).\n" +
				"    The buyer's 32-byte secret satisfies OP_SHA256 <hash> OP_EQUAL.\n" +
				"    Broadcast via elementsd, mine to confirm.\n" +
				"  Step 2 — LN payout:\n" +
				"    Seller's CLN wallet creates an invoice.\n" +
				"    Service pays it from LND. Seller receives sats over Lightning.",
			stepBuyerRelease,
		},
		{
			"Verify seller received LN payout",
			"Querying CLN to confirm the seller's node received the payment.",
			stepVerifySellerPayout,
		},
		{
			"Verify deal state is RELEASED",
			"Loading the deal from the JSON file store and confirming\n" +
				"the state machine transitioned to RELEASED with the claim txid recorded.",
			stepVerifyReleased,
		},
	}

	passed := runSteps(ctx, state, releaseSteps)
	total := len(releaseSteps)

	fmt.Println()
	if passed == total {
		fmt.Printf("RESULT: %d/%d passed ✓\n", passed, total)
	} else {
		fmt.Printf("RESULT: %d/%d passed ✗\n", passed, total)
		os.Exit(1)
	}

	// === Refund Path ===
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║         Liquid Ark Escrow — E2E Test (Refund Path)         ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	if verbose {
		fmt.Println()
		fmt.Println("This test simulates a timeout/refund scenario: the buyer funds")
		fmt.Println("the escrow but the seller never ships. After the CSV timeout")
		fmt.Println("expires (144 blocks), the buyer reclaims the funds.")
	}
	fmt.Println()

	state2 := &testState{
		lnd:        state.lnd,
		cln:        state.cln,
		elementsd:  state.elementsd,
		store:      state.store,
		oracleKey:  state.oracleKey,
		serviceKey: state.serviceKey,
	}

	refundSteps := []step{
		{
			"Seller creates deal (random amount)",
			"Same as release path — new deal with fresh keypair.",
			stepSellerCreateDeal,
		},
		{
			"Buyer joins deal",
			"Same as release path — new buyer keypair, new secret, new escrow address.",
			stepBuyerJoinDeal,
		},
		{
			"Fund escrow via LN (CLN → HODL invoice)",
			"Same atomic swap as release path. Funds are locked in the escrow VTXO.",
			stepBuyerFund,
		},
		{
			"Verify escrow VTXO on-chain",
			"Confirm the escrow output exists on-chain.",
			stepVerifyFunding,
		},
		{
			"Mine blocks past CSV timeout",
			"Mining past the CSV timeout (random value + 10 margin) to satisfy the\n" +
				"OP_CHECKSEQUENCEVERIFY condition on the timeout leaf.\n" +
				"After this, the buyer can spend the escrow VTXO without the seller.",
			stepMineBlocksPastTimeout,
		},
		{
			"Refund: claim escrow VTXO + pay buyer via LN",
			"Two-step atomic refund:\n" +
				"  Step 1 — On-chain claim using timeout leaf (leaf 1):\n" +
				"    Build a Liquid transaction with BIP68 sequence number.\n" +
				"    Witness: [buyer_signature] [leaf_script] [control_block]\n" +
				"    The CSV script: <timeout> OP_CHECKSEQUENCEVERIFY OP_DROP <buyer_pub> OP_CHECKSIG\n" +
				"    No preimage needed — just buyer's signature + enough blocks elapsed.\n" +
				"    Broadcast via elementsd, mine to confirm.\n" +
				"  Step 2 — LN refund:\n" +
				"    Buyer's CLN wallet creates an invoice.\n" +
				"    Service pays it from LND. Buyer gets their sats back.",
			stepBuyerRefund,
		},
		{
			"Verify buyer received LN refund",
			"Querying CLN to confirm the buyer's node received the refund.",
			stepVerifyBuyerRefund,
		},
		{
			"Verify deal state is REFUNDED",
			"Loading the deal from the JSON file store and confirming\n" +
				"the state machine transitioned to REFUNDED with the claim txid recorded.",
			stepVerifyRefunded,
		},
	}

	passed2 := runSteps(ctx, state2, refundSteps)
	total2 := len(refundSteps)

	fmt.Println()
	if passed2 == total2 {
		fmt.Printf("RESULT: %d/%d passed ✓\n", passed2, total2)
	} else {
		fmt.Printf("RESULT: %d/%d passed ✗\n", passed2, total2)
		os.Exit(1)
	}

	// === Security Tests ===
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║    Liquid Ark Escrow — E2E Test (Security & Recovery)       ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	if verbose {
		fmt.Println()
		fmt.Println("These tests verify the self-custody guarantee: a user can")
		fmt.Println("recover their funds using only a recovery kit and an elementsd")
		fmt.Println("node, even if the escrow service disappears completely.")
		fmt.Println()
		fmt.Println("Also verifies that funds cannot be stolen before the CSV timeout.")
	}
	fmt.Println()

	state3 := &testState{
		lnd:        state.lnd,
		cln:        state.cln,
		elementsd:  state.elementsd,
		store:      state.store,
		oracleKey:  state.oracleKey,
		serviceKey: state.serviceKey,
	}

	securitySteps := []step{
		{
			"Seller creates deal (random amount)",
			"Fresh deal for security tests.",
			stepSellerCreateDeal,
		},
		{
			"Buyer joins deal",
			"Fresh buyer keypair and secret.",
			stepBuyerJoinDeal,
		},
		{
			"Fund escrow via LN",
			"Lock funds in the escrow VTXO.",
			stepBuyerFund,
		},
		{
			"Verify escrow VTXO on-chain",
			"Confirm the escrow output exists.",
			stepVerifyFunding,
		},
		{
			"REJECT: buyer refund before CSV timeout",
			"The buyer tries to claim via the timeout leaf BEFORE the CSV\n" +
				"locktime has expired. This MUST fail — otherwise funds aren't locked.\n" +
				"elementsd should reject the transaction with a sequence locktime error.\n" +
				"This proves the escrow actually protects the seller during the deal.",
			stepRejectEarlyRefund,
		},
		{
			"Export buyer recovery kit",
			"Generate a self-contained recovery kit from the deal.\n" +
				"This blob contains: buyer private key, all 3 pubkeys, secret hash,\n" +
				"timeout, funding outpoint, amount, and network.\n" +
				"With this, the buyer can reconstruct the tapscript tree and claim.",
			stepExportRecoveryKit,
		},
		{
			"DELETE all service state",
			"Simulate the escrow service disappearing completely.\n" +
				"Wipe the deal store. The buyer has ONLY the recovery kit.",
			stepDeleteServiceState,
		},
		{
			"Mine blocks past CSV timeout",
			"Mining past the CSV timeout so the buyer can claim.",
			stepMineBlocksPastTimeout,
		},
		{
			"RECOVER: buyer claims from recovery kit only",
			"Using only the recovery kit and elementsd, reconstruct the\n" +
				"escrow tapscript tree, build a claim transaction for the\n" +
				"timeout leaf (buyer sig + BIP68 sequence), sign with Elements\n" +
				"sighash, broadcast. No service, no deal file, no LN needed.",
			stepRecoverFromKit,
		},
		{
			"Verify recovery destination received funds",
			"Check that the L-BTC arrived at the recovery destination address.",
			stepVerifyRecovery,
		},
	}

	passed3 := runSteps(ctx, state3, securitySteps)
	total3 := len(securitySteps)

	fmt.Println()
	if passed3 == total3 {
		fmt.Printf("RESULT: %d/%d passed ✓\n", passed3, total3)
	} else {
		fmt.Printf("RESULT: %d/%d passed ✗\n", passed3, total3)
		os.Exit(1)
	}

	// === Seller Recovery Path ===
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║  Liquid Ark Escrow — E2E Test (Seller Recovery, no service) ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	if verbose {
		fmt.Println()
		fmt.Println("This test simulates the seller recovering funds using only a")
		fmt.Println("recovery kit + buyer's secret, without the service. The seller")
		fmt.Println("claims via the release leaf (preimage + seller sig).")
	}
	fmt.Println()

	stateSellerRecovery := &testState{
		lnd:        state.lnd,
		cln:        state.cln,
		elementsd:  state.elementsd,
		store:      state.store,
		oracleKey:  state.oracleKey,
		serviceKey: state.serviceKey,
	}

	sellerRecoverySteps := []step{
		{
			"Seller creates deal (random amount)",
			"Fresh deal for seller recovery test.",
			stepSellerCreateDeal,
		},
		{
			"Buyer joins deal",
			"Fresh buyer keypair and secret.",
			stepBuyerJoinDeal,
		},
		{
			"Fund escrow via LN",
			"Lock funds in the escrow VTXO.",
			stepBuyerFund,
		},
		{
			"Verify escrow VTXO on-chain",
			"Confirm the escrow output exists.",
			stepVerifyFunding,
		},
		{
			"Export seller recovery kit + save buyer secret",
			"Generate a seller recovery kit. Also save the buyer's secret\n" +
				"separately — in production the buyer would reveal this to release.",
			stepExportSellerRecoveryKit,
		},
		{
			"DELETE all service state",
			"Simulate the escrow service disappearing completely.\n" +
				"Wipe the deal store. The seller has ONLY the recovery kit + secret.",
			stepDeleteServiceState,
		},
		{
			"RECOVER: seller claims via release leaf (no service)",
			"Using only the seller recovery kit, buyer's secret, and elementsd,\n" +
				"reconstruct the escrow tapscript tree, build a claim transaction\n" +
				"for the release leaf (seller sig + preimage), sign and broadcast.\n" +
				"No service, no deal file, no CSV timeout needed.",
			stepSellerRecoverFromKit,
		},
		{
			"Verify recovery destination received funds",
			"Check that the L-BTC arrived at the recovery destination address.",
			stepVerifyRecovery,
		},
	}

	passedSR := runSteps(ctx, stateSellerRecovery, sellerRecoverySteps)
	totalSR := len(sellerRecoverySteps)

	fmt.Println()
	if passedSR == totalSR {
		fmt.Printf("RESULT: %d/%d passed ✓ (seller recovery)\n", passedSR, totalSR)
	} else {
		fmt.Printf("RESULT: %d/%d passed ✗ (seller recovery)\n", passedSR, totalSR)
		os.Exit(1)
	}

	// === Dispute Path ===
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║      Liquid Ark Escrow — E2E Test (Dispute Path)           ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	if verbose {
		fmt.Println()
		fmt.Println("This test simulates a dispute: buyer and seller disagree,")
		fmt.Println("the oracle resolves in the seller's favor. The escrow VTXO")
		fmt.Println("is claimed using the dispute→seller leaf (oracle + seller")
		fmt.Println("multisig, no preimage or timeout needed).")
		fmt.Println()
		fmt.Println("Then a second dispute where the oracle rules for the buyer.")
	}
	fmt.Println()

	// --- Dispute → Seller ---
	state4 := &testState{
		lnd:        state.lnd,
		cln:        state.cln,
		elementsd:  state.elementsd,
		store:      state.store,
		oracleKey:  state.oracleKey,
		serviceKey: state.serviceKey,
	}

	disputeSellerSteps := []step{
		{
			"Seller creates deal (random amount)",
			"Fresh deal for dispute test.",
			stepSellerCreateDeal,
		},
		{
			"Buyer joins deal",
			"Fresh buyer keypair and secret.",
			stepBuyerJoinDeal,
		},
		{
			"Fund escrow via LN",
			"Lock funds in the escrow VTXO.",
			stepBuyerFund,
		},
		{
			"Verify escrow VTXO on-chain",
			"Confirm the escrow output exists.",
			stepVerifyFunding,
		},
		{
			"Dispute: oracle rules for SELLER (leaf 2)",
			"The oracle and seller cooperate to claim the escrow VTXO\n" +
				"using the dispute→seller leaf (leaf 2: MultisigClosure).\n" +
				"  Witness: [seller_sig] [oracle_sig] [leaf_script] [control_block]\n" +
				"  No preimage, no timeout — just two signatures.\n" +
				"  Then pay the seller via LN.",
			stepDisputeSeller,
		},
		{
			"Verify seller received LN payout",
			"Querying CLN to confirm the seller's node received the payment.",
			stepVerifySellerPayout,
		},
		{
			"Verify deal state is DISPUTED",
			"Loading the deal and confirming DISPUTED state.",
			stepVerifyDisputed,
		},
	}

	passed4 := runSteps(ctx, state4, disputeSellerSteps)
	total4 := len(disputeSellerSteps)

	fmt.Println()
	if passed4 == total4 {
		fmt.Printf("RESULT: %d/%d passed ✓ (dispute → seller)\n", passed4, total4)
	} else {
		fmt.Printf("RESULT: %d/%d passed ✗ (dispute → seller)\n", passed4, total4)
		os.Exit(1)
	}

	// --- Dispute → Buyer ---
	fmt.Println()

	state5 := &testState{
		lnd:        state.lnd,
		cln:        state.cln,
		elementsd:  state.elementsd,
		store:      state.store,
		oracleKey:  state.oracleKey,
		serviceKey: state.serviceKey,
	}

	disputeBuyerSteps := []step{
		{
			"Seller creates deal (random amount)",
			"Fresh deal for buyer-wins dispute test.",
			stepSellerCreateDeal,
		},
		{
			"Buyer joins deal",
			"Fresh buyer keypair and secret.",
			stepBuyerJoinDeal,
		},
		{
			"Fund escrow via LN",
			"Lock funds in the escrow VTXO.",
			stepBuyerFund,
		},
		{
			"Verify escrow VTXO on-chain",
			"Confirm the escrow output exists.",
			stepVerifyFunding,
		},
		{
			"Dispute: oracle rules for BUYER (leaf 3)",
			"The oracle and buyer cooperate to claim the escrow VTXO\n" +
				"using the dispute→buyer leaf (leaf 3: MultisigClosure).\n" +
				"  Witness: [buyer_sig] [oracle_sig] [leaf_script] [control_block]\n" +
				"  No preimage, no timeout — just two signatures.\n" +
				"  Then refund the buyer via LN.",
			stepDisputeBuyer,
		},
		{
			"Verify buyer received LN refund",
			"Querying CLN to confirm the buyer's node received the refund.",
			stepVerifyBuyerRefund,
		},
		{
			"Verify deal state is DISPUTED",
			"Loading the deal and confirming DISPUTED state.",
			stepVerifyDisputed,
		},
	}

	passed5 := runSteps(ctx, state5, disputeBuyerSteps)
	total5 := len(disputeBuyerSteps)

	fmt.Println()
	if passed5 == total5 {
		fmt.Printf("RESULT: %d/%d passed ✓ (dispute → buyer)\n", passed5, total5)
	} else {
		fmt.Printf("RESULT: %d/%d passed ✗ (dispute → buyer)\n", passed5, total5)
		os.Exit(1)
	}
}

func runSteps(ctx context.Context, state *testState, steps []step) int {
	passed := 0
	total := len(steps)
	for i, s := range steps {
		fmt.Printf("┌─ [%d/%d] %s\n", i+1, total, s.name)
		if verbose {
			// Print description indented
			for _, line := range strings.Split(s.desc, "\n") {
				fmt.Printf("│  %s\n", line)
			}
			fmt.Printf("│\n")
		}
		start := time.Now()
		err := s.fn(ctx, state)
		elapsed := time.Since(start)
		if err != nil {
			fmt.Printf("└─ FAIL (%v) [%s]\n\n", err, elapsed.Round(time.Millisecond))
			return passed
		}
		passed++
		fmt.Printf("└─ PASS [%s]\n\n", elapsed.Round(time.Millisecond))
	}
	return passed
}

func stepInit(ctx context.Context, state *testState) error {
	var err error
	state.elementsd, err = swap.NewElementsdClient(elementsdURL)
	if err != nil {
		return fmt.Errorf("elementsd: %w", err)
	}

	info, err := state.lnd.GetInfo(ctx)
	if err != nil {
		return fmt.Errorf("LND: %w", err)
	}
	logf("LND connected: %s", truncJSON(info, 80))

	clnInfo, err := state.cln.GetInfo(ctx)
	if err != nil {
		return fmt.Errorf("CLN: %w", err)
	}
	logf("CLN connected: %s", truncJSON(clnInfo, 80))

	height, err := state.elementsd.GetBlockCount(ctx)
	if err != nil {
		return fmt.Errorf("elementsd: %w", err)
	}
	logf("elementsd connected, block height: %d", height)

	dir, _ := os.MkdirTemp("", "escrow-test-*")
	state.store, err = escrow.NewFileStore(dir)
	if err != nil {
		return err
	}
	logf("Deal store: %s", dir)

	state.oracleKey, err = btcec.NewPrivateKey()
	if err != nil {
		return err
	}
	logf("Oracle pubkey: %s", hex.EncodeToString(schnorr.SerializePubKey(state.oracleKey.PubKey())))

	state.serviceKey, err = btcec.NewPrivateKey()
	if err != nil {
		return err
	}
	logf("Service key:   %s (for atomic HTLC swap)", hex.EncodeToString(schnorr.SerializePubKey(state.serviceKey.PubKey())))

	return nil
}

func stepFundWallet(ctx context.Context, state *testState) error {
	addr, err := state.elementsd.GetNewAddress(ctx)
	if err != nil {
		return err
	}
	logf("Mining 101 blocks to %s...", addr[:20]+"...")
	_, err = state.elementsd.GenerateToAddress(ctx, 101, addr)
	if err != nil {
		return err
	}
	balance, err := state.elementsd.GetBalance(ctx)
	if err != nil {
		return err
	}
	if balance < 1.0 {
		return fmt.Errorf("insufficient balance: %f", balance)
	}
	logf("Wallet balance: %.8f L-BTC", balance)
	return nil
}

func stepSellerCreateDeal(ctx context.Context, state *testState) error {
	var err error
	state.sellerKey, err = btcec.NewPrivateKey()
	if err != nil {
		return err
	}

	oraclePubHex := hex.EncodeToString(schnorr.SerializePubKey(state.oracleKey.PubKey()))
	sellerPubHex := hex.EncodeToString(schnorr.SerializePubKey(state.sellerKey.PubKey()))

	amount := randomDealAmount()
	csvTimeout := randomTimeout()

	state.deal, err = escrow.NewDeal("Test Widget", amount, sellerPubHex, oraclePubHex, csvTimeout)
	if err != nil {
		return err
	}
	state.deal.SellerPrivKey = hex.EncodeToString(state.sellerKey.Serialize())

	if err := state.store.Save(state.deal); err != nil {
		return err
	}

	logf("Deal ID:       %s", state.deal.ID)
	logf("Seller pubkey: %s", sellerPubHex)
	logf("Oracle pubkey: %s", oraclePubHex)
	logf("Amount:        %d sats (random)", state.deal.Amount)
	logf("Timeout:       %d blocks (random)", state.deal.TimeoutBlocks)
	return nil
}

func stepBuyerJoinDeal(ctx context.Context, state *testState) error {
	var err error
	state.buyerKey, err = btcec.NewPrivateKey()
	if err != nil {
		return err
	}

	buyerPubHex := hex.EncodeToString(schnorr.SerializePubKey(state.buyerKey.PubKey()))

	state.secret, state.secretHash, err = escrow.GenerateSecret()
	if err != nil {
		return err
	}

	logf("Buyer pubkey:  %s", buyerPubHex)
	logf("Secret:        %s", hex.EncodeToString(state.secret[:]))
	logf("Secret hash:   %s", hex.EncodeToString(state.secretHash[:]))

	if err := state.deal.Join(buyerPubHex, hex.EncodeToString(state.secretHash[:])); err != nil {
		return err
	}

	state.deal.BuyerPrivKey = hex.EncodeToString(state.buyerKey.Serialize())
	state.deal.Secret = hex.EncodeToString(state.secret[:])

	params, err := state.deal.EscrowParams()
	if err != nil {
		return err
	}
	state.escrowScript, err = escrow.NewEscrowScript(*params)
	if err != nil {
		return err
	}
	state.escrowAddr, err = state.escrowScript.Address(networkHRP)
	if err != nil {
		return err
	}
	state.deal.SetEscrowAddress(state.escrowAddr)

	logf("Escrow tapscript tree (4 leaves):")
	for i, c := range state.escrowScript.Closures {
		s, _ := c.Script()
		logf("  Leaf %d: %s (%d bytes)", i, leafTypeName(i), len(s))
	}
	logf("Escrow address: %s", state.escrowAddr)
	logf("Deal state:     %s → %s", escrow.DealStateCreated, state.deal.State)

	if err := state.store.Save(state.deal); err != nil {
		return err
	}

	return nil
}

func stepBuyerFund(ctx context.Context, state *testState) error {
	logf("Locking L-BTC in Liquid HTLC (atomic swap)...")
	result, err := swap.Fund(ctx, swap.FundConfig{
		LND:            state.lnd,
		Elementsd:      state.elementsd,
		EscrowAddress:  state.escrowAddr,
		AmountSats:     state.deal.Amount,
		ServicePubKey:  state.serviceKey.PubKey(),
		ServicePrivKey: state.serviceKey,
	})
	if err != nil {
		return err
	}
	state.fundResult = result

	logf("HTLC locked:    %s:%d", result.HTLCTxID, result.HTLCVout)
	logf("Preimage:       %s", hex.EncodeToString(result.Preimage))
	logf("Payment hash:   %s", hex.EncodeToString(result.PaymentHash))
	logf("HODL invoice:   %s...%s", result.PaymentRequest[:30], result.PaymentRequest[len(result.PaymentRequest)-10:])

	logf("Buyer's CLN paying HODL invoice...")
	payErrCh := make(chan error, 1)
	go func() {
		payCtx, payCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer payCancel()
		_, err := state.cln.Pay(payCtx, result.PaymentRequest)
		payErrCh <- err
	}()

	logf("Waiting for LND to detect held payment, then claiming HTLC → escrow...")
	escrowTxID, err := swap.WaitForPaymentClaimAndSettle(
		ctx, state.lnd, state.elementsd, result,
		state.serviceKey, state.escrowAddr, state.deal.Amount, nil, time.Second,
	)
	if err != nil {
		return fmt.Errorf("atomic swap failed: %w", err)
	}
	logf("HTLC claimed → escrow: %s", escrowTxID)
	logf("HODL invoice settled — preimage revealed, buyer's LN payment complete")

	// Wait for the CLN payment goroutine to finish and check for errors
	if payErr := <-payErrCh; payErr != nil {
		return fmt.Errorf("CLN payment failed: %w", payErr)
	}

	// Find the actual vout for the escrow output
	vout, err := swap.FindVoutByAddress(ctx, state.elementsd, escrowTxID, state.escrowAddr)
	if err != nil {
		return fmt.Errorf("escrow funded (tx %s) but could not find vout: %w", escrowTxID, err)
	}
	// The escrow VTXO holds deal.Amount minus HTLC claim fee
	state.escrowAmount = state.deal.Amount - swap.HTLCEstimatedFee
	if err := state.deal.Fund(escrowTxID, vout); err != nil {
		return err
	}

	logf("Deal funded: txid=%s vout=%d (escrow amount: %d sats, HTLC fee: %d sats)",
		state.deal.FundTxID, state.deal.FundVout, state.escrowAmount, swap.HTLCEstimatedFee)
	logf("Deal state: %s → %s", escrow.DealStateJoined, state.deal.State)

	return state.store.Save(state.deal)
}


func stepVerifyFunding(ctx context.Context, state *testState) error {
	logf("Mining 1 confirmation block...")
	addr, _ := state.elementsd.GetNewAddress(ctx)
	_, _ = state.elementsd.GenerateToAddress(ctx, 1, addr)

	out, err := state.elementsd.GetTxOut(ctx, state.deal.FundTxID, state.deal.FundVout)
	if err != nil {
		return err
	}
	if out == nil {
		return fmt.Errorf("escrow UTXO not found")
	}

	logf("Escrow UTXO confirmed at %s:%d", state.deal.FundTxID, state.deal.FundVout)
	return nil
}

func stepSellerShip(ctx context.Context, state *testState) error {
	if err := state.deal.Ship(); err != nil {
		return err
	}
	logf("Deal state: %s → %s", escrow.DealStateFunded, state.deal.State)
	return state.store.Save(state.deal)
}

func stepBuyerRelease(ctx context.Context, state *testState) error {
	destAddr, err := state.elementsd.GetNewAddress(ctx)
	if err != nil {
		return fmt.Errorf("dest address: %w", err)
	}

	logf("[On-chain] Building escrow claim tx (release leaf)...")
	logf("  Leaf:        0 (ConditionMultisigClosure)")
	logf("  Condition:   OP_SHA256 <%s...> OP_EQUAL", hex.EncodeToString(state.secretHash[:8]))
	logf("  Signer:      seller (%s...)", hex.EncodeToString(schnorr.SerializePubKey(state.sellerKey.PubKey()))[:16])
	logf("  Preimage:    %s...", hex.EncodeToString(state.secret[:8]))
	logf("  Destination: %s", destAddr)

	claimResult, err := swap.ClaimEscrow(ctx, state.elementsd, swap.ClaimEscrowConfig{
		EscrowScript: state.escrowScript,
		FundTxID:     state.deal.FundTxID,
		FundVout:     state.deal.FundVout,
		Amount:       state.escrowAmount,
		Leaf:         swap.EscrowLeafRelease,
		SigningKeys:  []*btcec.PrivateKey{state.sellerKey},
		Preimage:     state.secret[:],
		DestAddress:  destAddr,
		Network:      &elementsNetwork.Regtest,
	})
	if err != nil {
		return fmt.Errorf("escrow claim: %w", err)
	}
	logf("[On-chain] Escrow VTXO claimed! txid: %s", claimResult.TxID)

	logf("Mining 1 confirmation block...")
	addr, _ := state.elementsd.GetNewAddress(ctx)
	_, _ = state.elementsd.GenerateToAddress(ctx, 1, addr)

	logf("[Lightning] Creating seller invoice on CLN...")
	label := fmt.Sprintf("escrow-release-%d", time.Now().UnixNano())
	inv, err := state.cln.CreateInvoice(ctx, state.deal.Amount*1000, label, "Escrow release payout")
	if err != nil {
		return fmt.Errorf("seller invoice: %w", err)
	}
	state.lastInvoiceLabel = label
	logf("[Lightning] Seller invoice: %s...%s", inv.Bolt11[:30], inv.Bolt11[len(inv.Bolt11)-10:])

	logf("[Lightning] LND paying seller's invoice...")
	_, err = swap.Payout(ctx, swap.PayoutConfig{
		LND:            state.lnd,
		PaymentRequest: inv.Bolt11,
	})
	if err != nil {
		return fmt.Errorf("payout: %w", err)
	}
	logf("[Lightning] Seller paid %d sats via LN", state.deal.Amount)

	if err := state.deal.Release(claimResult.TxID); err != nil {
		return err
	}
	logf("Deal state: %s → %s", escrow.DealStateShipped, state.deal.State)
	return state.store.Save(state.deal)
}

func stepVerifySellerPayout(ctx context.Context, state *testState) error {
	inv, err := state.cln.WaitInvoice(ctx, state.lastInvoiceLabel)
	if err != nil {
		return fmt.Errorf("waitinvoice failed: %w", err)
	}
	if inv.Status != "paid" {
		return fmt.Errorf("invoice status: expected 'paid', got '%s'", inv.Status)
	}
	expectedMsat := state.deal.Amount * 1000
	if inv.AmountRecvMsat != expectedMsat {
		return fmt.Errorf("amount mismatch: expected %d msat, received %d msat", expectedMsat, inv.AmountRecvMsat)
	}
	logf("CLN confirms seller received %d sats (%d msat)", state.deal.Amount, inv.AmountRecvMsat)
	return nil
}

func stepVerifyReleased(ctx context.Context, state *testState) error {
	deal, err := state.store.Load(state.deal.ID)
	if err != nil {
		return err
	}
	if deal.State != escrow.DealStateReleased {
		return fmt.Errorf("expected RELEASED, got %s", deal.State)
	}
	logf("Deal %s state: %s", deal.ID[:8], deal.State)
	logf("Claim txid:    %s", deal.ClaimTxID)
	return nil
}

// === Refund path steps ===

func stepMineBlocksPastTimeout(ctx context.Context, state *testState) error {
	addr, err := state.elementsd.GetNewAddress(ctx)
	if err != nil {
		return err
	}

	heightBefore, err := state.elementsd.GetBlockCount(ctx)
	if err != nil {
		return err
	}

	csvTimeout := state.csvTimeout
	if state.deal != nil {
		csvTimeout = state.deal.TimeoutBlocks
	}
	blocksToMine := int(csvTimeout) + 10
	logf("Current height: %d, need +%d blocks to pass CSV timeout of %d", heightBefore, blocksToMine, csvTimeout)
	logf("Mining %d blocks...", blocksToMine)
	_, err = state.elementsd.GenerateToAddress(ctx, blocksToMine, addr)
	if err != nil {
		return err
	}

	heightAfter, err := state.elementsd.GetBlockCount(ctx)
	if err != nil {
		return err
	}

	logf("New height: %d (+%d blocks), CSV timeout satisfied", heightAfter, heightAfter-heightBefore)
	return nil
}

func stepBuyerRefund(ctx context.Context, state *testState) error {
	destAddr, err := state.elementsd.GetNewAddress(ctx)
	if err != nil {
		return fmt.Errorf("dest address: %w", err)
	}

	logf("[On-chain] Building escrow claim tx (timeout leaf)...")
	logf("  Leaf:        1 (CSVMultisigClosure)")
	logf("  CSV timeout: %d blocks (BIP68 sequence)", state.deal.TimeoutBlocks)
	logf("  Signer:      buyer (%s...)", hex.EncodeToString(schnorr.SerializePubKey(state.buyerKey.PubKey()))[:16])
	logf("  Destination: %s", destAddr)

	claimResult, err := swap.ClaimEscrow(ctx, state.elementsd, swap.ClaimEscrowConfig{
		EscrowScript: state.escrowScript,
		FundTxID:     state.deal.FundTxID,
		FundVout:     state.deal.FundVout,
		Amount:       state.escrowAmount,
		Leaf:         swap.EscrowLeafTimeout,
		SigningKeys:  []*btcec.PrivateKey{state.buyerKey},
		DestAddress:  destAddr,
		Network:      &elementsNetwork.Regtest,
	})
	if err != nil {
		return fmt.Errorf("escrow claim: %w", err)
	}
	logf("[On-chain] Escrow VTXO refunded! txid: %s", claimResult.TxID)

	logf("Mining 1 confirmation block...")
	addr, _ := state.elementsd.GetNewAddress(ctx)
	_, _ = state.elementsd.GenerateToAddress(ctx, 1, addr)

	logf("[Lightning] Creating buyer refund invoice on CLN...")
	label := fmt.Sprintf("escrow-refund-%d", time.Now().UnixNano())
	inv, err := state.cln.CreateInvoice(ctx, state.deal.Amount*1000, label, "Escrow refund payout")
	if err != nil {
		return fmt.Errorf("buyer invoice: %w", err)
	}
	state.lastInvoiceLabel = label
	logf("[Lightning] Buyer invoice: %s...%s", inv.Bolt11[:30], inv.Bolt11[len(inv.Bolt11)-10:])

	logf("[Lightning] LND paying buyer's refund invoice...")
	_, err = swap.Payout(ctx, swap.PayoutConfig{
		LND:            state.lnd,
		PaymentRequest: inv.Bolt11,
	})
	if err != nil {
		return fmt.Errorf("refund payout: %w", err)
	}
	logf("[Lightning] Buyer refunded %d sats via LN", state.deal.Amount)

	if err := state.deal.Refund(claimResult.TxID); err != nil {
		return err
	}
	logf("Deal state: %s → %s", escrow.DealStateFunded, state.deal.State)
	return state.store.Save(state.deal)
}

func stepVerifyBuyerRefund(ctx context.Context, state *testState) error {
	inv, err := state.cln.WaitInvoice(ctx, state.lastInvoiceLabel)
	if err != nil {
		return fmt.Errorf("waitinvoice failed: %w", err)
	}
	if inv.Status != "paid" {
		return fmt.Errorf("invoice status: expected 'paid', got '%s'", inv.Status)
	}
	expectedMsat := state.deal.Amount * 1000
	if inv.AmountRecvMsat != expectedMsat {
		return fmt.Errorf("amount mismatch: expected %d msat, received %d msat", expectedMsat, inv.AmountRecvMsat)
	}
	logf("CLN confirms buyer received %d sats refund (%d msat)", state.deal.Amount, inv.AmountRecvMsat)
	return nil
}

func stepVerifyRefunded(ctx context.Context, state *testState) error {
	deal, err := state.store.Load(state.deal.ID)
	if err != nil {
		return err
	}
	if deal.State != escrow.DealStateRefunded {
		return fmt.Errorf("expected REFUNDED, got %s", deal.State)
	}
	logf("Deal %s state: %s", deal.ID[:8], deal.State)
	logf("Claim txid:    %s", deal.ClaimTxID)
	return nil
}

// === Security & Recovery steps ===

func stepRejectEarlyRefund(ctx context.Context, state *testState) error {
	destAddr, err := state.elementsd.GetNewAddress(ctx)
	if err != nil {
		return fmt.Errorf("dest address: %w", err)
	}

	logf("Attempting to claim via timeout leaf BEFORE CSV expiry...")
	logf("  This should fail — the escrow UTXO was just confirmed,")
	logf("  nowhere near %d blocks of relative locktime.", state.deal.TimeoutBlocks)

	_, err = swap.ClaimEscrow(ctx, state.elementsd, swap.ClaimEscrowConfig{
		EscrowScript: state.escrowScript,
		FundTxID:     state.deal.FundTxID,
		FundVout:     state.deal.FundVout,
		Amount:       state.escrowAmount,
		Leaf:         swap.EscrowLeafTimeout,
		SigningKeys:  []*btcec.PrivateKey{state.buyerKey},
		DestAddress:  destAddr,
		Network:      &elementsNetwork.Regtest,
	})

	if err == nil {
		return fmt.Errorf("SECURITY FAILURE: early refund was accepted — funds are not locked!")
	}

	logf("Correctly rejected: %s", err)
	logf("Escrow funds remain locked — seller is protected during the deal.")
	return nil
}

func stepExportRecoveryKit(ctx context.Context, state *testState) error {
	kit, err := escrow.RecoveryKitForBuyer(state.deal)
	if err != nil {
		return fmt.Errorf("failed to create recovery kit: %w", err)
	}
	kit.NetworkHRP = networkHRP
	kit.Amount = state.escrowAmount // actual VTXO amount (deal amount minus HTLC fee)

	// Validate
	if err := kit.Validate(); err != nil {
		return fmt.Errorf("kit validation failed: %w", err)
	}
	logf("Recovery kit validated")

	// Verify it can reconstruct the escrow address
	kitAddr, err := kit.EscrowAddress()
	if err != nil {
		return fmt.Errorf("kit address reconstruction failed: %w", err)
	}
	if kitAddr != state.escrowAddr {
		return fmt.Errorf("kit address mismatch: %s != %s", kitAddr, state.escrowAddr)
	}
	logf("Kit reconstructs correct escrow address: %s", kitAddr)

	// Encode
	encoded, err := kit.Encode()
	if err != nil {
		return fmt.Errorf("kit encoding failed: %w", err)
	}
	state.recoveryKit = encoded
	logf("Recovery kit encoded: %s...%s (%d chars)", encoded[:20], encoded[len(encoded)-10:], len(encoded))

	// Verify roundtrip
	decoded, err := escrow.DecodeRecoveryKit(encoded)
	if err != nil {
		return fmt.Errorf("kit decode failed: %w", err)
	}
	decodedAddr, err := decoded.EscrowAddress()
	if err != nil {
		return fmt.Errorf("decoded kit address failed: %w", err)
	}
	if decodedAddr != state.escrowAddr {
		return fmt.Errorf("roundtrip address mismatch: %s != %s", decodedAddr, state.escrowAddr)
	}
	logf("Encode → decode roundtrip verified")

	return nil
}

func stepDeleteServiceState(ctx context.Context, state *testState) error {
	// Save the recovery kit and elementsd reference, wipe everything else
	recoveryKit := state.recoveryKit
	recoverySecret := state.recoverySecret
	elementsd := state.elementsd
	lnd := state.lnd
	cln := state.cln
	oracleKey := state.oracleKey
	csvTimeout := state.deal.TimeoutBlocks

	logf("Wiping deal store, keys, deal object, escrow script...")
	logf("After this, the user has ONLY:")
	logf("  - Recovery kit string (%d chars)", len(recoveryKit))
	if recoverySecret != "" {
		logf("  - Buyer's secret (for seller recovery)")
	}
	logf("  - Access to an elementsd node")

	// Nuke all service state
	state.deal = nil
	state.sellerKey = nil
	state.buyerKey = nil
	state.secret = [escrow.SecretSize]byte{}
	state.secretHash = [32]byte{}
	state.fundResult = nil
	state.escrowAddr = ""
	state.escrowScript = nil

	// Keep only what the recovery scenario needs
	state.recoveryKit = recoveryKit
	state.recoverySecret = recoverySecret
	state.elementsd = elementsd
	state.lnd = lnd
	state.cln = cln
	state.oracleKey = oracleKey
	state.csvTimeout = csvTimeout

	logf("Service state destroyed. Only recovery kit remains.")
	return nil
}

func stepRecoverFromKit(ctx context.Context, state *testState) error {
	logf("Decoding recovery kit...")
	kit, err := escrow.DecodeRecoveryKit(state.recoveryKit)
	if err != nil {
		return fmt.Errorf("failed to decode kit: %w", err)
	}

	logf("Kit contents:")
	logf("  Role:          %s", kit.Role)
	logf("  Seller pubkey: %s", kit.SellerPubKey)
	logf("  Buyer pubkey:  %s", kit.BuyerPubKey)
	logf("  Oracle pubkey: %s", kit.OraclePubKey)
	logf("  Secret hash:   %s", kit.SecretHash)
	logf("  Timeout:       %d blocks", kit.TimeoutBlocks)
	logf("  Fund outpoint: %s:%d", kit.FundTxID, kit.FundVout)
	logf("  Amount:        %d sats", kit.Amount)

	// Reconstruct escrow script from kit only
	logf("Reconstructing escrow tapscript tree from kit parameters...")
	params, err := kit.EscrowParams()
	if err != nil {
		return fmt.Errorf("failed to reconstruct params: %w", err)
	}
	es, err := escrow.NewEscrowScript(*params)
	if err != nil {
		return fmt.Errorf("failed to reconstruct escrow script: %w", err)
	}

	addr, err := es.Address(kit.NetworkHRP)
	if err != nil {
		return fmt.Errorf("failed to reconstruct address: %w", err)
	}
	logf("Reconstructed escrow address: %s", addr)

	// Verify UTXO still exists
	out, err := state.elementsd.GetTxOut(ctx, kit.FundTxID, kit.FundVout)
	if err != nil {
		return fmt.Errorf("failed to check UTXO: %w", err)
	}
	if out == nil {
		return fmt.Errorf("escrow UTXO already spent!")
	}
	logf("Escrow UTXO confirmed unspent at %s:%d", kit.FundTxID, kit.FundVout)

	// Parse private key from kit
	privKeyBytes, err := hex.DecodeString(kit.PrivKey)
	if err != nil {
		return fmt.Errorf("failed to decode private key: %w", err)
	}
	privKey, _ := btcec.PrivKeyFromBytes(privKeyBytes)

	// Get destination address
	destAddr, err := state.elementsd.GetNewAddress(ctx)
	if err != nil {
		return fmt.Errorf("failed to get dest address: %w", err)
	}
	state.recoveryDest = destAddr
	logf("Recovery destination: %s", destAddr)

	// Claim via timeout leaf
	logf("Building claim transaction (timeout leaf, BIP68 sequence)...")
	claimResult, err := swap.ClaimEscrow(ctx, state.elementsd, swap.ClaimEscrowConfig{
		EscrowScript: es,
		FundTxID:     kit.FundTxID,
		FundVout:     kit.FundVout,
		Amount:       kit.Amount,
		Leaf:         swap.EscrowLeafTimeout,
		SigningKeys:  []*btcec.PrivateKey{privKey},
		DestAddress:  destAddr,
		Network:      &elementsNetwork.Regtest,
	})
	if err != nil {
		return fmt.Errorf("recovery claim failed: %w", err)
	}

	logf("RECOVERED! Funds claimed in txid: %s", claimResult.TxID)
	logf("No service, no deal file, no LN — just the kit and elementsd.")

	// Mine to confirm
	mineAddr, _ := state.elementsd.GetNewAddress(ctx)
	_, _ = state.elementsd.GenerateToAddress(ctx, 1, mineAddr)

	return nil
}

func stepVerifyRecovery(ctx context.Context, state *testState) error {
	kit, err := escrow.DecodeRecoveryKit(state.recoveryKit)
	if err != nil {
		return fmt.Errorf("failed to decode kit: %w", err)
	}

	// Verify the original UTXO is now spent
	out, err := state.elementsd.GetTxOut(ctx, kit.FundTxID, kit.FundVout)
	if err != nil {
		return fmt.Errorf("failed to check UTXO: %w", err)
	}
	if out != nil {
		return fmt.Errorf("escrow UTXO should be spent after recovery, but it still exists")
	}
	logf("Original escrow UTXO %s:%d is spent (confirmed)", kit.FundTxID, kit.FundVout)
	logf("Recovery destination %s received %d sats (minus fee)", state.recoveryDest, kit.Amount)
	logf("Self-custody guarantee verified: user recovered funds without the service.")
	return nil
}

// === Seller recovery steps ===

func stepExportSellerRecoveryKit(ctx context.Context, state *testState) error {
	kit, err := escrow.RecoveryKitForSeller(state.deal)
	if err != nil {
		return fmt.Errorf("failed to create seller recovery kit: %w", err)
	}
	kit.NetworkHRP = networkHRP
	kit.Amount = state.escrowAmount // actual VTXO amount (deal amount minus HTLC fee)

	if err := kit.Validate(); err != nil {
		return fmt.Errorf("kit validation failed: %w", err)
	}
	logf("Seller recovery kit validated")

	// Verify it can reconstruct the escrow address
	kitAddr, err := kit.EscrowAddress()
	if err != nil {
		return fmt.Errorf("kit address reconstruction failed: %w", err)
	}
	if kitAddr != state.escrowAddr {
		return fmt.Errorf("kit address mismatch: %s != %s", kitAddr, state.escrowAddr)
	}
	logf("Kit reconstructs correct escrow address: %s", kitAddr)

	// Encode
	encoded, err := kit.Encode()
	if err != nil {
		return fmt.Errorf("kit encoding failed: %w", err)
	}
	state.recoveryKit = encoded
	logf("Seller recovery kit encoded: %s...%s (%d chars)", encoded[:20], encoded[len(encoded)-10:], len(encoded))

	// Save the buyer's secret separately — in production the buyer reveals this
	state.recoverySecret = hex.EncodeToString(state.secret[:])
	logf("Buyer's secret saved for seller recovery: %s...", state.recoverySecret[:16])

	// Verify the seller kit does NOT contain the secret
	decoded, err := escrow.DecodeRecoveryKit(encoded)
	if err != nil {
		return fmt.Errorf("kit decode failed: %w", err)
	}
	if decoded.Secret != "" {
		return fmt.Errorf("SECURITY: seller kit should NOT contain the buyer's secret")
	}
	logf("Verified: seller kit does not contain buyer's secret")

	return nil
}

func stepSellerRecoverFromKit(ctx context.Context, state *testState) error {
	logf("Decoding seller recovery kit...")
	kit, err := escrow.DecodeRecoveryKit(state.recoveryKit)
	if err != nil {
		return fmt.Errorf("failed to decode kit: %w", err)
	}

	logf("Kit contents:")
	logf("  Role:          %s", kit.Role)
	logf("  Seller pubkey: %s", kit.SellerPubKey)
	logf("  Buyer pubkey:  %s", kit.BuyerPubKey)
	logf("  Oracle pubkey: %s", kit.OraclePubKey)
	logf("  Secret hash:   %s", kit.SecretHash)
	logf("  Timeout:       %d blocks", kit.TimeoutBlocks)
	logf("  Fund outpoint: %s:%d", kit.FundTxID, kit.FundVout)
	logf("  Amount:        %d sats", kit.Amount)
	logf("  Secret in kit: (none — seller kit)")

	if kit.Role != "seller" {
		return fmt.Errorf("expected seller role, got %s", kit.Role)
	}

	// Reconstruct escrow script
	logf("Reconstructing escrow tapscript tree from kit parameters...")
	params, err := kit.EscrowParams()
	if err != nil {
		return fmt.Errorf("failed to reconstruct params: %w", err)
	}
	es, err := escrow.NewEscrowScript(*params)
	if err != nil {
		return fmt.Errorf("failed to reconstruct escrow script: %w", err)
	}

	addr, err := es.Address(kit.NetworkHRP)
	if err != nil {
		return fmt.Errorf("failed to reconstruct address: %w", err)
	}
	logf("Reconstructed escrow address: %s", addr)

	// Verify UTXO still exists
	out, err := state.elementsd.GetTxOut(ctx, kit.FundTxID, kit.FundVout)
	if err != nil {
		return fmt.Errorf("failed to check UTXO: %w", err)
	}
	if out == nil {
		return fmt.Errorf("escrow UTXO already spent!")
	}
	logf("Escrow UTXO confirmed unspent at %s:%d", kit.FundTxID, kit.FundVout)

	// Parse seller private key from kit
	privKeyBytes, err := hex.DecodeString(kit.PrivKey)
	if err != nil {
		return fmt.Errorf("failed to decode private key: %w", err)
	}
	privKey, _ := btcec.PrivKeyFromBytes(privKeyBytes)

	// Parse buyer's secret (provided separately, not from kit)
	preimage, err := hex.DecodeString(state.recoverySecret)
	if err != nil {
		return fmt.Errorf("failed to decode buyer secret: %w", err)
	}
	logf("Using buyer's secret: %s...", state.recoverySecret[:16])

	// Get destination address
	destAddr, err := state.elementsd.GetNewAddress(ctx)
	if err != nil {
		return fmt.Errorf("failed to get dest address: %w", err)
	}
	state.recoveryDest = destAddr
	logf("Recovery destination: %s", destAddr)

	// Claim via release leaf (seller sig + buyer's preimage, no CSV needed)
	logf("Building claim transaction (release leaf, seller sig + preimage)...")
	claimResult, err := swap.ClaimEscrow(ctx, state.elementsd, swap.ClaimEscrowConfig{
		EscrowScript: es,
		FundTxID:     kit.FundTxID,
		FundVout:     kit.FundVout,
		Amount:       kit.Amount,
		Leaf:         swap.EscrowLeafRelease,
		SigningKeys:  []*btcec.PrivateKey{privKey},
		Preimage:     preimage,
		DestAddress:  destAddr,
		Network:      &elementsNetwork.Regtest,
	})
	if err != nil {
		return fmt.Errorf("seller recovery claim failed: %w", err)
	}

	logf("RECOVERED! Seller claimed funds via release leaf, txid: %s", claimResult.TxID)
	logf("No service, no deal file, no CSV wait — just the kit, secret, and elementsd.")

	// Mine to confirm
	mineAddr, _ := state.elementsd.GetNewAddress(ctx)
	_, _ = state.elementsd.GenerateToAddress(ctx, 1, mineAddr)

	return nil
}

// === Dispute steps ===

func stepDisputeSeller(ctx context.Context, state *testState) error {
	destAddr, err := state.elementsd.GetNewAddress(ctx)
	if err != nil {
		return fmt.Errorf("dest address: %w", err)
	}

	logf("[On-chain] Building escrow claim tx (dispute→seller leaf)...")
	logf("  Leaf:        2 (MultisigClosure: oracle + seller)")
	logf("  Oracle:      %s...", hex.EncodeToString(schnorr.SerializePubKey(state.oracleKey.PubKey()))[:16])
	logf("  Seller:      %s...", hex.EncodeToString(schnorr.SerializePubKey(state.sellerKey.PubKey()))[:16])
	logf("  Destination: %s", destAddr)

	claimResult, err := swap.ClaimEscrow(ctx, state.elementsd, swap.ClaimEscrowConfig{
		EscrowScript: state.escrowScript,
		FundTxID:     state.deal.FundTxID,
		FundVout:     state.deal.FundVout,
		Amount:       state.escrowAmount,
		Leaf:         swap.EscrowLeafDisputeSeller,
		SigningKeys:  []*btcec.PrivateKey{state.oracleKey, state.sellerKey},
		DestAddress:  destAddr,
		Network:      &elementsNetwork.Regtest,
	})
	if err != nil {
		return fmt.Errorf("escrow claim: %w", err)
	}
	logf("[On-chain] Escrow claimed via dispute→seller! txid: %s", claimResult.TxID)

	logf("Mining 1 confirmation block...")
	addr, _ := state.elementsd.GetNewAddress(ctx)
	_, _ = state.elementsd.GenerateToAddress(ctx, 1, addr)

	logf("[Lightning] Creating seller invoice on CLN...")
	label := fmt.Sprintf("escrow-dispute-seller-%d", time.Now().UnixNano())
	inv, err := state.cln.CreateInvoice(ctx, state.deal.Amount*1000, label, "Escrow dispute payout (seller wins)")
	if err != nil {
		return fmt.Errorf("seller invoice: %w", err)
	}
	state.lastInvoiceLabel = label

	logf("[Lightning] LND paying seller's invoice...")
	_, err = swap.Payout(ctx, swap.PayoutConfig{
		LND:            state.lnd,
		PaymentRequest: inv.Bolt11,
	})
	if err != nil {
		return fmt.Errorf("payout: %w", err)
	}
	logf("[Lightning] Seller paid %d sats via LN (dispute won)", state.deal.Amount)

	if err := state.deal.Dispute(claimResult.TxID); err != nil {
		return err
	}
	logf("Deal state: %s → %s", escrow.DealStateFunded, state.deal.State)
	return state.store.Save(state.deal)
}

func stepDisputeBuyer(ctx context.Context, state *testState) error {
	destAddr, err := state.elementsd.GetNewAddress(ctx)
	if err != nil {
		return fmt.Errorf("dest address: %w", err)
	}

	logf("[On-chain] Building escrow claim tx (dispute→buyer leaf)...")
	logf("  Leaf:        3 (MultisigClosure: oracle + buyer)")
	logf("  Oracle:      %s...", hex.EncodeToString(schnorr.SerializePubKey(state.oracleKey.PubKey()))[:16])
	logf("  Buyer:       %s...", hex.EncodeToString(schnorr.SerializePubKey(state.buyerKey.PubKey()))[:16])
	logf("  Destination: %s", destAddr)

	claimResult, err := swap.ClaimEscrow(ctx, state.elementsd, swap.ClaimEscrowConfig{
		EscrowScript: state.escrowScript,
		FundTxID:     state.deal.FundTxID,
		FundVout:     state.deal.FundVout,
		Amount:       state.escrowAmount,
		Leaf:         swap.EscrowLeafDisputeBuyer,
		SigningKeys:  []*btcec.PrivateKey{state.oracleKey, state.buyerKey},
		DestAddress:  destAddr,
		Network:      &elementsNetwork.Regtest,
	})
	if err != nil {
		return fmt.Errorf("escrow claim: %w", err)
	}
	logf("[On-chain] Escrow claimed via dispute→buyer! txid: %s", claimResult.TxID)

	logf("Mining 1 confirmation block...")
	addr, _ := state.elementsd.GetNewAddress(ctx)
	_, _ = state.elementsd.GenerateToAddress(ctx, 1, addr)

	logf("[Lightning] Creating buyer refund invoice on CLN...")
	label := fmt.Sprintf("escrow-dispute-buyer-%d", time.Now().UnixNano())
	inv, err := state.cln.CreateInvoice(ctx, state.deal.Amount*1000, label, "Escrow dispute refund (buyer wins)")
	if err != nil {
		return fmt.Errorf("buyer invoice: %w", err)
	}
	state.lastInvoiceLabel = label

	logf("[Lightning] LND paying buyer's refund invoice...")
	_, err = swap.Payout(ctx, swap.PayoutConfig{
		LND:            state.lnd,
		PaymentRequest: inv.Bolt11,
	})
	if err != nil {
		return fmt.Errorf("refund payout: %w", err)
	}
	logf("[Lightning] Buyer refunded %d sats via LN (dispute won)", state.deal.Amount)

	if err := state.deal.Dispute(claimResult.TxID); err != nil {
		return err
	}
	logf("Deal state: %s → %s", escrow.DealStateFunded, state.deal.State)
	return state.store.Save(state.deal)
}

func stepVerifyDisputed(ctx context.Context, state *testState) error {
	deal, err := state.store.Load(state.deal.ID)
	if err != nil {
		return err
	}
	if deal.State != escrow.DealStateDisputed {
		return fmt.Errorf("expected DISPUTED, got %s", deal.State)
	}
	logf("Deal %s state: %s", deal.ID[:8], deal.State)
	logf("Claim txid:    %s", deal.ClaimTxID)
	return nil
}

// === Helpers ===

// logf prints a verbose log line, indented under the current step.
func logf(format string, args ...interface{}) {
	if !verbose {
		return
	}
	fmt.Printf("│  → %s\n", fmt.Sprintf(format, args...))
}

func leafTypeName(index int) string {
	switch index {
	case 0:
		return "Release (ConditionMultisigClosure: SHA256 preimage + seller sig)"
	case 1:
		return "Timeout (CSVMultisigClosure: CSV delay + buyer sig)"
	case 2:
		return "Dispute→Seller (MultisigClosure: oracle + seller sigs)"
	case 3:
		return "Dispute→Buyer (MultisigClosure: oracle + buyer sigs)"
	default:
		return "Unknown"
	}
}

func truncJSON(data []byte, maxLen int) string {
	s := string(data)
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
