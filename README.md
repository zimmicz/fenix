# fenix

Replacement for the Fenix V24 WiFi floor heating app, plus weather-forecast notifications.

- **Web app**: https://zimmicz.github.io/fenix/ (open in Safari, Share → Add to Home Screen). Talks to the Fenix cloud directly from your phone; your login stays on the device.
- **Hourly job** in your own private GitHub repo: logs zone temperatures to `data/log.csv`, checks the forecast, sends push notifications via [ntfy](https://ntfy.sh), and optionally switches zones to eco/frost on sunny or warm days (reverted the next day).

## Setup

1. Create a **private** GitHub repo (it will hold your heating log, which reveals when you are home).
2. Copy `template/.github/workflows/heating.yml` from this repo into it.
3. Repo → Settings → Secrets and variables → Actions: add `FENIX_EMAIL` and `FENIX_PASSWORD`.
4. Create a [fine-grained token](https://github.com/settings/personal-access-tokens/new): only your new repo, permission *Contents: Read and write*.
5. Open the web app → Settings: enter your Fenix login, the repo (`you/repo`) and the token, tap *Load config*.
6. Pick which way your main windows face, add rules, *Save*.
7. Install the ntfy app, subscribe to the topic shown in Settings, tap *Send test*.

The job runs hourly (about 720 of the 2000 free Actions minutes per month for private repos). Run it once by hand: Actions → heating → Run workflow.

## Rules

Each rule: day (today/tomorrow), metric, above/below a value, message, optional mode for chosen zones.

| metric | meaning |
|---|---|
| `tmax`, `tmin` | outdoor max/min °C |
| `sunshine` | sunshine hours |
| `solar` | sun energy hitting a vertical window facing your direction, kWh/m² per day (Open-Meteo tilted irradiance). A clear October day on a south window ≈ 3–4. |

A rule notifies at most once per day. With *Auto-adjust* on, its mode change is applied on the target day and reverted the following night.

## CLI

```sh
FENIX_EMAIL=... FENIX_PASSWORD=... go run github.com/zimmicz/fenix@main status
go run github.com/zimmicz/fenix@main set ZONE comfort|eco|frost|boost|manual CELSIUS
go run github.com/zimmicz/fenix@main mode ZONE comfort|off|frost|eco|boost|program
go run github.com/zimmicz/fenix@main log        # appends to data/log.csv (LOG_FILE)
go run github.com/zimmicz/fenix@main forecast   # config.json + data/state.json (CONFIG_FILE, STATE_FILE)
```
