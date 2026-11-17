package main

//go:generate go run . gen-flag-docs ../../docs/flags.md

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/script"
)

const exitWaiting = 75

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	defer logging.Close()

	if err := prepare(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	buildTree()
	if err := missingWorkflow(rootCmd); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if waiting := (*script.Waiting)(nil); errors.As(err, &waiting) {
			os.Exit(exitWaiting)
		}
		os.Exit(1)
	}
}
