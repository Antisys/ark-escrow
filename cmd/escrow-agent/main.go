package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	log "github.com/sirupsen/logrus"
)

var Version = "dev"

func main() {
	log.SetLevel(log.DebugLevel)
	log.Infof("ark-escrow agent %s starting...", Version)

	// TODO: Load config, init store, start gRPC server

	fmt.Println("Escrow agent is ready. Waiting for shutdown signal...")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	<-sigChan

	log.Info("shutting down...")
}
