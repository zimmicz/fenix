# fenix

CLI for Fenix V24 WiFi floor heating (cloud API shared with Purmo/Touch E3).

```sh
FENIX_EMAIL=... FENIX_PASSWORD=... go run github.com/zimmicz/fenix@main status
go run github.com/zimmicz/fenix@main set ZONE comfort|eco|frost|boost|manual CELSIUS
go run github.com/zimmicz/fenix@main mode ZONE comfort|off|frost|eco|boost|program
go run github.com/zimmicz/fenix@main log   # appends to data/log.csv (LOG_FILE overrides)
```
