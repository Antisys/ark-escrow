package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Antisys/ark-escrow/pkg/escrow"
	"github.com/Antisys/ark-escrow/pkg/escrow/swap"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/urfave/cli/v2"
)

var createCmd = &cli.Command{
	Name:  "create",
	Usage: "Create a new escrow deal (seller)",
	Flags: []cli.Flag{
		&cli.Uint64Flag{Name: "amount", Required: true, Usage: "Amount in satoshis"},
		&cli.StringFlag{Name: "title", Required: true, Usage: "Deal title/description"},
		&cli.Uint64Flag{Name: "timeout", Value: escrow.DefaultTimeoutBlocks, Usage: "Timeout in blocks"},
	},
	Action: createAction,
}

func createAction(c *cli.Context) error {
	store, err := escrow.NewFileStore(c.String("datadir"))
	if err != nil {
		return err
	}

	oraclePubKey := c.String("oracle-pubkey")
	if oraclePubKey == "" {
		return fmt.Errorf("--oracle-pubkey is required")
	}

	// Generate seller keypair
	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		return fmt.Errorf("failed to generate seller key: %w", err)
	}
	sellerPubKey := hex.EncodeToString(schnorr.SerializePubKey(privKey.PubKey()))

	deal, err := escrow.NewDeal(
		c.String("title"),
		c.Uint64("amount"),
		sellerPubKey,
		oraclePubKey,
		uint32(c.Uint64("timeout")),
	)
	if err != nil {
		return err
	}

	// Store the seller's private key for later signing
	deal.SellerPrivKey = hex.EncodeToString(privKey.Serialize())

	if err := store.Save(deal); err != nil {
		return err
	}

	token := deal.JoinToken()
	tokenJSON, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("failed to marshal join token: %w", err)
	}

	fmt.Printf("Deal created: %s\n", deal.ID)
	fmt.Printf("Seller pubkey: %s\n", sellerPubKey)
	fmt.Printf("Join token: %s\n", string(tokenJSON))
	return nil
}

var joinCmd = &cli.Command{
	Name:  "join",
	Usage: "Join an escrow deal (buyer)",
	Flags: []cli.Flag{
		&cli.StringFlag{Name: "token", Required: true, Usage: "Join token JSON from seller"},
	},
	Action: joinAction,
}

func joinAction(c *cli.Context) error {
	store, err := escrow.NewFileStore(c.String("datadir"))
	if err != nil {
		return err
	}

	// Parse join token
	var token escrow.JoinToken
	if err := json.Unmarshal([]byte(c.String("token")), &token); err != nil {
		return fmt.Errorf("invalid join token: %w", err)
	}

	// Load the deal (it may have been synced or we create from token)
	deal, err := store.Load(token.DealID)
	if err != nil {
		// Deal doesn't exist locally — create from token
		deal = &escrow.Deal{
			ID:            token.DealID,
			State:         escrow.DealStateCreated,
			Title:         token.Title,
			Amount:        token.Amount,
			SellerPubKey:  token.SellerPubKey,
			OraclePubKey:  token.OraclePubKey,
			TimeoutBlocks: token.TimeoutBlocks,
			CreatedAt:     time.Now().UTC(),
			UpdatedAt:     time.Now().UTC(),
		}
	}

	// Generate buyer keypair
	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		return fmt.Errorf("failed to generate buyer key: %w", err)
	}
	buyerPubKey := hex.EncodeToString(schnorr.SerializePubKey(privKey.PubKey()))

	// Generate secret
	secret, secretHash, err := escrow.GenerateSecret()
	if err != nil {
		return err
	}

	// Join the deal
	if err := deal.Join(buyerPubKey, hex.EncodeToString(secretHash[:])); err != nil {
		return err
	}

	// Store the buyer's private key and escrow secret
	deal.BuyerPrivKey = hex.EncodeToString(privKey.Serialize())
	deal.Secret = hex.EncodeToString(secret[:])

	// Compute escrow address
	params, err := deal.EscrowParams()
	if err != nil {
		return err
	}
	es, err := escrow.NewEscrowScript(*params)
	if err != nil {
		return err
	}
	addr, err := es.Address(c.String("network-hrp"))
	if err != nil {
		return err
	}
	deal.SetEscrowAddress(addr)

	if err := store.Save(deal); err != nil {
		return err
	}

	fmt.Printf("Joined deal: %s\n", deal.ID)
	fmt.Printf("Buyer pubkey: %s\n", buyerPubKey)
	fmt.Printf("Secret hash: %s\n", hex.EncodeToString(secretHash[:]))
	fmt.Printf("Escrow address: %s\n", addr)

	// Print recovery kit for buyer
	kit, err := escrow.RecoveryKitForBuyer(deal)
	if err == nil {
		kit.NetworkHRP = c.String("network-hrp")
		encoded, err := kit.Encode()
		if err == nil {
			fmt.Printf("\n=== RECOVERY KIT (SAVE THIS — allows you to claim funds without the service) ===\n")
			fmt.Printf("%s\n", encoded)
			fmt.Printf("=================================================================================\n")
		}
	}

	return nil
}

