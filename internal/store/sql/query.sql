-- name: UpsertEvent :exec
INSERT INTO events (
  event_id, schema_version, provider, surface, repo, project, cost_tag, model, ts_start_unixnano, event_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(event_id) DO UPDATE SET
  schema_version    = excluded.schema_version,
  provider          = excluded.provider,
  surface           = excluded.surface,
  repo              = excluded.repo,
  project           = excluded.project,
  cost_tag          = excluded.cost_tag,
  model             = excluded.model,
  ts_start_unixnano = excluded.ts_start_unixnano,
  event_json        = excluded.event_json;

-- name: GetEvent :one
SELECT event_json FROM events WHERE event_id = ?;

-- name: ListEvents :many
SELECT event_json FROM events
WHERE (sqlc.narg('provider') IS NULL OR provider = sqlc.narg('provider'))
  AND (sqlc.narg('repo')     IS NULL OR repo = sqlc.narg('repo'))
  AND (sqlc.narg('since')    IS NULL OR ts_start_unixnano >= sqlc.narg('since'))
  AND (sqlc.narg('until')    IS NULL OR ts_start_unixnano <= sqlc.narg('until'))
ORDER BY ts_start_unixnano ASC;

-- name: GetLastScan :one
SELECT last_scan FROM scan_meta WHERE provider = ?;

-- name: SetLastScan :exec
INSERT INTO scan_meta (provider, last_scan) VALUES (?, ?)
ON CONFLICT(provider) DO UPDATE SET last_scan = excluded.last_scan;
