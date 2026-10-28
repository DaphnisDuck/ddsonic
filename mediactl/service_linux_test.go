//go:build linux

package mediactl

import (
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/godbus/dbus/v5"

	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/internal/playback"
)

// discardTransport is a D-Bus transport that drops every write. It lets
// the tests run the godbus property code without a session bus.
type discardTransport struct{}

func (discardTransport) Read([]byte) (int, error)    { return 0, io.EOF }
func (discardTransport) Write(p []byte) (int, error) { return len(p), nil }
func (discardTransport) Close() error                { return nil }

func newTestService(t *testing.T, send func(tea.Msg)) *Service {
	t.Helper()
	conn, err := dbus.NewConn(discardTransport{})
	if err != nil {
		t.Fatalf("dbus.NewConn() error = %v", err)
	}
	svc, err := newService(conn, send)
	if err != nil {
		t.Fatalf("newService() error = %v", err)
	}
	t.Cleanup(svc.Close)
	return svc
}

// returnsWithin fails the test when fn does not return within 2 seconds.
func returnsWithin(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not return while send was blocked", what)
	}
}

// TestServiceCallbacksDoNotWaitForSend holds send blocked, as prog.Send
// blocks until the event loop reads the message. Each D-Bus call and the
// event loop Update must still return, and the messages must keep their order.
func TestServiceCallbacksDoNotWaitForSend(t *testing.T) {
	const playerName = "org.mpris.MediaPlayer2.Player"
	type call struct {
		name string
		do   func(*Service) *dbus.Error
	}
	setVolume := func(v float64) call {
		return call{name: "Set Volume", do: func(s *Service) *dbus.Error {
			return s.props.Set(playerName, "Volume", dbus.MakeVariant(v))
		}}
	}
	next := call{name: "Next", do: func(s *Service) *dbus.Error { return playerIface{s}.Next() }}
	playPause := call{name: "PlayPause", do: func(s *Service) *dbus.Error { return playerIface{s}.PlayPause() }}
	seek := call{name: "Seek", do: func(s *Service) *dbus.Error { return playerIface{s}.DoSeek(5_000_000) }}

	tests := []struct {
		name  string
		calls []call
		want  []tea.Msg
	}{
		{
			name:  "one volume change",
			calls: []call{setVolume(0.5)},
			want:  []tea.Msg{playback.SetVolumeMsg{VolumeDB: linearToDb(0.5)}},
		},
		{
			name:  "rapid volume changes keep order",
			calls: []call{setVolume(0.2), setVolume(0.9), setVolume(0.5), setVolume(0.7)},
			want: []tea.Msg{
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.2)},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.9)},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.5)},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.7)},
			},
		},
		{
			name:  "out of range volume clamps",
			calls: []call{setVolume(1.5), setVolume(-0.5)},
			want: []tea.Msg{
				playback.SetVolumeMsg{VolumeDB: linearToDb(1)},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0)},
			},
		},
		{
			name:  "methods and volume keep order",
			calls: []call{next, setVolume(0.3), playPause, seek, setVolume(0.6)},
			want: []tea.Msg{
				playback.NextMsg{},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.3)},
				playback.PlayPauseMsg{},
				playback.SeekMsg{Offset: 5 * time.Second},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.6)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := make(chan struct{})
			got := make(chan tea.Msg, len(tt.want))
			svc := newTestService(t, func(msg tea.Msg) {
				<-release
				got <- msg
			})

			for _, c := range tt.calls {
				returnsWithin(t, c.name, func() {
					if err := c.do(svc); err != nil {
						t.Errorf("%s error = %v", c.name, err)
					}
				})
			}
			// The event loop calls Update, which takes the godbus
			// Properties lock that the Volume Set held.
			returnsWithin(t, "Update", func() {
				svc.Update(playback.State{Status: playback.StatusPlaying, VolumeDB: -12})
			})

			close(release)
			for i, want := range tt.want {
				select {
				case msg := <-got:
					if msg != want {
						t.Fatalf("message %d = %#v, want %#v", i, msg, want)
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("message %d not sent, want %#v", i, want)
				}
			}
			select {
			case msg := <-got:
				t.Fatalf("unexpected extra message %#v", msg)
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}

// TestServiceUpdateAfterConnectionLoss checks that Update does not panic
// after the bus connection drops. godbus then fails each property emit, and
// SetMust panics. The model calls Update on the event loop, so a panic there
// ends ddsonic in the middle of playback. (Upstream 23c66b70.)
func TestServiceUpdateAfterConnectionLoss(t *testing.T) {
	base := playback.State{Status: playback.StatusPaused, VolumeDB: -12, Seekable: true}
	tests := []struct {
		name   string
		change func(*playback.State)
	}{
		{name: "status", change: func(s *playback.State) { s.Status = playback.StatusPlaying }},
		{name: "track", change: func(s *playback.State) { s.Track.Title = "Next" }},
		{name: "volume", change: func(s *playback.State) { s.VolumeDB = -6 }},
		{name: "can seek", change: func(s *playback.State) { s.Seekable = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t, func(tea.Msg) {})
			svc.Update(base)
			svc.conn.Close()

			state := base
			tt.change(&state)
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Update panicked after the connection dropped: %v", r)
				}
			}()
			svc.Update(state)
		})
	}
}

// TestServicePublishAfterConnectionLoss checks the guard for a connection
// that drops while Update publishes.
func TestServicePublishAfterConnectionLoss(t *testing.T) {
	svc := newTestService(t, func(tea.Msg) {})
	svc.conn.Close()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("publish panicked after the connection dropped: %v", r)
		}
	}()
	svc.publish("PlaybackStatus", string(playback.StatusPlaying))
}

// TestServiceNamesItsDesktopEntry checks the property a desktop's media
// controls read to find the player's launcher entry and icon. ddsonic.
func TestServiceNamesItsDesktopEntry(t *testing.T) {
	svc := newTestService(t, func(tea.Msg) {})

	v, err := svc.props.Get("org.mpris.MediaPlayer2", "DesktopEntry")
	if err != nil {
		t.Fatalf("Get(DesktopEntry) error = %v", err)
	}
	if got, want := v.Value(), any(appmeta.DesktopEntry()); got != want {
		t.Errorf("DesktopEntry = %v, want %v", got, want)
	}
	if !strings.Contains(introspectXML, `<property name="DesktopEntry" type="s" access="read"/>`) {
		t.Error("the introspection data does not declare DesktopEntry")
	}
}
