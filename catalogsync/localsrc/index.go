package localsrc

import (
	"cmp"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/catalog"
)

const (
	unknownArtist  = "Unknown Artist"
	variousArtists = "Various Artists"
)

// discDir matches per-disc subfolders ("CD1", "Disc 2") so a multi-disc
// album stays one album; the number is the disc.
var discDir = regexp.MustCompile(`(?i)^(?:cd|disc|disk)\s*(\d+)$`)

// albumDir is the folder that identifies an album on disk, and the disc
// number its folder names (1 when none).
func albumDir(path string) (string, int) {
	dir := filepath.Dir(path)
	if m := discDir.FindStringSubmatch(filepath.Base(dir)); m != nil {
		disc, _ := strconv.Atoi(m[1])
		return filepath.Dir(dir), max(disc, 1)
	}
	return dir, 1
}

// localRef is a local object's catalog identity.
func localRef(id string) catalog.Ref { return catalog.Ref{Provider: catalog.Local, ProviderID: id} }

// artistRecord identifies an artist by name, ignoring case: local files
// carry no artist IDs.
func artistRecord(name string) catalog.ArtistRecord {
	return catalog.ArtistRecord{Ref: localRef(strings.ToLower(name)), Name: name}
}

// snapshot groups files into albums (by album tag within an album folder;
// untagged files by folder), credits each album with its track artists, and
// lists every album, artist and track as a library member.
func snapshot(files []file) catalog.Snapshot {
	type album struct {
		rec    *catalog.AlbumRecord
		dir    string
		files  []file
		discOf map[string]int // path → disc
	}
	byKey := map[string]*album{}
	var albums []*album
	for _, f := range files {
		dir, disc := albumDir(f.stat.Path)
		title := f.track.Album
		if title == "" {
			title = filepath.Base(dir)
		}
		key := dir + "\x00" + strings.ToLower(title)
		a, ok := byKey[key]
		if !ok {
			a = &album{rec: &catalog.AlbumRecord{Ref: localRef(key), Title: title}, dir: dir, discOf: map[string]int{}}
			byKey[key] = a
			albums = append(albums, a)
		}
		a.files = append(a.files, f)
		a.discOf[f.stat.Path] = disc
	}

	snap := catalog.Snapshot{Provider: catalog.Local, Collection: Files, Files: true}
	artistSeen := map[string]bool{}
	for _, a := range albums {
		slices.SortStableFunc(a.files, func(x, y file) int {
			return cmp.Or(
				cmp.Compare(a.discOf[x.stat.Path], a.discOf[y.stat.Path]),
				cmp.Compare(x.track.TrackNumber, y.track.TrackNumber),
				cmp.Compare(x.stat.Path, y.stat.Path),
			)
		})
		rec := a.rec
		rec.TrackCount = len(a.files)
		credited := map[string]bool{}
		for _, f := range a.files {
			artist := artistRecord(cmp.Or(f.track.Artist, unknownArtist))
			if !credited[artist.Ref.ProviderID] {
				credited[artist.Ref.ProviderID] = true
				rec.Artists = append(rec.Artists, artist)
			}
			if !artistSeen[artist.Ref.ProviderID] {
				artistSeen[artist.Ref.ProviderID] = true
				snap.Artists = append(snap.Artists, artist)
			}
			rec.Year = max(rec.Year, f.track.Year)
		}
		rec.Credit = rec.Artists[0].Name
		if len(rec.Artists) > 1 {
			rec.Credit = variousArtists
		}
		snap.Albums = append(snap.Albums, *rec)

		for _, f := range a.files {
			t, stat := f.track, f.stat
			snap.Tracks = append(snap.Tracks, catalog.TrackRecord{
				Ref:         localRef(stat.Path),
				Title:       cmp.Or(t.Title, strings.TrimSuffix(filepath.Base(stat.Path), filepath.Ext(stat.Path))),
				Artists:     []catalog.ArtistRecord{artistRecord(cmp.Or(t.Artist, unknownArtist))},
				Album:       rec,
				Disc:        a.discOf[stat.Path],
				TrackNo:     t.TrackNumber,
				Duration:    time.Duration(t.DurationSecs) * time.Second,
				PlayableURI: stat.Path,
				Genre:       t.Genre,
				Year:        t.Year,
				File:        &stat,
			})
		}
	}
	return snap
}
