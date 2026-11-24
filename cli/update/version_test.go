package update

import "testing"

func TestReleaseNewerThanInstalledVersion(t *testing.T) {
	for _, tc := range []struct {
		release, installed string
		want               bool
	}{
		{"0.6.0", "0.6.0-rc.6-local.2", true},
		{"0.6.0-rc.10", "0.6.0-rc.9", true},
		{"0.6.0-rc.7", "0.6.0-rc.7", false},
		{"0.6.0-rc.7", "0.6.0", false},
		{"0.9.0", "0.10.0", false},
		{"0.6.0", "dev", false},
	} {
		if got := (Release{Version: tc.release}).NewerThan(tc.installed); got != tc.want {
			t.Errorf("%s newer than %s: got %t want %t", tc.release, tc.installed, got, tc.want)
		}
	}
}
