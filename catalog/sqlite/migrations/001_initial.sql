-- ddmus catalog schema v1. Never edit a migration once merged; add the
-- next numbered file instead. Times are Unix milliseconds.

CREATE TABLE artists (
    id          INTEGER PRIMARY KEY,
    provider    TEXT    NOT NULL,
    provider_id TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    sort_name   TEXT    NOT NULL,
    image_url   TEXT    NOT NULL DEFAULT '',
    updated_at  INTEGER NOT NULL,
    UNIQUE (provider, provider_id)
);

CREATE TABLE albums (
    id               INTEGER PRIMARY KEY,
    provider         TEXT    NOT NULL,
    provider_id      TEXT    NOT NULL,
    title            TEXT    NOT NULL,
    sort_title       TEXT    NOT NULL,
    -- Denormalized credit, written with album_artists so list queries need
    -- no per-row joins. Sort keys come from catalog.SortKey.
    artist_credit    TEXT    NOT NULL DEFAULT '',
    sort_artist      TEXT    NOT NULL DEFAULT '',
    year             INTEGER NOT NULL DEFAULT 0,
    track_count      INTEGER NOT NULL DEFAULT 0,
    artwork_url      TEXT    NOT NULL DEFAULT '',
    tracks_cached_at INTEGER,          -- NULL until the track list is cached
    updated_at       INTEGER NOT NULL,
    UNIQUE (provider, provider_id)
);

CREATE TABLE tracks (
    id           INTEGER PRIMARY KEY,
    provider     TEXT    NOT NULL,
    provider_id  TEXT    NOT NULL,
    title        TEXT    NOT NULL,
    artist_credit TEXT   NOT NULL DEFAULT '', -- denormalized, written with track_artists
    album_id     INTEGER REFERENCES albums (id) ON DELETE SET NULL,
    album_title  TEXT    NOT NULL DEFAULT '', -- display fallback without an album row
    disc         INTEGER NOT NULL DEFAULT 1,
    track_no     INTEGER NOT NULL DEFAULT 0,
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    playable_uri TEXT    NOT NULL,
    genre        TEXT    NOT NULL DEFAULT '',
    year         INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL,
    UNIQUE (provider, provider_id)
);
CREATE INDEX tracks_album ON tracks (album_id, disc, track_no);

CREATE TABLE album_artists (
    album_id  INTEGER NOT NULL REFERENCES albums (id) ON DELETE CASCADE,
    artist_id INTEGER NOT NULL REFERENCES artists (id) ON DELETE CASCADE,
    position  INTEGER NOT NULL,
    PRIMARY KEY (album_id, artist_id)
) WITHOUT ROWID;
CREATE INDEX album_artists_artist ON album_artists (artist_id);

CREATE TABLE track_artists (
    track_id  INTEGER NOT NULL REFERENCES tracks (id) ON DELETE CASCADE,
    artist_id INTEGER NOT NULL REFERENCES artists (id) ON DELETE CASCADE,
    position  INTEGER NOT NULL,
    PRIMARY KEY (track_id, artist_id)
) WITHOUT ROWID;
CREATE INDEX track_artists_artist ON track_artists (artist_id);

CREATE TABLE playlists (
    id          INTEGER PRIMARY KEY,
    provider    TEXT    NOT NULL,
    provider_id TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    own         INTEGER NOT NULL DEFAULT 0, -- 1 when the user owns it
    snapshot    TEXT    NOT NULL DEFAULT '', -- provider change marker
    track_count INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL,
    UNIQUE (provider, provider_id)
);

CREATE TABLE playlist_tracks (
    playlist_id INTEGER NOT NULL REFERENCES playlists (id) ON DELETE CASCADE,
    position    INTEGER NOT NULL,
    track_id    INTEGER NOT NULL REFERENCES tracks (id) ON DELETE CASCADE,
    PRIMARY KEY (playlist_id, position)
) WITHOUT ROWID;
CREATE INDEX playlist_tracks_track ON playlist_tracks (track_id);

-- Library membership, kept apart from the entities: an album can be in the
-- catalog (say, from a browsed discography) without being saved. Each row
-- belongs to the sync collection that saw it, and reconciling a collection
-- deletes only its own stale rows, never shared entities.
CREATE TABLE library_items (
    provider      TEXT    NOT NULL,
    collection    TEXT    NOT NULL, -- matches sync_state.collection
    kind          TEXT    NOT NULL CHECK (kind IN ('album', 'artist', 'track', 'playlist')),
    item_id       INTEGER NOT NULL, -- id in the table named by kind
    added_at      INTEGER NOT NULL,
    last_seen_gen INTEGER NOT NULL,
    PRIMARY KEY (provider, collection, kind, item_id)
) WITHOUT ROWID;
CREATE INDEX library_items_kind ON library_items (provider, kind, added_at);

CREATE TABLE sync_state (
    provider        TEXT    NOT NULL,
    collection      TEXT    NOT NULL,
    generation      INTEGER NOT NULL DEFAULT 0,
    last_attempt_at INTEGER,
    last_success_at INTEGER,
    last_error      TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (provider, collection)
);

-- Change detection for the local indexer.
CREATE TABLE local_files (
    path     TEXT    PRIMARY KEY,
    size     INTEGER NOT NULL,
    mtime_ns INTEGER NOT NULL,
    track_id INTEGER REFERENCES tracks (id) ON DELETE SET NULL
);
CREATE INDEX local_files_track ON local_files (track_id);
