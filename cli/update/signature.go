package update

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net"
	"net/url"
)

// releaseKeys verify release.json.sig. To rotate a key, ship the new key here
// in one release and start signing with it in a later release.
var releaseKeys = []string{"mzdNx60Y7iLqg3DbQm/9M308xB1uNfHaQnpPOnnhfPI="}

var errReleaseSignature = errors.New("release signature is missing or invalid")

func verifyRelease(manifest, signature []byte) error {
	sig, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(signature)))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errReleaseSignature
	}
	for _, encoded := range releaseKeys {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err == nil && len(key) == ed25519.PublicKeySize && ed25519.Verify(key, manifest, sig) {
			return nil
		}
	}
	return errReleaseSignature
}

// SignRelease returns the detached signature for a manifest from a base64 Ed25519 seed.
func SignRelease(manifest, seed []byte) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(seed)))
	if err != nil || len(raw) != ed25519.SeedSize {
		return nil, errors.New("invalid release signing key")
	}
	signature := ed25519.Sign(ed25519.NewKeyFromSeed(raw), manifest)
	return []byte(base64.StdEncoding.EncodeToString(signature) + "\n"), nil
}

// Local test and development servers on loopback may serve unsigned manifests.
func loopbackRelease(address string) bool {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}
