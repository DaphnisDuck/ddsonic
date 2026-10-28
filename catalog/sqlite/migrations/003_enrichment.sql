-- ddmus catalog schema v3: track enrichment. enriched_at is when a
-- background read filled in a track's real title, artists, album and year
-- (YouTube, whose sync knows only video titles and channels); NULL until
-- then. A sync never overwrites an enriched track's title or credits.
ALTER TABLE tracks ADD COLUMN enriched_at INTEGER;
CREATE INDEX tracks_unenriched ON tracks (provider, enriched_at);