var fundCmd = &cli.Command{
	Name:  "fund",
	Usage: "Fund the escrow via Lightning (buyer)",
	Flags: []cli.Flag{
		&cli.StringFlag{Name: "deal", Required: true, Usage: "Deal ID"},
	},
	Action: fundAction,
}

func fundAction(c *cli.Context) error {
	store, err := escrow.NewFileStore(c.String("datadir"))
	if err != nil {
		return err
	}

	deal, err := store.Load(c.String("deal"))
	if err != nil {
		return err
	}

	if deal.EscrowAddress == "" {
		return fmt.Errorf("deal has no escrow address — join first")
	}

	lnd := swap.NewLNDClient(c.String("lnd-url"), c.String("lnd-macaroon"))
	elementsd, err := swap.NewElementsdClient(c.String("elementsd-url"))
	if err != nil {
		return err
	}

	ctx := context.Background()
	result, err := swap.Fund(ctx, swap.FundConfig{
		LND:           lnd,
		Elementsd:     elementsd,
		EscrowAddress: deal.EscrowAddress,
		AmountSats:    deal.Amount,
	})
	if err != nil {
		return err
	}

	fmt.Printf("HODL invoice: %s\n", result.PaymentRequest)
	fmt.Printf("Waiting for payment...\n")

	// Wait for payment and settle
	err = swap.WaitForPaymentAndSettle(ctx, lnd, result.PaymentHash, result.Preimage, 2*time.Second)
	if err != nil {
		return fmt.Errorf("payment settlement failed: %w", err)
	}

	fundTxID := result.EscrowTxID
	fundVout := result.EscrowVout
	if fundTxID == "" {
		fundTxID = result.HTLCTxID
		fundVout = result.HTLCVout
	}
	if err := deal.Fund(fundTxID, fundVout); err != nil {
		return err
	}
	if err := store.Save(deal); err != nil {
		return err
	}

	fmt.Printf("Deal funded! TxID: %s\n", fundTxID)

	// Print updated recovery kit with funding outpoint
	kit, err := escrow.RecoveryKitForBuyer(deal)
	if err == nil {
		kit.NetworkHRP = c.String("network-hrp")
		encoded, err := kit.Encode()
		if err == nil {
			fmt.Printf("\n=== RECOVERY KIT (UPDATED — now includes funding outpoint) ===\n")
			fmt.Printf("%s\n", encoded)
			fmt.Printf("===============================================================\n")
		}
	}

	return nil
}

var shipCmd = &cli.Command{
	Name:  "ship",
	Usage: "Mark deal as shipped (seller)",
	Flags: []cli.Flag{
		&cli.StringFlag{Name: "deal", Required: true, Usage: "Deal ID"},
	},
	Action: shipAction,
}

func shipAction(c *cli.Context) error {
	store, err := escrow.NewFileStore(c.String("datadir"))
	if err != nil {
		return err
	}
	deal, err := store.Load(c.String("deal"))
	if err != nil {
		return err
	}
	if err := deal.Ship(); err != nil {
		return err
	}
	if err := store.Save(deal); err != nil {
		return err
	}
	fmt.Printf("Deal %s marked as SHIPPED\n", deal.ID)
	return nil
}

