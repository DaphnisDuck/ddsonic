-- ddmus catalog schema v6: a provider's rate-limit block, kept across
-- restarts so a new run does not ask again before the provider allows.
CREATE TABLE rate_limits (
    provider TEXT    PRIMARY KEY,
    until    INTEGER NOT NULL -- unix milliseconds
);
