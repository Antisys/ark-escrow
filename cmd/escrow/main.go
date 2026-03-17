package main

import (
	"fmt"
	"os"

	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:  "escrow",
		Usage: "Liquid Ark Escrow CLI — non-custodial escrow via Lightning",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "datadir",
				Usage:   "Data directory for deal storage",
				Value:   defaultDataDir(),
				EnvVars: []string{"ESCROW_DATADIR"},
			},
			&cli.StringFlag{
				Name:    "lnd-url",
				Usage:   "LND REST URL (e.g., https://localhost:18080)",
				Value:   "https://localhost:18080",
				EnvVars: []string{"ESCROW_LND_URL"},
			},
			&cli.StringFlag{
				Name:    "lnd-macaroon",
				Usage:   "LND admin macaroon (hex)",
				EnvVars: []string{"ESCROW_LND_MACAROON"},
			},
			&cli.StringFlag{
				Name:    "elementsd-url",
				Usage:   "elementsd RPC URL (e.g., http://user:pass@localhost:18884)",
				EnvVars: []string{"ESCROW_ELEMENTSD_URL"},
			},
			&cli.StringFlag{
				Name:    "oracle-pubkey",
				Usage:   "Oracle public key (hex, 32 or 33 bytes)",
				EnvVars: []string{"ESCROW_ORACLE_PUBKEY"},
			},
			&cli.StringFlag{
				Name:    "network-hrp",
				Usage:   "Bech32 HRP for addresses (ert for regtest, ex for liquid)",
				Value:   "ert",
				EnvVars: []string{"ESCROW_NETWORK_HRP"},
			},
		},
		Commands: []*cli.Command{
			createCmd,
			joinCmd,
			fundCmd,
			shipCmd,
			releaseCmd,
			refundCmd,
			disputeCmd,
			statusCmd,
			recoverykitCmd,
			recoverCmd,
		},
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".ark-escrow"
	}
	return home + "/.ark-escrow/deals"
}