var releaseCmd = &cli.Command{
	Name:  "release",
	Usage: "Release escrow to seller (claim VTXO with preimage + seller sig, pay seller via LN)",
	Flags: []cli.Flag{
		&cli.StringFlag{Name: "deal", Required: true, Usage: "Deal ID"},
		&cli.StringFlag{Name: "seller-invoice", Required: true, Usage: "Seller's LN invoice for payout"},
		&cli.StringFlag{Name: "dest-address", Usage: "L-BTC destination for claimed escrow (default: elementsd wallet)"},
	},
	Action: releaseAction,
}

func releaseAction(c *cli.Context) error {
	store, err := escrow.NewFileStore(c.String("datadir"))
	if err != nil {
		return err
	}
	deal, err := store.Load(c.String("deal"))
	if err != nil {
		return err
	}

	if deal.SellerPrivKey == "" {
		return fmt.Errorf("seller private key not available in deal file")
	}
	if deal.Secret == "" {
		return fmt.Errorf("buyer secret not available in deal file")
	}

	sellerKey, err := parsePrivKey(deal.SellerPrivKey)
	if err != nil {
		return fmt.Errorf("invalid seller private key: %w", err)
	}

	preimage, err := hex.DecodeString(deal.Secret)
	if err != nil {
		return fmt.Errorf("invalid secret: %w", err)
	}

	elementsd, err := swap.NewElementsdClient(c.String("elementsd-url"))
	if err != nil {
		return err
	}
	lnd := swap.NewLNDClient(c.String("lnd-url"), c.String("lnd-macaroon"))

	ctx := context.Background()

	// Get destination address for the on-chain claim
	destAddr := c.String("dest-address")
	if destAddr == "" {
		destAddr, err = elementsd.GetNewAddress(ctx)
		if err != nil {
			return fmt.Errorf("failed to get destination address: %w", err)
		}
	}

	// Build escrow script
	params, err := deal.EscrowParams()
	if err != nil {
		return err
	}
	es, err := escrow.NewEscrowScript(*params)
	if err != nil {
		return err
	}

	// Step 1: Claim the escrow VTXO on-chain (release leaf: preimage + seller sig)
	fmt.Printf("Claiming escrow VTXO...\n")
	claimResult, err := swap.ClaimEscrow(ctx, elementsd, swap.ClaimEscrowConfig{
		EscrowScript: es,
		FundTxID:     deal.FundTxID,
		FundVout:     deal.FundVout,
		Amount:       deal.Amount,
		Leaf:         swap.EscrowLeafRelease,
		SigningKeys:  []*btcec.PrivateKey{sellerKey},
		Preimage:     preimage,
		DestAddress:  destAddr,
	})
	if err != nil {
		return fmt.Errorf("escrow claim failed: %w", err)
	}
	fmt.Printf("Escrow claimed: %s\n", claimResult.TxID)

	// Step 2: Pay the seller via LN
	fmt.Printf("Paying seller via LN...\n")
	payoutResult, err := swap.Payout(ctx, swap.PayoutConfig{
		LND:            lnd,
		PaymentRequest: c.String("seller-invoice"),
	})
	if err != nil {
		return fmt.Errorf("LN payout failed (escrow already claimed in %s): %w", claimResult.TxID, err)
	}

	if err := deal.Release(claimResult.TxID); err != nil {
		return err
	}
	if err := store.Save(deal); err != nil {
		return err
	}

	fmt.Printf("Deal %s RELEASED\n", deal.ID)
	fmt.Printf("Claim TxID: %s\n", claimResult.TxID)
	fmt.Printf("LN payout preimage: %s\n", hex.EncodeToString(payoutResult.Preimage))
	return nil
}

var refundCmd = &cli.Command{
	Name:  "refund",
	Usage: "Refund escrow to buyer (claim VTXO via CSV timeout, pay buyer via LN)",
	Flags: []cli.Flag{
		&cli.StringFlag{Name: "deal", Required: true, Usage: "Deal ID"},
		&cli.StringFlag{Name: "buyer-invoice", Required: true, Usage: "Buyer's LN invoice for refund"},
		&cli.StringFlag{Name: "dest-address", Usage: "L-BTC destination for claimed escrow (default: elementsd wallet)"},
	},
	Action: refundAction,
}

