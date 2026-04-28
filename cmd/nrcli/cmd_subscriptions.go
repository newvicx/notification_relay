package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
)

const subscriptionsUsage = `Usage: nrcli subscriptions <subcommand> [arguments]

Subcommands:
  list                    List all SMS subscriptions (admin only)
  subscribe USERNAME      Subscribe a user by username (admin only)
  subscribe-me            Subscribe yourself
  unsubscribe USERNAME    Remove a user's subscription (admin only)
  unsubscribe-me          Remove your own subscription

Examples:
  nrcli subscriptions list
  nrcli subscriptions subscribe jsmith
  nrcli subscriptions subscribe-me
  nrcli subscriptions unsubscribe jsmith
  nrcli subscriptions unsubscribe-me
`

func runSubscriptions(cfg *Config, args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, subscriptionsUsage)
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		runSubscriptionsList(cfg, args[1:])
	case "subscribe":
		runSubscriptionsSubscribe(cfg, args[1:])
	case "subscribe-me":
		runSubscriptionsSubscribeMe(cfg, args[1:])
	case "unsubscribe":
		runSubscriptionsUnsubscribe(cfg, args[1:])
	case "unsubscribe-me":
		runSubscriptionsUnsubscribeMe(cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: unknown subscriptions subcommand %q\n\n", args[0])
		fmt.Fprint(os.Stderr, subscriptionsUsage)
		os.Exit(1)
	}
}

func runSubscriptionsList(cfg *Config, args []string) {
	fs := newFlagSet("subscriptions list")
	fs.Usage = func() { fmt.Fprint(os.Stderr, subscriptionsUsage) }
	parseFlags(fs, args)

	body, err := NewClient(cfg).Get("/api/v1/sms-subscriptions", nil)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var subs []SMSSubscription
	if err := json.Unmarshal(body, &subs); err != nil {
		die(err)
	}
	printSMSSubscriptionList(subs)
}

func runSubscriptionsSubscribe(cfg *Config, args []string) {
	fs := newFlagSet("subscriptions subscribe")
	fs.Usage = func() { fmt.Fprint(os.Stderr, subscriptionsUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("USERNAME argument is required")
	}
	username := fs.Arg(0)

	_, respBody, err := NewClient(cfg).Post("/api/v1/sms-subscriptions", map[string]string{
		"username": username,
	})
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(respBody)
		return
	}
	var sub SMSSubscription
	if err := json.Unmarshal(respBody, &sub); err != nil {
		die(err)
	}
	printSMSSubscriptionDetail(sub)
}

func runSubscriptionsSubscribeMe(cfg *Config, args []string) {
	fs := newFlagSet("subscriptions subscribe-me")
	fs.Usage = func() { fmt.Fprint(os.Stderr, subscriptionsUsage) }
	parseFlags(fs, args)

	_, respBody, err := NewClient(cfg).Post("/api/v1/sms-subscriptions/me", nil)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(respBody)
		return
	}
	var sub SMSSubscription
	if err := json.Unmarshal(respBody, &sub); err != nil {
		die(err)
	}
	printSMSSubscriptionDetail(sub)
}

func runSubscriptionsUnsubscribe(cfg *Config, args []string) {
	fs := newFlagSet("subscriptions unsubscribe")
	fs.Usage = func() { fmt.Fprint(os.Stderr, subscriptionsUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("USERNAME argument is required")
	}
	username := fs.Arg(0)

	if err := NewClient(cfg).Delete("/api/v1/sms-subscriptions/" + url.PathEscape(username)); err != nil {
		die(err)
	}
	fmt.Printf("Subscription for %q removed.\n", username)
}

func runSubscriptionsUnsubscribeMe(cfg *Config, args []string) {
	fs := newFlagSet("subscriptions unsubscribe-me")
	fs.Usage = func() { fmt.Fprint(os.Stderr, subscriptionsUsage) }
	parseFlags(fs, args)

	if err := NewClient(cfg).Delete("/api/v1/sms-subscriptions/me"); err != nil {
		die(err)
	}
	fmt.Println("You have been unsubscribed from SMS alerts.")
}
