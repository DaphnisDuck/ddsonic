-- ddmus catalog schema v7: an album's cached track list as its provider
-- gave it. tracks.album_id says which album a track belongs to, and other
-- collections (liked songs, playlists) write it too; this table alone says
-- which tracks make up the album's complete list, so a shorter list
-- replaces a longer one and liked tracks cannot join it.
CREATE TABLE album_tracks (
    album_id INTEGER NOT NULL REFERENCES albums (id) ON DELETE CASCADE,
    track_id INTEGER NOT NULL REFERENCES tracks (id) ON DELETE CASCADE,
    PRIMARY KEY (album_id, track_id)
);
CREATE INDEX album_tracks_track ON album_tracks (track_id);

-- Until now a cached album's list was every track naming it.
INSERT INTO album_tracks (album_id, track_id)
SELECT album_id, id FROM tracks
WHERE album_id IN (SELECT id FROM albums WHERE tracks_cached_at IS NOT NULL);