func refundAction(c *cli.Context) error {
	store, err := escrow.NewFileStore(c.String("datadir"))
	if err != nil {
		return err
	}
	deal, err := store.Load(c.String("deal"))
	if err != nil {
		return err
	}

	if deal.BuyerPrivKey == "" {
		return fmt.Errorf("buyer private key not available in deal file")
	}

	buyerKey, err := parsePrivKey(deal.BuyerPrivKey)
	if err != nil {
		return fmt.Errorf("invalid buyer private key: %w", err)
	}

	elementsd, err := swap.NewElementsdClient(c.String("elementsd-url"))
	if err != nil {
		return err
	}
	lnd := swap.NewLNDClient(c.String("lnd-url"), c.String("lnd-macaroon"))

	ctx := context.Background()

	destAddr := c.String("dest-address")
	if destAddr == "" {
		destAddr, err = elementsd.GetNewAddress(ctx)
		if err != nil {
			return fmt.Errorf("failed to get destination address: %w", err)
		}
	}

	// Build escrow script
	params, err := deal.EscrowParams()
	if err != nil {
		return err
	}
	es, err := escrow.NewEscrowScript(*params)
	if err != nil {
		return err
	}

	// Step 1: Claim the escrow VTXO on-chain (timeout leaf: buyer sig after CSV)
	fmt.Printf("Claiming escrow VTXO via timeout...\n")
	claimResult, err := swap.ClaimEscrow(ctx, elementsd, swap.ClaimEscrowConfig{
		EscrowScript: es,
		FundTxID:     deal.FundTxID,
		FundVout:     deal.FundVout,
		Amount:       deal.Amount,
		Leaf:         swap.EscrowLeafTimeout,
		SigningKeys:  []*btcec.PrivateKey{buyerKey},
		DestAddress:  destAddr,
	})
	if err != nil {
		return fmt.Errorf("escrow claim failed: %w", err)
	}
	fmt.Printf("Escrow claimed: %s\n", claimResult.TxID)

	// Step 2: Pay the buyer via LN
	fmt.Printf("Paying buyer via LN...\n")
	payoutResult, err := swap.Payout(ctx, swap.PayoutConfig{
		LND:            lnd,
		PaymentRequest: c.String("buyer-invoice"),
	})
	if err != nil {
		return fmt.Errorf("LN refund failed (escrow already claimed in %s): %w", claimResult.TxID, err)
	}

	if err := deal.Refund(claimResult.TxID); err != nil {
		return err
	}
	if err := store.Save(deal); err != nil {
		return err
	}

	fmt.Printf("Deal %s REFUNDED\n", deal.ID)
	fmt.Printf("Claim TxID: %s\n", claimResult.TxID)
	fmt.Printf("LN refund preimage: %s\n", hex.EncodeToString(payoutResult.Preimage))
	return nil
}

var disputeCmd = &cli.Command{
	Name:  "dispute",
	Usage: "Resolve a dispute (oracle + winner claim escrow VTXO, pay winner via LN)",
	Flags: []cli.Flag{
		&cli.StringFlag{Name: "deal", Required: true, Usage: "Deal ID"},
		&cli.StringFlag{Name: "winner", Required: true, Usage: "Winner: 'seller' or 'buyer'"},
		&cli.StringFlag{Name: "oracle-privkey", Required: true, Usage: "Oracle private key (hex)"},
		&cli.StringFlag{Name: "winner-invoice", Required: true, Usage: "Winner's LN invoice for payout"},
		&cli.StringFlag{Name: "dest-address", Usage: "L-BTC destination for claimed escrow (default: elementsd wallet)"},
	},
	Action: disputeAction,
}

