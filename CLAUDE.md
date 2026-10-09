# CLAUDE.md

Public repo. Never commit personal data (logs, location, tariff, credentials). Data and the hourly workflow live in each user's private repo, which runs `GOPROXY=direct go install github.com/zimmicz/fenix@main`, then `fenix log` and `fenix forecast`, started hourly by cron-job.org via the `workflow_dispatch` API (GitHub's `schedule` trigger is throttled to a few runs a day). Plan: `~/dev/heating-automation/PLAN.md` (local, gitignored).

## Commands

```sh
go test ./...                                   # httptest fake of the cloud API, no creds needed
FENIX_EMAIL=... FENIX_PASSWORD=... go run . status
go run . set ZONE comfort|eco|frost|boost|manual CELSIUS   # polls 60s for readback
go run . mode ZONE comfort|off|frost|eco|boost|program|manual
go run . log                                    # appends rows to data/log.csv (LOG_FILE overrides)
go run . forecast                               # rules from config.json, state in data/state.json, ntfy push
```

Web app: `docs/index.html` (GitHub Pages from `/docs`), single file, no build, no deps. Talks to Fenix, Open-Meteo, ntfy and the GitHub contents API (user's private repo `config.json`) straight from the browser. User workflow lives in the template repo `zimmicz/fenix-template` (local `~/dev/fenix-template`); change it there.

## Architecture

- Fenix V24 uses the same cloud backend as the `clevertouch` Python lib (Purmo/Touch E3). Reimplemented in Go (`fenix.go`) with stdlib only; do not parse devices into typed structs, the lib crashed on `gv_mode` 16 for that reason. `Device` is a raw `map[string]any`.
- Auth: OpenID password grant at `https://auth.v24.fenixgroup.eu/realms/fenix/...`, client `app-front`. Realm is `fenix` (`purmo` fails). Token is not refreshed; processes are short-lived.
- API: form-encoded POSTs to `/api/v0.1/human/...`; response status `code.code` 1 or 8 is success on reads, 8 on writes. Writes go through `human/query/push/` with `query[field]=value`.
- Both auth and API allow CORS from any origin, so a browser app can call them directly. Error responses (e.g. 401 on an expired token) lack the CORS header, so the browser throws instead of seeing the status; the app renews the token before `expires_in`.
- Endpoint list comes from the official Android app (`com.irisinteractive.fenixprod`, Cordova, `assets/www/scripts/scripts.js`). Usage tab reads `human/stats/read/` (`smarthome_id`): kWh per device (`yesterday_conso`, `weekly_conso`, `monthly_conso{jan..dec}`, `last_year_conso`, …) and `stats_by_day.statsByDay{Current,Last}Month[day][num_zone].conso_day`. Price per kWh is per device (localStorage), not in config.json.
- Temperatures are tenths of °F: `C = (raw - 320) / 18`. Setpoint limits come from each device's `min_set_point`/`max_set_point` (410..986 raw, 5..37 °C).
- Writes take effect with delay (cloud to unit); a readback right after a write can show the old value.
- `programme` = weekly schedule, 336 hex chars: Mon..Sun, 96 quarter-hours per day, 2 bits per slot, high bits first. Slot 0 = eco (`gv_mode` 11), 1 = comfort (8), 3 = Booster (`gv_mode` 16, setpoint `consigne_boost`); the official app offers no frost slot. Only the web app's Weekly program sheet writes it (whole string via `query[programme]`); it edits whole hours and keeps untouched quarters. The robot never touches it.
- Zones usually run raw `gv_mode` values outside the named map (8, 16 seen: unit schedules). Sun control restores the recorded raw `gv_mode`/`nv_mode`, never a mode name; the web app's Schedule button restores the last seen raw code (localStorage).
- Away mode: the app writes `config.away` {until (UTC ISO), prev raw codes} and does the eco switch itself; the robot only restores (rooms still on eco '3') at `until - away_warmup_hours` and marks `state.away_restored = until`, never edits config.json; the app drops `config.away` once restored. Sun control is skipped while away. `holiday_mode`/`general_mode` on the smarthome are untested and unused.
- Sun control math lives twice: `sunWindow` in forecast.go and `sunWindow` in docs/index.html (preview). Keep them identical.
- Forecast dates come from Open-Meteo `timezone=auto`, so "today" is the home's local date; state compares dates as `YYYY-MM-DD` strings.
- UI is Czech + English: app strings in `TEXT`/`HELP` (docs/index.html), robot notifications in `messages` (forecast.go), language from `config.json` `lang`. Every new string needs both languages.
- Breaking a command's CLI breaks every user's hourly workflow (they track `@main`).
