package qobuz

import "encoding/json"

// apiArtist is the Qobuz artist object.
type apiArtist struct {
	ID          json.Number `json:"id"`
	Name        string      `json:"name"`
	AlbumsCount int         `json:"albums_count"`
}

// apiGenre is the Qobuz genre object.
type apiGenre struct {
	Name string `json:"name"`
}

// apiAlbum is the Qobuz album object. Tracks is populated only when the request
// asks for the "tracks" extra (e.g. album/get).
type apiAlbum struct {
	ID                  string        `json:"id"`
	Title               string        `json:"title"`
	TracksCount         int           `json:"tracks_count"`
	Duration            int           `json:"duration"`
	ReleaseDateOriginal string        `json:"release_date_original"`
	Genre               apiGenre      `json:"genre"`
	Artist              apiArtist     `json:"artist"`
	Tracks              *apiTrackList `json:"tracks"`
}

// apiTrack is the Qobuz track object. Album is present in search and playlist
// responses but absent when the track is nested inside an album/get response.
type apiTrack struct {
	ID          json.Number `json:"id"`
	Title       string      `json:"title"`
	TrackNumber int         `json:"track_number"`
	Duration    int         `json:"duration"`
	Streamable  bool        `json:"streamable"`
	Performer   apiArtist   `json:"performer"`
	Album       *apiAlbum   `json:"album"`
}

// apiTrackList is a paginated list of tracks.
type apiTrackList struct {
	Items []apiTrack `json:"items"`
	Total int        `json:"total"`
}

// apiAlbumList is a paginated list of albums.
type apiAlbumList struct {
	Items []apiAlbum `json:"items"`
	Total int        `json:"total"`
}

// apiArtistList is a paginated list of artists.
type apiArtistList struct {
	Items []apiArtist `json:"items"`
	Total int         `json:"total"`
}

// apiPlaylist is the Qobuz playlist object.
type apiPlaylist struct {
	ID          json.Number   `json:"id"`
	Name        string        `json:"name"`
	TracksCount int           `json:"tracks_count"`
	Duration    int           `json:"duration"`
	Tracks      *apiTrackList `json:"tracks"`
}

// apiPlaylistList is a paginated list of playlists.
type apiPlaylistList struct {
	Items []apiPlaylist `json:"items"`
	Total int           `json:"total"`
}
