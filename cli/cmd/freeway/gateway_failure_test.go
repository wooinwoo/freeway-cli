package main

import (
	"strings"
	"testing"
)

func TestGatewayFailureDropsRepeatedPrefixesAndExplainsPermissions(t *testing.T) {
	t.Setenv("FREEWAY_GATEWAY_HOME", "/tmp/fixture-gateway")
	got := gatewayFailure("Freeway gateway: Freeway gateway: gateway data must use owner-only permissions (0700 directories, 0600 files)\n")
	if strings.Count(got, "Freeway gateway") != 0 || !strings.HasPrefix(got, "local gateway: gateway data") ||
		!strings.Contains(got, "/tmp/fixture-gateway") || !strings.Contains(got, "chmod 700") {
		t.Fatalf("unexpected message: %q", got)
	}
}