func disputeAction(c *cli.Context) error {
	store, err := escrow.NewFileStore(c.String("datadir"))
	if err != nil {
		return err
	}
	deal, err := store.Load(c.String("deal"))
	if err != nil {
		return err
	}

	winner := c.String("winner")
	if winner != "seller" && winner != "buyer" {
		return fmt.Errorf("--winner must be 'seller' or 'buyer'")
	}

	oracleKey, err := parsePrivKey(c.String("oracle-privkey"))
	if err != nil {
		return fmt.Errorf("invalid oracle private key: %w", err)
	}

	// Get the winner's private key
	var winnerKey *btcec.PrivateKey
	var leaf swap.EscrowLeaf
	if winner == "seller" {
		if deal.SellerPrivKey == "" {
			return fmt.Errorf("seller private key not available in deal file")
		}
		winnerKey, err = parsePrivKey(deal.SellerPrivKey)
		if err != nil {
			return fmt.Errorf("invalid seller private key: %w", err)
		}
		leaf = swap.EscrowLeafDisputeSeller
	} else {
		if deal.BuyerPrivKey == "" {
			return fmt.Errorf("buyer private key not available in deal file")
		}
		winnerKey, err = parsePrivKey(deal.BuyerPrivKey)
		if err != nil {
			return fmt.Errorf("invalid buyer private key: %w", err)
		}
		leaf = swap.EscrowLeafDisputeBuyer
	}

	elementsd, err := swap.NewElementsdClient(c.String("elementsd-url"))
	if err != nil {
		return err
	}
	lnd := swap.NewLNDClient(c.String("lnd-url"), c.String("lnd-macaroon"))

	ctx := context.Background()

	destAddr := c.String("dest-address")
	if destAddr == "" {
		destAddr, err = elementsd.GetNewAddress(ctx)
		if err != nil {
			return fmt.Errorf("failed to get destination address: %w", err)
		}
	}

	params, err := deal.EscrowParams()
	if err != nil {
		return err
	}
	es, err := escrow.NewEscrowScript(*params)
	if err != nil {
		return err
	}

	// Step 1: Claim the escrow VTXO (dispute leaf: oracle + winner sigs)
	fmt.Printf("Claiming escrow VTXO via dispute (%s wins)...\n", winner)
	claimResult, err := swap.ClaimEscrow(ctx, elementsd, swap.ClaimEscrowConfig{
		EscrowScript: es,
		FundTxID:     deal.FundTxID,
		FundVout:     deal.FundVout,
		Amount:       deal.Amount,
		Leaf:         leaf,
		SigningKeys:  []*btcec.PrivateKey{oracleKey, winnerKey},
		DestAddress:  destAddr,
	})
	if err != nil {
		return fmt.Errorf("escrow claim failed: %w", err)
	}
	fmt.Printf("Escrow claimed: %s\n", claimResult.TxID)

	// Step 2: Pay the winner via LN
	fmt.Printf("Paying %s via LN...\n", winner)
	payoutResult, err := swap.Payout(ctx, swap.PayoutConfig{
		LND:            lnd,
		PaymentRequest: c.String("winner-invoice"),
	})
	if err != nil {
		return fmt.Errorf("LN payout failed (escrow already claimed in %s): %w", claimResult.TxID, err)
	}

	if err := deal.Dispute(claimResult.TxID); err != nil {
		return err
	}
	if err := store.Save(deal); err != nil {
		return err
	}

	fmt.Printf("Deal %s DISPUTED — %s wins\n", deal.ID, winner)
	fmt.Printf("Claim TxID: %s\n", claimResult.TxID)
	fmt.Printf("LN payout preimage: %s\n", hex.EncodeToString(payoutResult.Preimage))
	return nil
}

var statusCmd = &cli.Command{
	Name:  "status",
	Usage: "Show deal status",
	Flags: []cli.Flag{
		&cli.StringFlag{Name: "deal", Required: true, Usage: "Deal ID"},
	},
	Action: statusAction,
}

