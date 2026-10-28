-- ddmus catalog schema v2: full-text search. One FTS5 table per kind,
-- keyed by the entity's catalog ID (rowid = id), kept in step with the
-- entity tables by triggers. Never edit a migration once merged.
--
-- unicode61 with remove_diacritics 2 folds case and accents ("dvorak"
-- finds "Dvořák"); the prefix indexes keep as-you-type prefix queries fast
-- (one-character words match whole words only, so they need none).
-- Update triggers fire only when a searchable value changes, so a sync that
-- rewrites unchanged rows leaves the index alone. Each table also carries
-- the provider, unindexed, so a source filter applies inside the FTS scan.

CREATE VIRTUAL TABLE artists_fts USING fts5(
    name, provider UNINDEXED,
    tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3'
);

CREATE VIRTUAL TABLE albums_fts USING fts5(
    title, artist, provider UNINDEXED,
    tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3'
);

-- album is the album's title, or the track's own album_title without one.
CREATE VIRTUAL TABLE tracks_fts USING fts5(
    title, artist, album, genre, provider UNINDEXED,
    tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3'
);

CREATE VIRTUAL TABLE playlists_fts USING fts5(
    name, provider UNINDEXED,
    tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3'
);

-- Each table ranks with bm25 and its column weights (a title outweighs an
-- artist, which outweighs an album, then a genre). Queries order by the
-- rank column, which FTS5 sorts itself, faster than calling bm25().
INSERT INTO artists_fts (artists_fts, rank) VALUES ('rank', 'bm25()');
INSERT INTO albums_fts (albums_fts, rank) VALUES ('rank', 'bm25(10.0, 5.0)');
INSERT INTO tracks_fts (tracks_fts, rank) VALUES ('rank', 'bm25(10.0, 5.0, 3.0, 1.0)');
INSERT INTO playlists_fts (playlists_fts, rank) VALUES ('rank', 'bm25()');

-- Artists.
CREATE TRIGGER artists_fts_insert AFTER INSERT ON artists BEGIN
    INSERT INTO artists_fts (rowid, name, provider) VALUES (NEW.id, NEW.name, NEW.provider);
END;
CREATE TRIGGER artists_fts_delete AFTER DELETE ON artists BEGIN
    DELETE FROM artists_fts WHERE rowid = OLD.id;
END;
CREATE TRIGGER artists_fts_update AFTER UPDATE OF name ON artists
WHEN OLD.name IS NOT NEW.name BEGIN
    UPDATE artists_fts SET name = NEW.name WHERE rowid = NEW.id;
END;

-- Albums. A retitle also updates the album column of its tracks.
CREATE TRIGGER albums_fts_insert AFTER INSERT ON albums BEGIN
    INSERT INTO albums_fts (rowid, title, artist, provider) VALUES (NEW.id, NEW.title, NEW.artist_credit, NEW.provider);
END;
CREATE TRIGGER albums_fts_delete AFTER DELETE ON albums BEGIN
    DELETE FROM albums_fts WHERE rowid = OLD.id;
END;
CREATE TRIGGER albums_fts_update AFTER UPDATE OF title, artist_credit ON albums
WHEN OLD.title IS NOT NEW.title OR OLD.artist_credit IS NOT NEW.artist_credit BEGIN
    UPDATE albums_fts SET title = NEW.title, artist = NEW.artist_credit WHERE rowid = NEW.id;
END;
CREATE TRIGGER albums_fts_retitle_tracks AFTER UPDATE OF title ON albums
WHEN OLD.title IS NOT NEW.title BEGIN
    UPDATE tracks_fts SET album = NEW.title
    WHERE rowid IN (SELECT id FROM tracks WHERE album_id = NEW.id);
END;

-- Tracks.
CREATE TRIGGER tracks_fts_insert AFTER INSERT ON tracks BEGIN
    INSERT INTO tracks_fts (rowid, title, artist, album, genre, provider)
    VALUES (NEW.id, NEW.title, NEW.artist_credit,
            COALESCE((SELECT title FROM albums WHERE id = NEW.album_id), NEW.album_title), NEW.genre, NEW.provider);
END;
CREATE TRIGGER tracks_fts_delete AFTER DELETE ON tracks BEGIN
    DELETE FROM tracks_fts WHERE rowid = OLD.id;
END;
CREATE TRIGGER tracks_fts_update AFTER UPDATE OF title, artist_credit, album_id, album_title, genre ON tracks
WHEN OLD.title IS NOT NEW.title OR OLD.artist_credit IS NOT NEW.artist_credit
    OR OLD.album_id IS NOT NEW.album_id OR OLD.album_title IS NOT NEW.album_title
    OR OLD.genre IS NOT NEW.genre BEGIN
    UPDATE tracks_fts SET title = NEW.title, artist = NEW.artist_credit,
        album = COALESCE((SELECT title FROM albums WHERE id = NEW.album_id), NEW.album_title),
        genre = NEW.genre
    WHERE rowid = NEW.id;
END;

-- Playlists.
CREATE TRIGGER playlists_fts_insert AFTER INSERT ON playlists BEGIN
    INSERT INTO playlists_fts (rowid, name, provider) VALUES (NEW.id, NEW.name, NEW.provider);
END;
CREATE TRIGGER playlists_fts_delete AFTER DELETE ON playlists BEGIN
    DELETE FROM playlists_fts WHERE rowid = OLD.id;
END;
CREATE TRIGGER playlists_fts_update AFTER UPDATE OF name ON playlists
WHEN OLD.name IS NOT NEW.name BEGIN
    UPDATE playlists_fts SET name = NEW.name WHERE rowid = NEW.id;
END;

-- Index what an upgraded catalog already holds.
INSERT INTO artists_fts (rowid, name, provider) SELECT id, name, provider FROM artists;
INSERT INTO albums_fts (rowid, title, artist, provider) SELECT id, title, artist_credit, provider FROM albums;
INSERT INTO tracks_fts (rowid, title, artist, album, genre, provider)
    SELECT t.id, t.title, t.artist_credit, COALESCE(al.title, t.album_title), t.genre, t.provider
    FROM tracks t LEFT JOIN albums al ON al.id = t.album_id;
INSERT INTO playlists_fts (rowid, name, provider) SELECT id, name, provider FROM playlists;

-- Search ranks library members higher: find an item's membership by ID.
CREATE INDEX library_items_item ON library_items (kind, item_id);
