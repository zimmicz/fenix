# CLAUDE.md

Public repo. Never commit personal data (logs, location, tariff, credentials). Data and the hourly workflow live in each user's private repo, which runs `GOPROXY=direct go run github.com/zimmicz/fenix@main ...`. Plan: `~/dev/heating-automation/PLAN.md` (local, gitignored).

## Commands

```sh
go test ./...                                   # httptest fake of the cloud API, no creds needed
FENIX_EMAIL=... FENIX_PASSWORD=... go run . status
go run . set ZONE comfort|eco|frost|boost|manual CELSIUS   # polls 60s for readback
go run . mode ZONE comfort|off|frost|eco|boost|program
go run . log                                    # appends rows to data/log.csv (LOG_FILE overrides)
```

## Architecture

- Fenix V24 uses the same cloud backend as the `clevertouch` Python lib (Purmo/Touch E3). Reimplemented in Go (`fenix.go`) with stdlib only; do not parse devices into typed structs, the lib crashed on `gv_mode` 16 for that reason. `Device` is a raw `map[string]any`.
- Auth: OpenID password grant at `https://auth.v24.fenixgroup.eu/realms/fenix/...`, client `app-front`. Realm is `fenix` (`purmo` fails). Token is not refreshed; processes are short-lived.
- API: form-encoded POSTs to `/api/v0.1/human/...`; response status `code.code` 1 or 8 is success on reads, 8 on writes. Writes go through `human/query/push/` with `query[field]=value`.
- Both auth and API allow CORS from any origin, so a browser app can call them directly.
- Temperatures are tenths of °F: `C = (raw - 320) / 18`. Setpoint limits come from each device's `min_set_point`/`max_set_point` (410..986 raw, 5..37 °C).
- Writes take effect with delay (cloud to unit); a readback right after a write can show the old value.
- Never rewrite the `programme` schedule strings; drive setpoints and modes only.
- Breaking a command's CLI breaks every user's hourly workflow (they track `@main`).
