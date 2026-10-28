package localsrc

import (
	"sync"

	"github.com/bjarneo/cliamp/playlist"
)

// tagWorkers is how many files are read at once.
const tagWorkers = 8

// readTags reads each file's tags, in order, on a few workers. A file whose
// tags crash the tag reader is indexed by its file name, so one bad file
// cannot take down the index at every startup.
func readTags(paths []string) []playlist.Track {
	tracks := make([]playlist.Track, len(paths))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(len(paths), tagWorkers) {
		wg.Go(func() {
			for i := range next {
				tracks[i] = trackFromPath(paths[i])
			}
		})
	}
	for i := range paths {
		next <- i
	}
	close(next)
	wg.Wait()
	return tracks
}

func trackFromPath(path string) (t playlist.Track) {
	defer func() {
		if recover() != nil {
			t = playlist.TrackFromFilename(path)
		}
	}()
	return tagReader(path)
}

// tagReader reads one file's tags; replaced in tests.
var tagReader = playlist.TrackFromPath
