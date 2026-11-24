package update

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReleaseSignatureAcceptsOnlyTrustedKeys(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = 7
	trusted := base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey))
	t.Cleanup(func(keys []string) func() { return func() { releaseKeys = keys } }(releaseKeys))
	releaseKeys = []string{trusted}
	manifest := []byte(`{"schemaVersion":1,"version":"9.9.9"}`)
	signature, err := SignRelease(manifest, []byte(base64.StdEncoding.EncodeToString(seed)))
	if err != nil || verifyRelease(manifest, signature) != nil {
		t.Fatalf("a valid signature was rejected: %v", err)
	}
	if verifyRelease(append(manifest, ' '), signature) == nil {
		t.Fatal("a modified manifest was accepted")
	}
	other := make([]byte, ed25519.SeedSize)
	forged, _ := SignRelease(manifest, []byte(base64.StdEncoding.EncodeToString(other)))
	if verifyRelease(manifest, forged) == nil || verifyRelease(manifest, []byte("not base64")) == nil {
		t.Fatal("an untrusted or malformed signature was accepted")
	}
}

func TestRemoteReleaseRequiresSignature(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/release.json" {
			w.Write([]byte(`{"schemaVersion":1,"version":"9.9.9","artifacts":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	m := Manager{Root: t.TempDir(), ReleaseURL: server.URL + "/release.json", HTTPClient: server.Client()}
	if _, err := m.Check(context.Background()); !errors.Is(err, errReleaseSignature) {
		t.Fatalf("an unsigned HTTPS manifest was not rejected: %v", err)
	}
	if loopbackRelease(server.URL) || !loopbackRelease("http://127.0.0.1:9/release.json") {
		t.Fatal("only plain-HTTP loopback test servers may skip signatures")
	}
}
