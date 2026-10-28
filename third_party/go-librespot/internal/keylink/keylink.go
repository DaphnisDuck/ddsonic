// Added by ddsonic on 2026-10-14: the module-internal link between an access
// point and its one audio key provider
// (patches/go-librespot/0002-audio-key-requests-across-connection-changes.patch).

// Package keylink lets package audio register a key provider's
// pre-publication announcer with an access point, without a public API.
//
// The restriction to package audio's constructor is a source invariant that
// ddsonic's SDK gate checks; it is not runtime access control: any package
// that may import this one can call or overwrite AttachAnnouncer.
package keylink

import "errors"

// ErrTaken: the access point already has its key announcer.
var ErrTaken = errors.New("keylink: access point already has a key announcer")

// AttachAnnouncer is installed by package ap's init and is nil before it. It
// attaches f as the announcer of the access point a (an *ap.Accesspoint), at
// most once per access point, under that access point's connection lock. Its
// supported caller is audio.NewAudioKeyProvider, with a non-nil access point
// and the provider's own non-nil announce method.
var AttachAnnouncer func(a any, f func(next uint64)) error
