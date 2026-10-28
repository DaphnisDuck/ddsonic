-- ddmus catalog schema v4: enrichment failures. enrich_failures counts
-- the enrichment runs in which a track could not be read for a reason not
-- known to be permanent; after a few, the track is marked read (enriched_at
-- set) so one bad track no longer stops every run.
ALTER TABLE tracks ADD COLUMN enrich_failures INTEGER NOT NULL DEFAULT 0;
