-- ddmus catalog schema v5: per-collection source bookkeeping.
-- last_applied_at is when a collection was last read in full and written:
-- a source that can tell cheaply that nothing changed still reads
-- everything now and then, so metadata changes land. source_version is the
-- version of the source's own rules the stored collection was built with
-- (the local indexer's grouping), so a new version rebuilds it once.
ALTER TABLE sync_state ADD COLUMN last_applied_at INTEGER;
ALTER TABLE sync_state ADD COLUMN source_version INTEGER NOT NULL DEFAULT 0;
