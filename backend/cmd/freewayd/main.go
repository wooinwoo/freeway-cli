// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/wooinwoo/freeway-cli/backend/internal/app"
	"github.com/wooinwoo/freeway-cli/backend/internal/storage"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Freeway gateway:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "version") {
		fmt.Println(version)
		return nil
	}
	command := "serve"
	if len(args) > 0 && (args[0] == "serve" || args[0] == "key") {
		command, args = args[0], args[1:]
	}
	defaultDir, err := app.DataDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("freewayd "+command, flag.ContinueOnError)
	path := flags.String("data-dir", defaultDir, "private gateway data directory")
	var address, enabled, preferred, allowed string
	if command == "serve" {
		flags.StringVar(&address, "listen", app.DefaultAddress, "loopback address and port")
		flags.StringVar(&enabled, "providers", "", "comma-separated gateway provider IDs")
		flags.StringVar(&preferred, "prefer", "", "comma-separated preferred route IDs in priority order")
		flags.StringVar(&allowed, "allow-models", "", "comma-separated eligible route IDs")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("expected serve, key or --version")
	}
	if command == "key" {
		key, err := storage.ReadClientKey(*path)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(os.Stdout, key)
		return err
	}
	return app.Serve(ctx, app.Config{Address: address, DataDir: *path,
		Providers: csv(enabled), Preferred: csv(preferred), Allowed: csv(allowed)})
}

func csv(value string) []string {
	fields := strings.Split(value, ",")
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	return slices.DeleteFunc(fields, func(item string) bool { return item == "" })
}
