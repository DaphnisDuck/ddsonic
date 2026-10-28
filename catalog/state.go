package catalog

import "time"

// CollectionState is what the catalog holds of one synced collection:
// enough for a source to tell with one cheap request that it is current.
type CollectionState struct {
	Count     int       // members
	NewestAt  time.Time // the latest member's added_at; zero when empty
	Newest    []string  // provider IDs of the members added at NewestAt
	AppliedAt time.Time // when last read in full and written; zero if unknown
	Version   int       // Snapshot.Version of the last written snapshot
}

// CollectionHead is what a service says of a collection in one request:
// how many items it has and which it added last.
type CollectionHead struct {
	Total    int
	NewestID string // "" when empty, or the newest cannot be synced
	NewestAt time.Time
}

// Matches reports whether head describes the stored collection: the same
// number of items, with the same newest one. Adding moves the newest, so
// with an unchanged count nothing was added or removed. The one miss: an
// add in the same second as the newest one plus a removal; the periodic
// full read catches it.
func (st CollectionState) Matches(head CollectionHead) bool {
	if head.Total != st.Count {
		return false
	}
	if head.Total == 0 {
		return true
	}
	if head.NewestID == "" || !head.NewestAt.Equal(st.NewestAt) {
		return false
	}
	for _, id := range st.Newest {
		if id == head.NewestID {
			return true
		}
	}
	return false
}
