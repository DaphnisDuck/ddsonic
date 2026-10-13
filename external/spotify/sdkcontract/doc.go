// Package sdkcontract holds ddsonic's contract tests against go-librespot at
// the version go.mod pins, and the runner that executes them.
//
// ddsonic's Spotify session code relies on a few properties of go-librespot
// that its exported API does not state and that cannot be made to stall
// through it: that the access point's Done channel closes before Close can
// block, that a stalled audio key request is released by it, that the first
// audio chunk request ends with its HTTP client's context, and how a decoder
// is released. The tests in testdata are compiled into a staged copy of the
// SDK's own packages, where they can reach those internals, and run there.
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
