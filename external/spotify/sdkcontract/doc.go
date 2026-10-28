// Package sdkcontract holds ddsonic's contract tests against go-librespot at
// the version go.mod pins, and the runner that executes them.
//
// ddsonic's Spotify session code relies on a few properties of go-librespot
// that its exported API does not state and that cannot be made to stall
// through it: that the access point's Done channel closes before Close can
// block, that a stalled audio key request is released by it, that the first
// audio chunk request ends with its HTTP client's context, how a decoder
// is released, and what the access point and the key provider do when the
// peer closes the connection: unserviced and serviced, with a key request in
// flight, when a reconnect fails for good, and when the access point is
// closed after a request was replaced (those run against a synthetic
// access point on loopback TCP, testdata/ap_fake_helper.go.txt). The tests
// in testdata are compiled into a staged copy of the
// SDK's own packages, where they can reach those internals, and run there.
// The SDK is the tracked local replacement (third_party/go-librespot); the
// runner first checks it against its authenticated upstream base and recorded
// patches (vehicle_test.go), and also covers the race on the player's CDN
// quarantine map (testdata/player_cdn_*.go.txt). Patch 0002's access-point
// generations and fenced key requests have tests of their own
// (testdata/ap_keylink_*.go.txt, testdata/audio_keylife_contract_test.go.txt);
// after them the runner runs the symbols gate, tools/sdkgate (gate_test.go),
// and the behavioural mutations B1 and B2, which must each turn a test red.
//
// The runner is a test behind the build tag "sdkcontract", so that it is not
// part of the routine suite: it copies and compiles the SDK from empty
// caches. It is a separate, mandatory gate: run it with
//
//	make sdk-contract
//
// for a full validation, for a release candidate, and for any change that
// touches the boundary with go-librespot or its version. See
// docs/ddsonic/acceptance.md and
// docs/ddsonic/design/spotify-token-coordinator.md, section 14.5.
package sdkcontract
