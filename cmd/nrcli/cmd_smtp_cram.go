package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
)

const smtpCramUsage = `Usage: nrcli smtp-cram <subcommand> [arguments]

Manage credentials for the SMTP ingestion server's CRAM-MD5 authentication
mechanism. These are separate from LDAP: CRAM-MD5 requires the server to hold
the plaintext shared secret, which an LDAP bind never exposes. Requires admin
role.

Subcommands:
  list                                List all CRAM-MD5 credentials
  add USERNAME --role ROLE [...]      Create a credential (secret shown once)
  remove USERNAME                     Remove a credential
`

func runSMTPCram(cfg *Config, args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, smtpCramUsage)
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		runSMTPCramList(cfg, args[1:])
	case "add":
		runSMTPCramAdd(cfg, args[1:])
	case "remove":
		runSMTPCramRemove(cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: unknown smtp-cram subcommand %q\n\n", args[0])
		fmt.Fprint(os.Stderr, smtpCramUsage)
		os.Exit(1)
	}
}

func runSMTPCramList(cfg *Config, args []string) {
	fs := newFlagSet("smtp-cram list")
	fs.Usage = func() { fmt.Fprint(os.Stderr, smtpCramUsage) }
	parseFlags(fs, args)

	body, err := NewClient(cfg).Get("/api/v1/smtp/cram-credentials", nil)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var creds []CRAMCredential
	if err := json.Unmarshal(body, &creds); err != nil {
		die(err)
	}
	printCRAMCredentialList(creds)
}

func runSMTPCramAdd(cfg *Config, args []string) {
	fs := newFlagSet("smtp-cram add")
	fs.Usage = func() { fmt.Fprint(os.Stderr, smtpCramUsage) }
	var roles stringSlice
	fs.Var(&roles, "role", "Role granted to this credential (admin, publisher, reader); repeatable")
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("USERNAME argument is required")
	}
	if len(roles) == 0 {
		dief("at least one --role is required")
	}
	username := fs.Arg(0)

	_, body, err := NewClient(cfg).Post("/api/v1/smtp/cram-credentials", map[string]any{
		"username": username,
		"roles":    []string(roles),
	})
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var c CRAMCredentialCreated
	if err := json.Unmarshal(body, &c); err != nil {
		die(err)
	}
	printCRAMCredentialCreated(c)
}

func runSMTPCramRemove(cfg *Config, args []string) {
	fs := newFlagSet("smtp-cram remove")
	fs.Usage = func() { fmt.Fprint(os.Stderr, smtpCramUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("USERNAME argument is required")
	}
	username := fs.Arg(0)

	if err := NewClient(cfg).Delete("/api/v1/smtp/cram-credentials/" + url.PathEscape(username)); err != nil {
		die(err)
	}
	fmt.Printf("SMTP CRAM-MD5 credential %q removed.\n", username)
}