func statusAction(c *cli.Context) error {
	store, err := escrow.NewFileStore(c.String("datadir"))
	if err != nil {
		return err
	}
	deal, err := store.Load(c.String("deal"))
	if err != nil {
		return err
	}

	fmt.Printf("Deal ID:         %s\n", deal.ID)
	fmt.Printf("State:           %s\n", deal.State)
	fmt.Printf("Title:           %s\n", deal.Title)
	fmt.Printf("Amount:          %d sats\n", deal.Amount)
	fmt.Printf("Created:         %s\n", deal.CreatedAt.Format(time.RFC3339))
	fmt.Printf("Updated:         %s\n", deal.UpdatedAt.Format(time.RFC3339))
	fmt.Printf("Seller PubKey:   %s\n", deal.SellerPubKey)
	fmt.Printf("Buyer PubKey:    %s\n", deal.BuyerPubKey)
	fmt.Printf("Oracle PubKey:   %s\n", deal.OraclePubKey)
	fmt.Printf("Timeout:         %d blocks\n", deal.TimeoutBlocks)
	fmt.Printf("Escrow Address:  %s\n", deal.EscrowAddress)
	fmt.Printf("Fund TxID:       %s\n", deal.FundTxID)
	fmt.Printf("Claim TxID:      %s\n", deal.ClaimTxID)
	return nil
}

var recoverykitCmd = &cli.Command{
	Name:  "recoverykit",
	Usage: "Export a recovery kit for a deal (allows claiming funds without the service)",
	Flags: []cli.Flag{
		&cli.StringFlag{Name: "deal", Required: true, Usage: "Deal ID"},
		&cli.StringFlag{Name: "role", Required: true, Usage: "Role: 'buyer' or 'seller'"},
		&cli.BoolFlag{Name: "json", Usage: "Output as JSON instead of encoded string"},
	},
	Action: recoverykitAction,
}

func recoverykitAction(c *cli.Context) error {
	store, err := escrow.NewFileStore(c.String("datadir"))
	if err != nil {
		return err
	}
	deal, err := store.Load(c.String("deal"))
	if err != nil {
		return err
	}

	var kit *escrow.RecoveryKit
	switch c.String("role") {
	case "buyer":
		kit, err = escrow.RecoveryKitForBuyer(deal)
	case "seller":
		kit, err = escrow.RecoveryKitForSeller(deal)
	default:
		return fmt.Errorf("--role must be 'buyer' or 'seller'")
	}
	if err != nil {
		return err
	}

	kit.NetworkHRP = c.String("network-hrp")

	if c.Bool("json") {
		j, err := kit.JSON()
		if err != nil {
			return err
		}
		fmt.Println(j)
	} else {
		encoded, err := kit.Encode()
		if err != nil {
			return err
		}
		fmt.Println(encoded)
	}

	// Verify the kit can reconstruct the escrow address
	addr, err := kit.EscrowAddress()
	if err == nil && deal.EscrowAddress != "" {
		if addr == deal.EscrowAddress {
			fmt.Fprintf(os.Stderr, "Verified: kit reconstructs escrow address %s\n", addr)
		} else {
			fmt.Fprintf(os.Stderr, "WARNING: kit address %s != deal address %s\n", addr, deal.EscrowAddress)
		}
	}

	return nil
}

var recoverCmd = &cli.Command{
	Name:  "recover",
	Usage: "Claim escrowed funds using a recovery kit (no service needed)",
	Description: `Reconstructs the escrow tapscript tree from the recovery kit and claims
the funds on-chain. Only needs access to an elementsd node.

For buyers: claims via the timeout leaf (CSV must have expired).
For sellers: claims via the release leaf (requires the buyer's secret).`,
	Flags: []cli.Flag{
		&cli.StringFlag{Name: "kit", Required: true, Usage: "Recovery kit (encoded string or path to JSON file)"},
		&cli.StringFlag{Name: "dest-address", Required: true, Usage: "Destination address for claimed L-BTC"},
		&cli.StringFlag{Name: "secret", Usage: "Buyer's secret (hex) — required for seller to claim via release leaf"},
	},
	Action: recoverAction,
}

