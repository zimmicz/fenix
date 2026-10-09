package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

var setpoints = map[string]string{
	"comfort": "consigne_confort",
	"eco":     "consigne_eco",
	"frost":   "consigne_hg",
	"boost":   "consigne_boost",
	"manual":  "consigne_manuel",
}

var modes = map[string]string{
	"comfort": "0",
	"off":     "1",
	"frost":   "2",
	"eco":     "3",
	"boost":   "4",
	"program": "11",
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func connect() (*Client, error) {
	email, password := os.Getenv("FENIX_EMAIL"), os.Getenv("FENIX_PASSWORD")
	if email == "" || password == "" {
		return nil, fmt.Errorf("FENIX_EMAIL and FENIX_PASSWORD required")
	}
	c := NewClient(env("FENIX_HOST", "v24.fenixgroup.eu"), env("FENIX_REALM", "fenix"))
	return c, c.Login(email, password)
}

func findZone(c *Client, label string) (Zone, error) {
	zones, err := c.Zones()
	if err != nil {
		return Zone{}, err
	}
	var names []string
	for _, z := range zones {
		if z.Label == label {
			return z, nil
		}
		names = append(names, z.Label)
	}
	return Zone{}, fmt.Errorf("unknown zone %q, have %v", label, names)
}

func status(c *Client) error {
	zones, err := c.Zones()
	if err != nil {
		return err
	}
	for _, z := range zones {
		d := z.Device
		fmt.Printf("%-10s air=%.1f comfort=%.1f eco=%.1f mode=%s heating=%s\n",
			z.Label, d.celsius("temperature_air"), d.celsius("consigne_confort"),
			d.celsius("consigne_eco"), d.str("gv_mode"), d.str("heating_up"))
	}
	return nil
}

func setSetpoint(c *Client, zone, kind string, celsius float64) error {
	field, ok := setpoints[kind]
	if !ok {
		return fmt.Errorf("unknown setpoint %q", kind)
	}
	z, err := findZone(c, zone)
	if err != nil {
		return err
	}
	lo, hi := z.Device.celsius("min_set_point"), z.Device.celsius("max_set_point")
	if celsius < lo || celsius > hi {
		return fmt.Errorf("%.1f outside %.1f..%.1f", celsius, lo, hi)
	}
	want := strconv.Itoa(toRaw(celsius))
	if err := c.Write(z.Device.str("id_device"), map[string]string{field: want}); err != nil {
		return err
	}
	fmt.Println("sent", field, want)
	for i := 1; i <= 12; i++ {
		time.Sleep(5 * time.Second)
		cur, err := findZone(c, zone)
		if err != nil {
			return err
		}
		got := cur.Device.str(field)
		fmt.Printf("+%ds %s=%s\n", i*5, field, got)
		if got == want {
			return nil
		}
	}
	return fmt.Errorf("value never changed")
}

func setMode(c *Client, zone, mode string) error {
	code, ok := modes[mode]
	if !ok {
		return fmt.Errorf("unknown mode %q", mode)
	}
	z, err := findZone(c, zone)
	if err != nil {
		return err
	}
	return c.Write(z.Device.str("id_device"), map[string]string{"gv_mode": code, "nv_mode": code})
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: status | log | set ZONE KIND CELSIUS | mode ZONE MODE")
	}
	c, err := connect()
	if err != nil {
		return err
	}
	switch {
	case args[0] == "status":
		return status(c)
	case args[0] == "log":
		return logZones(c, env("LOG_FILE", "data/log.csv"))
	case args[0] == "set" && len(args) == 4:
		celsius, err := strconv.ParseFloat(args[3], 64)
		if err != nil {
			return err
		}
		return setSetpoint(c, args[1], args[2], celsius)
	case args[0] == "mode" && len(args) == 3:
		return setMode(c, args[1], args[2])
	}
	return fmt.Errorf("bad command %v", args)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
