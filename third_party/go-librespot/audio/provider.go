// Modified by ddsonic on 2026-10-14: a fenced request lifecycle with a writer
// and a receiver, connection-stamped requests, typed dispositions and no
// replay (patches/go-librespot/0002-audio-key-requests-across-connection-changes.patch).

package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/ap"
	"github.com/devgianlu/go-librespot/internal/keylink"
)

type KeyProviderError struct {
	Code uint16
}

func (e KeyProviderError) Error() string {
	return fmt.Sprintf("failed retrieving aes key with code %d", e.Code)
}

// ErrKeyProviderNotOwner is returned by every request to a key provider that
// is not its access point's first: the access point's announcements belong
// to that first provider.
var ErrKeyProviderNotOwner = errors.New("audio key provider does not own its access point")

// ErrKeySequenceExhausted is returned once a key provider has used every
// 32-bit request sequence number; no request was made.
var ErrKeySequenceExhausted = errors.New("audio key sequence numbers exhausted")

// ConnectionReplacedError: the request's write was stamped for connection
// generation Sent (the connection selected for it, whether or not the write
// completed), and the access point has since completed generation Now. The
// request is ended; an answer that still arrives for it is discarded.
type ConnectionReplacedError struct {
	Sent, Now uint64
}

func (e *ConnectionReplacedError) Error() string {
	return fmt.Sprintf("audio key request on connection %d was lost when connection %d replaced it", e.Sent, e.Now)
}

// KeySendError: the request's write on connection generation Gen failed
// while the access point was open.
type KeySendError struct {
	Gen uint64
	Err error
}

func (e *KeySendError) Error() string {
	return fmt.Sprintf("failed sending key request on connection %d: %v", e.Gen, e.Err)
}

func (e *KeySendError) Unwrap() error { return e.Err }

// keyWindow is how long a committed request waits for its answer, from when
// its caller sees it committed.
const keyWindow = 15 * time.Second

type keyState int

const (
	keyAdmitted  keyState = iota // queued
	keyCommitted                 // taken by the writer, not yet stamped
	keyWriting                   // stamped; its write attempted
	keySent                      // its write returned without error
	keyTerminal                  // has its outcome
)

type keyRecord struct {
	seq         uint32
	gid, fileId []byte

	state    keyState
	stamp    uint64
	expiring bool

	committed chan struct{} // closed when the writer commits the record
	done      chan struct{} // closed when the record gets its outcome

	key []byte
	err error
}

type KeyProvider struct {
	ap  *ap.Accesspoint
	log librespot.Logger

	owner     bool // set once, by the constructor
	startOnce sync.Once

	// mu is a leaf lock: nothing is acquired, called or waited for under it
	// except non-blocking channel operations.
	mu           sync.Mutex
	table        map[uint32]*keyRecord
	queue        []*keyRecord
	next         uint64
	registered   bool
	receiverGone bool
	pendingGen   uint64
	unknown      uint64 // answers for no live request, discarded

	kickW, kickR chan struct{}

	// Test seams: production has the access point's subscription and no
	// hooks. A test sets them before the provider's first request.
	subscribe func() <-chan ap.Packet
	hooks     *keyHooks
}

// keyHooks are test barriers and observations, each run with no lock held
// unless its name ends in Locked.
type keyHooks struct {
	expiry       func(seq uint32) <-chan time.Time // the request's window, in place of keyWindow
	beforeAdmit  func()                            // between E4 and E5
	beforeWait   func(seq uint32)                  // after the caller saw the commit
	beforeStamp  func(seq uint32, gen uint64)      // in the stamp callback, before L3.5
	afterStamp   func(seq uint32)                  // after L3.5, before the write
	afterSend    func(seq uint32, err error)       // the write returned, before L4
	beforeStep0  func()
	beforeStep1  func()            // after step 0 found the access point open
	afterReceipt func(site string) // a packet received at "step1" or "step3", before L5
	betweenL6L7  func()
	l7Locked     func() // holding p.mu, before L7
	passDone     func() // after step 2
	inStep3      func() // before the step-3 select
	exited       func(site string)
}

func NewAudioKeyProvider(log librespot.Logger, a *ap.Accesspoint) *KeyProvider {
	p := &KeyProvider{
		log:   log,
		ap:    a,
		table: map[uint32]*keyRecord{},
		kickW: make(chan struct{}, 1),
		kickR: make(chan struct{}, 1),
	}
	p.subscribe = func() <-chan ap.Packet { return a.Receive(ap.PacketTypeAesKey, ap.PacketTypeAesKeyError) }
	p.owner = ownerFrom(keylink.AttachAnnouncer(a, p.announce))
	return p
}

// ownerFrom: only a nil error makes a provider its access point's owner.
func ownerFrom(err error) bool { return err == nil }

// announce records the connection generation the access point is about to
// publish, and nothing else.
func (p *KeyProvider) announce(next uint64) {
	p.mu.Lock()
	if next > p.pendingGen {
		p.pendingGen = next
	}
	p.mu.Unlock()
}

