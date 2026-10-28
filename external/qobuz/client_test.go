package qobuz

import (
	"testing"
)

func TestMD5Hex(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "d41d8cd98f00b204e9800998ecf8427e"},
		{"abc", "900150983cd24fb0d6963f7d28e17f72"},
	}
	for _, tt := range tests {
		if got := md5hex(tt.in); got != tt.want {
			t.Errorf("md5hex(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestTrackFileURLSig pins the request_sig layout for track/getFileUrl against
// a precomputed md5. If the raw string format in trackFileURLSig changes,
// streaming breaks and this test fails.
func TestTrackFileURLSig(t *testing.T) {
	// md5("trackgetFileUrlformat_id6intentstreamtrack_id59667831700000000deadbeefsecret")
	const want = "bc7a09d686b3e5c1cd32f5268eff1030"
	got := trackFileURLSig("5966783", 6, "1700000000", "deadbeefsecret")
	if got != want {
		t.Fatalf("trackFileURLSig = %q, want %q", got, want)
	}
}

func TestValidQuality(t *testing.T) {
	for _, q := range []int{5, 6, 7, 27} {
		if !validQuality(q) {
			t.Errorf("expected quality %d to be valid", q)
		}
	}
	for _, q := range []int{0, 1, 4, 8, 100} {
		if validQuality(q) {
			t.Errorf("expected quality %d to be invalid", q)
		}
	}
}
