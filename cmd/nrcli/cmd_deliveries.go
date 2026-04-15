package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
)

const deliveriesUsage = `Usage: nrcli deliveries <subcommand> [arguments]

Subcommands:
  get DELIVERY_ID   Get a delivery attempt by ID
`

func runDeliveries(cfg *Config, args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, deliveriesUsage)
		os.Exit(1)
	}
	switch args[0] {
	case "get":
		runDeliveriesGet(cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: unknown deliveries subcommand %q\n\n", args[0])
		fmt.Fprint(os.Stderr, deliveriesUsage)
		os.Exit(1)
	}
}

func runDeliveriesGet(cfg *Config, args []string) {
	fs := newFlagSet("deliveries get")
	fs.Usage = func() { fmt.Fprint(os.Stderr, deliveriesUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("DELIVERY_ID argument is required")
	}
	deliveryID := fs.Arg(0)

	body, err := NewClient(cfg).Get("/api/v1/deliveries/"+url.PathEscape(deliveryID), nil)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var d Delivery
	if err := json.Unmarshal(body, &d); err != nil {
		die(err)
	}
	printDeliveryDetail(d)
}
