package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
)

const syncGroupsUsage = `Usage: nrcli sync-groups <subcommand> [arguments]

Manage which LDAP groups the syncer mirrors into the membership table.
Requires admin role.

Subcommands:
  list              List all configured sync groups
  add GROUP_NAME    Add an LDAP group to the sync list
  remove GROUP_NAME Remove a group from the sync list
`

func runSyncGroups(cfg *Config, args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, syncGroupsUsage)
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		runSyncGroupsList(cfg, args[1:])
	case "add":
		runSyncGroupsAdd(cfg, args[1:])
	case "remove":
		runSyncGroupsRemove(cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: unknown sync-groups subcommand %q\n\n", args[0])
		fmt.Fprint(os.Stderr, syncGroupsUsage)
		os.Exit(1)
	}
}

func runSyncGroupsList(cfg *Config, args []string) {
	fs := newFlagSet("sync-groups list")
	fs.Usage = func() { fmt.Fprint(os.Stderr, syncGroupsUsage) }
	parseFlags(fs, args)

	body, err := NewClient(cfg).Get("/api/v1/sync-groups", nil)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var groups []SyncGroup
	if err := json.Unmarshal(body, &groups); err != nil {
		die(err)
	}
	printSyncGroupList(groups)
}

func runSyncGroupsAdd(cfg *Config, args []string) {
	fs := newFlagSet("sync-groups add")
	fs.Usage = func() { fmt.Fprint(os.Stderr, syncGroupsUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("GROUP_NAME argument is required")
	}
	groupName := fs.Arg(0)

	_, body, err := NewClient(cfg).Post("/api/v1/sync-groups", map[string]string{
		"group_name": groupName,
	})
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var g SyncGroup
	if err := json.Unmarshal(body, &g); err != nil {
		die(err)
	}
	printSyncGroupDetail(g)
}

func runSyncGroupsRemove(cfg *Config, args []string) {
	fs := newFlagSet("sync-groups remove")
	fs.Usage = func() { fmt.Fprint(os.Stderr, syncGroupsUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("GROUP_NAME argument is required")
	}
	groupName := fs.Arg(0)

	if err := NewClient(cfg).Delete("/api/v1/sync-groups/" + url.PathEscape(groupName)); err != nil {
		die(err)
	}
	fmt.Printf("Sync group %q removed.\n", groupName)
}
