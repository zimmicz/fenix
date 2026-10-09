# fenix

Replacement for the Fenix V24 WiFi floor heating app, plus weather-forecast notifications.

- **Web app**: https://www.zimmi.cz/fenix/ (open in Safari, Share → Add to Home Screen). Talks to the Fenix cloud directly from your phone; your login stays on the device.
- **Hourly job** in your own private GitHub repo: logs zone temperatures to `data/log.csv`, checks the forecast, sends push notifications via [ntfy](https://ntfy.sh), and optionally switches sunny rooms to eco for the hours the sun heats them.

## Setup

The app has a step-by-step **Setup guide** (Czech and English) on its welcome screen, readable before signing in. In short:

1. Open https://www.zimmi.cz/fenix/ in Safari → Share → Add to Home Screen, sign in with your Fenix login. That is enough for remote control.
2. For the weather robot: [create a private repository from the template](https://github.com/new?template_name=fenix-template&template_owner=zimmicz) (`zimmicz/fenix-template`). Keep it **private**: the log reveals when you are home.
3. Repository → Settings → Secrets and variables → Actions: add `FENIX_EMAIL` and `FENIX_PASSWORD`. Actions → heating → Run workflow.
4. Create a [fine-grained token](https://github.com/settings/personal-access-tokens/new): only your repository, *Actions: Read and write* and *Contents: Read and write*. In the app: Settings → GitHub.
5. GitHub's own cron skips most hours, so trigger the workflow from a free [cron-job.org](https://cron-job.org) job: every hour at :07, `POST https://api.github.com/repos/OWNER/REPO/actions/workflows/heating.yml/dispatches`, headers `Authorization: Bearer <token>` and `Accept: application/vnd.github+json`, body `{"ref":"main"}`.
6. Install ntfy, subscribe to the topic from Settings, send a test. Add your rooms' windows.
7. Optional: a [healthchecks.io](https://healthchecks.io) check (period 1 h, grace 1 h, ntfy integration) with its ping URL in secret `HC_PING_URL` alerts you when the robot stops.

The job runs hourly (about 720 of the 2000 free Actions minutes per month for private repos).

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