func recoverAction(c *cli.Context) error {
	kitStr := c.String("kit")

	// Try to decode as recovery kit string first
	kit, err := escrow.DecodeRecoveryKit(kitStr)
	if err != nil {
		// Try reading as JSON file
		data, fileErr := os.ReadFile(kitStr)
		if fileErr != nil {
			return fmt.Errorf("invalid recovery kit (not a valid encoded string or file path): %w", err)
		}
		kit = &escrow.RecoveryKit{}
		if err := json.Unmarshal(data, kit); err != nil {
			return fmt.Errorf("invalid recovery kit JSON: %w", err)
		}
	}

	if err := kit.Validate(); err != nil {
		return fmt.Errorf("invalid recovery kit: %w", err)
	}

	if kit.FundTxID == "" {
		return fmt.Errorf("recovery kit has no funding outpoint — deal may not have been funded")
	}

	elementsd, err := swap.NewElementsdClient(c.String("elementsd-url"))
	if err != nil {
		return err
	}

	ctx := context.Background()

	// Reconstruct escrow script
	params, err := kit.EscrowParams()
	if err != nil {
		return fmt.Errorf("failed to reconstruct escrow params: %w", err)
	}
	es, err := escrow.NewEscrowScript(*params)
	if err != nil {
		return fmt.Errorf("failed to reconstruct escrow script: %w", err)
	}

	// Verify the escrow address matches what's on-chain
	addr, err := es.Address(kit.NetworkHRP)
	if err != nil {
		return err
	}
	fmt.Printf("Escrow address: %s\n", addr)

	// Parse the user's private key
	signingKey, err := parsePrivKey(kit.PrivKey)
	if err != nil {
		return fmt.Errorf("invalid private key in kit: %w", err)
	}

	// Determine which leaf to use
	var leaf swap.EscrowLeaf
	var preimage []byte

	switch kit.Role {
	case "buyer":
		leaf = swap.EscrowLeafTimeout
		fmt.Printf("Claiming via timeout leaf (buyer refund, CSV=%d blocks)\n", kit.TimeoutBlocks)
	case "seller":
		leaf = swap.EscrowLeafRelease
		// Seller needs the buyer's secret
		secretHex := c.String("secret")
		if secretHex == "" && kit.Secret != "" {
			secretHex = kit.Secret
		}
		if secretHex == "" {
			return fmt.Errorf("seller needs --secret (buyer's preimage) to claim via release leaf")
		}
		preimage, err = hex.DecodeString(secretHex)
		if err != nil {
			return fmt.Errorf("invalid secret: %w", err)
		}
		fmt.Printf("Claiming via release leaf (seller claim with preimage)\n")
	}

	// Check if the UTXO still exists
	out, err := elementsd.GetTxOut(ctx, kit.FundTxID, kit.FundVout)
	if err != nil {
		return fmt.Errorf("failed to check UTXO: %w", err)
	}
	if out == nil {
		return fmt.Errorf("escrow UTXO %s:%d has already been spent", kit.FundTxID, kit.FundVout)
	}
	fmt.Printf("Escrow UTXO found: %s:%d (%d sats)\n", kit.FundTxID, kit.FundVout, kit.Amount)

	// Claim
	fmt.Printf("Building and broadcasting claim transaction...\n")
	claimResult, err := swap.ClaimEscrow(ctx, elementsd, swap.ClaimEscrowConfig{
		EscrowScript: es,
		FundTxID:     kit.FundTxID,
		FundVout:     kit.FundVout,
		Amount:       kit.Amount,
		Leaf:         leaf,
		SigningKeys:  []*btcec.PrivateKey{signingKey},
		Preimage:     preimage,
		DestAddress:  c.String("dest-address"),
	})
	if err != nil {
		return fmt.Errorf("claim failed: %w", err)
	}

	fmt.Printf("Funds claimed! TxID: %s\n", claimResult.TxID)
	fmt.Printf("Destination: %s\n", c.String("dest-address"))
	return nil
}

// parsePrivKey decodes a hex-encoded 32-byte private key.
func parsePrivKey(hexKey string) (*btcec.PrivateKey, error) {
	keyBytes, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hex: %w", err)
	}
	if len(keyBytes) != 32 {
		return nil, fmt.Errorf("expected 32 bytes, got %d", len(keyBytes))
	}
	privKey, _ := btcec.PrivKeyFromBytes(keyBytes)
	return privKey, nil
}
