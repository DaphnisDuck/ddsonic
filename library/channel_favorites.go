package library

// ddsonic: favorite cliamp radio channels (issue #135). A channel favorite is
// the channel's ID and the name it was saved under; it is not a station, so
// it is a Play row (Enter queues the channel's songs, as in Browse
// Stations), never a Track row.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
)

// ChannelRef names a cliamp radio channel: ID is its identity, Name the name
// a row shows (the channel's own in Browse, the saved one in Favorites).
type ChannelRef struct {
	ID   string
	Name string
}

// channelMarker is a channel favorite's Detail, which tells it apart from a
// station in Favorites and in search.
const channelMarker = "cliamp radio"

// ErrChannelGone is a channel favorite's channel that a successfully loaded
// channel list no longer has. The favorite stays; f removes it.
var ErrChannelGone = errors.New("channel no longer available")

// channelRow is the Browse Stations row of a channel: Enter queues its songs
// (or, upstream's behavior, its stream when it has none). Only a channel
// with songs can be a favorite, so only its row carries Channel.
func channelRow(channels playlist.Provider, info playlist.PlaylistInfo) Entry {
	e := Entry{Title: info.Name, Play: func(context.Context) ([]playlist.Track, error) {
		return channels.Tracks(info.ID)
	}}
	if info.TrackCount > 0 {
		e.Channel = &ChannelRef{ID: info.ID, Name: info.Name}
	}
	return e
}

// channelFavoriteEntry is a channel favorite's row, under its saved name.
func channelFavoriteEntry(channels playlist.Provider, ref ChannelRef) Entry {
	return Entry{
		ID: "channel:" + ref.ID, Title: ref.Name, Detail: channelMarker,
		Channel: &ref, Play: playChannel(channels, ref.ID),
	}
}

// playChannel resolves a channel favorite by its ID to the channel's songs.
// A channel list that fails to load is the existing, temporary load error;
// a list that loads without the channel is ErrChannelGone. A channel that
// has no songs now is an error, never its live stream.
func playChannel(channels playlist.Provider, id string) func(context.Context) ([]playlist.Track, error) {
	return func(context.Context) ([]playlist.Track, error) {
		infos, err := channels.Playlists()
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(infos, func(info playlist.PlaylistInfo) bool { return info.ID == id }) {
			return nil, ErrChannelGone
		}
		tracks, err := channels.Tracks(id)
		if err != nil {
			return nil, err
		}
		if len(tracks) == 0 || slices.ContainsFunc(tracks, func(t playlist.Track) bool { return t.Realtime }) {
			return nil, errors.New("the channel has no songs")
		}
		return tracks, nil
	}
}

// radioFavorites is Radio → Favorites: the starred stations and the
// favorite channels in one list, A–Z by the name each row shows.
func radioFavorites(stations favoriteStations, channels playlist.Provider, channelFavs func() []ChannelRef) func(context.Context) ([]Entry, error) {
	return func(context.Context) ([]Entry, error) {
		var entries []Entry
		if stations != nil {
			entries = trackEntries(stations.FavoriteTracks())
		}
		if channels != nil && channelFavs != nil {
			for _, ref := range channelFavs() {
				entries = append(entries, channelFavoriteEntry(channels, ref))
			}
		}
		return sortedByName(entries, func(e Entry) string { return e.Title }, func(e Entry) string {
			if e.Track != nil {
				return e.Track.Path
			}
			return e.ID
		}), nil
	}
}

// channelFavoriteMatches lists the favorite channels whose saved name
// matches q's text, for search's Stations, or none when q cannot want a
// station from radio. Only the title field applies: a channel has no
// artist, album or genre.
func channelFavoriteMatches(q catalog.Query, channels playlist.Provider, channelFavs func() []ChannelRef) []Entry {
	if channels == nil || channelFavs == nil || q.Empty() || !q.Wants(catalog.SearchStation) {
		return nil
	}
	if len(q.Providers) > 0 && !slices.Contains(q.Providers, catalog.Radio) {
		return nil
	}
	var rows []Entry
	for _, ref := range channelFavs() {
		if channelNameMatches(ref.Name, q.Terms) {
			rows = append(rows, channelFavoriteEntry(channels, ref))
		}
	}
	for i := range rows {
		rows[i].ID = "station:" + rows[i].ID
		rows[i].Source = catalog.Radio
	}
	return sortedByName(rows, func(e Entry) string { return e.Title }, func(e Entry) string { return e.ID })
}

// channelNameMatches applies search's word rules (catalog/sqlite ftsMatch)
// to a name: every term must match, its words in order, the last as a
// prefix unless it is a single character, which matches only a whole word.
// Case, accents and punctuation are ignored.
func channelNameMatches(name string, terms []catalog.Term) bool {
	words := searchWords(name)
	for _, t := range terms {
		if t.Field != catalog.FieldAny && t.Field != catalog.FieldTitle {
			return false
		}
		want := searchWords(t.Text)
		if len(want) == 0 || !wordsMatch(words, want) {
			return false
		}
	}
	return len(terms) > 0
}

// searchWords splits s into the words the catalog's tokenizer keeps: runs
// of letters and digits, case and accents folded.
func searchWords(s string) []string {
	return strings.FieldsFunc(catalog.SortKey(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// wordsMatch reports whether want occurs in words in order, each word whole
// but the last, which is a prefix when it is longer than one character.
func wordsMatch(words, want []string) bool {
	last := len(want) - 1
	for i := 0; i+len(want) <= len(words); i++ {
		ok := true
		for j, w := range want {
			got := words[i+j]
			if j == last && len([]rune(w)) > 1 {
				ok = strings.HasPrefix(got, w)
			} else {
				ok = got == w
			}
			if !ok {
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// withChannels puts the matching favorite channels ahead of the Stations
// results: they are favorites, which come first among stations. Other
// sections are rows as they are.
func (s *catalogView) withChannels(kind catalog.SearchKind, channels, rows []Entry) []Entry {
	if kind != catalog.SearchStation || len(channels) == 0 {
		return rows
	}
	return append(slices.Clip(channels), rows...)
}