func kick(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

func (p *KeyProvider) Request(ctx context.Context, gid []byte, fileId []byte) ([]byte, error) {
	// E1-E3, before anything is launched.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.owner {
		return nil, ErrKeyProviderNotOwner
	}
	done := p.ap.Done()
	select {
	case <-done:
		return nil, ap.ErrAccesspointClosed
	default:
	}

	// E4
	p.startOnce.Do(func() {
		go p.writer()
		go p.receiver()
	})
	if h := p.hooks; h != nil && h.beforeAdmit != nil {
		h.beforeAdmit()
	}

	// E5
	r, err := p.admit(ctx, gid, fileId, done)
	if err != nil {
		return nil, err
	}
	kick(p.kickW)

	select {
	case <-r.done:
		return p.outcome(r)
	case <-ctx.Done():
		return p.terminate(r, ctx.Err())
	case <-done:
		return p.terminate(r, ap.ErrAccesspointClosed)
	case <-r.committed:
	}

	var expiry <-chan time.Time
	if h := p.hooks; h != nil && h.expiry != nil {
		expiry = h.expiry(r.seq)
	} else {
		timer := time.NewTimer(keyWindow)
		defer timer.Stop()
		expiry = timer.C
	}
	if h := p.hooks; h != nil && h.beforeWait != nil {
		h.beforeWait(r.seq)
	}
	for {
		select {
		case <-r.done:
			return p.outcome(r)
		case <-ctx.Done():
			return p.terminate(r, ctx.Err())
		case <-done:
			return p.terminate(r, ap.ErrAccesspointClosed)
		case <-expiry:
			expiry = nil
			p.mu.Lock()
			if p.receiverGone {
				p.terminateLocked(r, nil, context.DeadlineExceeded)
			} else {
				r.expiring = true // the receiver decides (L7)
			}
			p.mu.Unlock()
			kick(p.kickR)
		}
	}
}

// admit is L1.
func (p *KeyProvider) admit(ctx context.Context, gid, fileId []byte, done <-chan struct{}) (*keyRecord, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-done:
		return nil, ap.ErrAccesspointClosed
	default:
	}
	if p.next >= 1<<32 {
		return nil, ErrKeySequenceExhausted
	}
	r := &keyRecord{
		seq:       uint32(p.next),
		gid:       gid,
		fileId:    fileId,
		committed: make(chan struct{}),
		done:      make(chan struct{}),
	}
	p.next++
	p.table[r.seq] = r
	p.queue = append(p.queue, r)
	return r, nil
}

// terminateLocked is L2, the record's one terminal transition: the first
// outcome stands and later ones are ignored.
func (p *KeyProvider) terminateLocked(r *keyRecord, key []byte, err error) {
	if r.state == keyTerminal {
		return
	}
	r.state, r.key, r.err = keyTerminal, key, err
	delete(p.table, r.seq)
	for i, q := range p.queue {
		if q == r {
			p.queue = append(p.queue[:i], p.queue[i+1:]...)
			break
		}
	}
	close(r.done)
}

// terminate ends r with err unless it already has an outcome, and returns
// the outcome that stands.
func (p *KeyProvider) terminate(r *keyRecord, err error) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.terminateLocked(r, nil, err)
	return r.key, r.err
}

func (p *KeyProvider) outcome(r *keyRecord) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return r.key, r.err
}

// writer commits queued requests in order, once the receiver is subscribed,
// and writes each one once.
func (p *KeyProvider) writer() {
	done := p.ap.Done()
	for {
		select {
		case <-p.kickW:
		case <-done:
			return
		}

		for {
			r, payload := p.commit()
			if r == nil {
				break
			}

			// Background on purpose: one caller's cancellation ends that
			// caller's wait, not a write that may be under way.
			gen, err := p.ap.SendStamped(context.Background(), ap.PacketTypeRequestKey, payload, func(g uint64) {
				if h := p.hooks; h != nil && h.beforeStamp != nil {
					h.beforeStamp(r.seq, g)
				}
				p.stampWriting(r, g)
				if h := p.hooks; h != nil && h.afterStamp != nil {
					h.afterStamp(r.seq)
				}
			})
			if h := p.hooks; h != nil && h.afterSend != nil {
				h.afterSend(r.seq, err)
			}
			p.written(r, gen, err)
		}
	}
}

// commit is L3: it takes the oldest admitted record, if the receiver is
// subscribed, and builds its request.
func (p *KeyProvider) commit() (*keyRecord, []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.registered || len(p.queue) == 0 {
		return nil, nil
	}
	r := p.queue[0]
	p.queue = p.queue[1:]
	r.state = keyCommitted
	close(r.committed)

	var buf bytes.Buffer
	_, _ = buf.Write(r.fileId)
	_, _ = buf.Write(r.gid)
	_ = binary.Write(&buf, binary.BigEndian, r.seq)
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))
	return r, buf.Bytes()
}

