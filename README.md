# fenix

Replacement for the Fenix V24 WiFi floor heating app, plus weather-forecast notifications.

- **Web app**: https://www.zimmi.cz/fenix/ (open in Safari, Share → Add to Home Screen). Talks to the Fenix cloud directly from your phone; your login stays on the device.
- **Hourly job** in your own private GitHub repo: logs zone temperatures to `data/log.csv`, checks the forecast, sends push notifications via [ntfy](https://ntfy.sh), and optionally switches sunny rooms to eco for the hours the sun heats them.

## Setup

1. Create a **private** GitHub repo (it will hold your heating log, which reveals when you are home).
2. Copy `template/.github/workflows/heating.yml` from this repo into it.
3. Repo → Settings → Secrets and variables → Actions: add `FENIX_EMAIL` and `FENIX_PASSWORD`.
4. Create a [fine-grained token](https://github.com/settings/personal-access-tokens/new): only your new repo, permission *Contents: Read and write*.
5. Open the web app → Settings: enter your Fenix login, the repo (`you/repo`) and the token, tap *Load config*.
6. Add each room's windows, *Save*.
7. Install the ntfy app, subscribe to the topic shown in Settings, tap *Send test*.

The job runs hourly (about 720 of the 2000 free Actions minutes per month for private repos). Run it once by hand: Actions → heating → Run workflow.

## Sun control

Add each room's windows in Settings (direction, width × height in metres, how many). Every hour the job takes the forecast sun on each window direction (Open-Meteo tilted irradiance) and computes the heat coming in per room:

`gain = Σ width × height × count × irradiance × 0.6` (0.6 ≈ share of sunlight passing double glazing as heat)

An hour is **sunny** for a room when the gain is at least *share* (default 50 %) of the room's heater power. On a sunny day the room goes to eco *lead* hours (default 3) before the first sunny hour, because the floor needs time to cool, and goes back to its previous mode after the last sunny hour, or earlier if clouds wipe out the remaining sun. A room you change by hand is left alone for the rest of the day. Bathrooms usually want sun control off.

With *Switch rooms to eco automatically* off you only get the evening heads-up (default 18:00): "Tomorrow: Obyvak sun 10–15 h, eco from 07 h".

## Weather rules

Notify-only, once per day: day (today/tomorrow), `tmax`/`tmin` (°C) or `sunshine` (hours), above/below a value.

## CLI

```sh
FENIX_EMAIL=... FENIX_PASSWORD=... go run github.com/zimmicz/fenix@main status
go run github.com/zimmicz/fenix@main set ZONE comfort|eco|frost|boost|manual CELSIUS
go run github.com/zimmicz/fenix@main mode ZONE comfort|off|frost|eco|boost|program
go run github.com/zimmicz/fenix@main log        # appends to data/log.csv (LOG_FILE)
go run github.com/zimmicz/fenix@main forecast   # config.json + data/state.json (CONFIG_FILE, STATE_FILE)
```
