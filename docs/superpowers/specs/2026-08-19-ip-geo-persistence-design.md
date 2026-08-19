# IP Geo Persistence Design

## Status

Approved design, pending user review.

## Goal

Persist public IP geolocation in SQLite and refresh it periodically from the
Panel background job. Frontend reads the persisted values instead of resolving
IP location on every API request.

## Refresh interval

`[geo] refresh_interval = "12h"`.

## Data model

Add these columns to the `nodes` table:

- `ip_location TEXT` formatted display value, e.g. `美国-洛杉矶`
- `ip_country TEXT`
- `ip_region TEXT`
- `ip_city TEXT`
- `ip_geo_updated_at INTEGER`

The IP address itself continues to be read from `nodes.last_host_json`.

## Background job

- Runs at startup and every configured interval.
- Selects all nodes with a public IP in `last_host_json`.
- Resolves location with `http://ip-api.com/json/{ip}?lang=zh-CN&fields=status,country,regionName,city`.
- Updates `ip_location`, `ip_country`, `ip_region`, `ip_city`, and
  `ip_geo_updated_at`.
- Private/loopback IPs are stored as `内网` without external lookup.
- Failed lookups retain the previous values and are logged.

## API

- Node list/detail responses include:
  - `ip_location`
  - `ip_country`
  - `ip_region`
  - `ip_city`
  - `ip_geo_updated_at`
- Remove the current per-request in-memory geo cache and live lookup.

## Migration

- Fresh installations include the new columns in `CREATE TABLE`.
- Existing databases run an idempotent `ALTER TABLE` migration.
- Missing columns are added without losing existing data.

## Frontend

- Node page continues to display `ip_location`.
- No new column for `ip_geo_updated_at` in this change.

## Testing

- Unit-test location formatting.
- Test that private IPs do not call the geo provider.
- Test migration adds missing columns.
- Run `go test ./...`, `go vet ./...`, `npx tsc --noEmit`, and
  `npm run build`.