// stampWriting is L3.5: the record learns the generation of the connection
// selected for its write, before the write is attempted.
func (p *KeyProvider) stampWriting(r *keyRecord, g uint64) {
	p.mu.Lock()
	if r.state == keyCommitted {
		r.state, r.stamp = keyWriting, g
	}
	p.mu.Unlock()
	kick(p.kickR)
}

// written is L4. Whether the access point is done is read here, in the
// deciding section: a write that failed while it was open is ErrAccesspointClosed
// if it is done by now. An earlier terminal stands.
func (p *KeyProvider) written(r *keyRecord, gen uint64, err error) {
	if err == nil {
		p.log.Debugf("requested aes key for file %s, gid: %s", hex.EncodeToString(r.fileId), librespot.GidToBase62(r.gid))
	}
	done := p.ap.Done()
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case r.state == keyTerminal:
	case err == nil:
		if r.state == keyWriting {
			r.state = keySent
		}
	case errors.Is(err, ap.ErrAccesspointClosed) || isClosed(done):
		p.terminateLocked(r, nil, ap.ErrAccesspointClosed)
	default:
		p.terminateLocked(r, nil, &KeySendError{Gen: gen, Err: err})
	}
}

func isClosed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// receiver is the provider's one subscriber and its one classifier: answers
// (L5), then requests whose connection was replaced (L6), then expired
// requests (L7).
func (p *KeyProvider) receiver() {
	ch := p.subscribe()
	p.mu.Lock()
	p.registered = true
	p.mu.Unlock()
	kick(p.kickW)

	done := p.ap.Done()
	h := p.hooks
	for {
		if h != nil && h.beforeStep0 != nil {
			h.beforeStep0()
		}

		// step 0
		select {
		case <-done:
			p.fenceAll("step0-done")
			return
		default:
		}

		// step 1
		if h != nil && h.beforeStep1 != nil {
			h.beforeStep1()
		}
		accepted := false
		select {
		case pkt, ok := <-ch:
			if !ok {
				p.fenceAll("step1-closed")
				return
			}
			if h != nil && h.afterReceipt != nil {
				h.afterReceipt("step1")
			}
			p.answer(pkt)
			accepted = true
		default:
		}

		// step 2
		g, changed := p.ap.Generation()
		p.classify(g)
		if h != nil && h.passDone != nil {
			h.passDone()
		}
		if accepted {
			continue
		}

		// step 3
		if h != nil && h.inStep3 != nil {
			h.inStep3()
		}
		select {
		case pkt, ok := <-ch:
			if !ok {
				p.fenceAll("step3-closed")
				return
			}
			if h != nil && h.afterReceipt != nil {
				h.afterReceipt("step3")
			}
			p.answer(pkt) // classified at the next step 2
		case <-p.kickR:
		case <-changed:
		case <-done:
			p.fenceAll("step3-done")
			return
		}
	}
}

// fenceAll ends every live request with ErrAccesspointClosed as the receiver
// leaves.
func (p *KeyProvider) fenceAll(site string) {
	p.mu.Lock()
	p.receiverGone = true
	for _, r := range p.table {
		p.terminateLocked(r, nil, ap.ErrAccesspointClosed)
	}
	p.mu.Unlock()
	if h := p.hooks; h != nil && h.exited != nil {
		h.exited(site)
	}
}

// answer is L5.
func (p *KeyProvider) answer(pkt ap.Packet) {
	resp := bytes.NewReader(pkt.Payload)
	var seq uint32
	_ = binary.Read(resp, binary.BigEndian, &seq)

	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.table[seq]
	if !ok {
		p.unknown++
		return
	}

	switch pkt.Type {
	case ap.PacketTypeAesKey:
		key := make([]byte, 16)
		if _, err := io.ReadFull(resp, key); err != nil {
			p.terminateLocked(r, nil, fmt.Errorf("malformed aes key response for sequence %d: %w", seq, err))
			return
		}
		p.terminateLocked(r, key, nil)
	case ap.PacketTypeAesKeyError:
		var errCode uint16
		_ = binary.Read(resp, binary.BigEndian, &errCode)
		p.terminateLocked(r, nil, &KeyProviderError{errCode})
	default:
		panic("unexpected packet type")
	}
}

// classify is L6 then L7, against completed generation g, each in its own
// section of p.mu.
func (p *KeyProvider) classify(g uint64) {
	p.mu.Lock()
	for _, r := range p.table {
		if (r.state == keyWriting || r.state == keySent) && r.stamp < g {
			p.terminateLocked(r, nil, &ConnectionReplacedError{Sent: r.stamp, Now: g})
		}
	}
	p.mu.Unlock()

	h := p.hooks
	if h != nil && h.betweenL6L7 != nil {
		h.betweenL6L7()
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if h != nil && h.l7Locked != nil {
		h.l7Locked()
	}
	for _, r := range p.table {
		if !r.expiring {
			continue
		}
		stamped := r.state == keyWriting || r.state == keySent
		if stamped && r.stamp < max(g, p.pendingGen) {
			continue // a newer connection is announced: L6 decides
		}
		p.terminateLocked(r, nil, context.DeadlineExceeded)
	}
}
