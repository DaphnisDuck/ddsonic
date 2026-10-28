package model

import (
	"encoding/json"
	"testing"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// An unknown shuffle or repeat name fails; upstream toggled or cycled on it.
func TestV2ModeRejectsUnknownName(t *testing.T) {
	for _, op := range []string{"shuffle", "repeat"} {
		engine := &playbackFakeEngine{}
		pl := playlist.New()
		pl.Add(playlist.Track{Path: "/music/one.flac", Title: "One"})
		m := Model{player: engine, playlist: pl, vis: ui.NewVisualizer(float64(engine.SampleRate()))}
		jobs := ipc.NewJobStore()
		job, err := jobs.Create(op)
		if err != nil {
			t.Fatal(err)
		}
		shuffled, repeat := pl.Shuffled(), pl.Repeat()
		_, _ = m.Update(V2RequestMsg{
			Request: ipc.V2Request{Operation: op, Params: json.RawMessage(`{"name":"bogus"}`)},
			Jobs:    jobs,
			JobID:   job.ID,
		})
		done, ok := jobs.Get(job.ID)
		if !ok || done.State != ipc.JobFailed || done.Error == nil || done.Error.Code != ipc.V2ErrorCodeInvalidParams {
			t.Fatalf("%s bogus: job=%#v", op, done)
		}
		if pl.Shuffled() != shuffled || pl.Repeat() != repeat {
			t.Errorf("%s bogus changed the setting", op)
		}
	}
}
