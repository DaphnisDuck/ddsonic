package catalog

import (
	"testing"
	"time"
)

func TestCollectionStateMatches(t *testing.T) {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	stored := CollectionState{Count: 3, NewestAt: at, Newest: []string{"a", "b"}}
	tests := []struct {
		name  string
		st    CollectionState
		head  CollectionHead
		match bool
	}{
		{"same", stored, CollectionHead{Total: 3, NewestID: "b", NewestAt: at}, true},
		{"added", stored, CollectionHead{Total: 4, NewestID: "c", NewestAt: at.Add(time.Second)}, false},
		{"added one, removed one", stored, CollectionHead{Total: 3, NewestID: "c", NewestAt: at.Add(time.Second)}, false},
		{"removed", stored, CollectionHead{Total: 2, NewestID: "a", NewestAt: at}, false},
		{"re-added the newest", stored, CollectionHead{Total: 3, NewestID: "a", NewestAt: at.Add(time.Minute)}, false},
		{"newest not syncable", stored, CollectionHead{Total: 3, NewestAt: at}, false},
		{"both empty", CollectionState{}, CollectionHead{}, true},
		{"emptied", stored, CollectionHead{}, false},
	}
	for _, tt := range tests {
		if got := tt.st.Matches(tt.head); got != tt.match {
			t.Errorf("%s: Matches = %v, want %v", tt.name, got, tt.match)
		}
	}
}
