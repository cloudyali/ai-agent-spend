-- Schema for the SQLite backend (-tags sqlite). The full AgentEvent rides in
-- event_json (lossless); scalar columns are a denormalized index for filtering.
-- See design-documents/02-data-model.md §6.
CREATE TABLE IF NOT EXISTS events (
  event_id          TEXT PRIMARY KEY,
  schema_version    INTEGER NOT NULL,
  provider          TEXT NOT NULL,
  surface           TEXT NOT NULL,
  repo              TEXT NOT NULL,
  project           TEXT NOT NULL,
  cost_tag          TEXT NOT NULL,
  model             TEXT NOT NULL,
  ts_start_unixnano INTEGER NOT NULL,
  event_json        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_ts   ON events (ts_start_unixnano);
CREATE INDEX IF NOT EXISTS idx_events_repo ON events (repo);

CREATE TABLE IF NOT EXISTS scan_meta (
  provider  TEXT PRIMARY KEY,
  last_scan INTEGER NOT NULL
);
