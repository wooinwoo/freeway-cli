package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wooinwoo/freeway-cli/cli/client"
	"github.com/wooinwoo/freeway-cli/cli/imagegen"
)

func openGateway(ctx context.Context, address, key string) (*client.HTTP, func(), error) {
	if address != "" || key != "" {
		if key == "" {
			return nil, nil, errors.New("set FREEWAY_API_KEY when choosing a gateway URL")
		}
		gateway, err := client.NewHTTP(client.Config{URL: address, APIKey: key})
		if err != nil {
			return nil, nil, err
		}
		return gateway, gateway.Close, nil
	}
	binary, err := nativeBinary("freewayd")
	if err != nil {
		return nil, nil, err
	}
	connect := func(attempt context.Context) (*client.HTTP, func(), error) {
		return connectManaged(attempt, ctx, binary)
	}
	active, release, err := connect(ctx)
	if err != nil {
		return nil, nil, err
	}
	gateway := client.SuperviseManaged(active, release, connect)
	return gateway, gateway.Close, nil
}

func connectManaged(ctx, lifetime context.Context, binary string) (*client.HTTP, func(), error) {
	var leaseErr error
	for range 2 {
		gateway, err := discoverGateway(ctx, binary)
		if err != nil {
			return nil, nil, err
		}
		release, err := gateway.Lease(lifetime)
		if err == nil {
			if err := ctx.Err(); err != nil {
				release()
				gateway.Close()
				return nil, nil, err
			}
			return gateway, func() { release(); gateway.Close() }, nil
		}
		gateway.Close()
		leaseErr = err
	}
	return nil, nil, fmt.Errorf("connect to the local Freeway gateway: %w", leaseErr)
}

// gatewayFailure drops the prefix that each gateway process layer adds, and explains permission failures.
func gatewayFailure(stderr string) string {
	message := strings.TrimSpace(stderr)
	for {
		rest, ok := strings.CutPrefix(message, "Freeway gateway: ")
		if !ok {
			break
		}
		message = rest
	}
	if strings.Contains(message, "owner-only permissions") {
		dir := os.Getenv("FREEWAY_GATEWAY_HOME")
		if dir == "" {
			if home, err := os.UserHomeDir(); err == nil {
				dir = filepath.Join(home, ".freeway", "gateway")
			}
		}
		message += fmt.Sprintf("\nFix the gateway folder %s: chmod 700 on it and its folders, chmod 600 on its files.", dir)
	}
	return "local gateway: " + message
}

func discoverGateway(ctx context.Context, binary string) (*client.HTTP, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "connect")
	cmd.Env = imagegen.WithoutTokenEnv()
	output, err := cmd.Output()
	if err != nil {
		var process *exec.ExitError
		if errors.As(err, &process) && len(process.Stderr) > 0 {
			return nil, errors.New(gatewayFailure(string(process.Stderr)))
		}
		return nil, fmt.Errorf("start the local Freeway gateway: %w", err)
	}
	var connection struct {
		URL    string `json:"url"`
		APIKey string `json:"apiKey"`
	}
	if json.Unmarshal(output, &connection) != nil || connection.URL == "" || connection.APIKey == "" {
		return nil, errors.New("the local gateway did not return a valid connection")
	}
	return client.NewHTTP(client.Config{URL: connection.URL, APIKey: connection.APIKey, Managed: true})
}
