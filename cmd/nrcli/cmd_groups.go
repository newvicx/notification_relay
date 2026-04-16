package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
)

const groupsUsage = `Usage: nrcli groups <subcommand> [arguments]

Subcommands:
  list                  List all synced LDAP groups
  members GROUP_NAME    List members of a group
  sync                  Manage LDAP sync group configuration (admin only)
`

const groupsSyncUsage = `Usage: nrcli groups sync <subcommand> [arguments]

Manage which LDAP groups the syncer mirrors into the membership table.
Requires admin role.

Subcommands:
  list              List all configured sync groups
  add GROUP_NAME    Add an LDAP group to the sync list
  remove GROUP_NAME Remove a group from the sync list
`

func runGroups(cfg *Config, args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, groupsUsage)
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		runGroupsList(cfg, args[1:])
	case "members":
		runGroupsMembers(cfg, args[1:])
	case "sync":
		runGroupsSync(cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: unknown groups subcommand %q\n\n", args[0])
		fmt.Fprint(os.Stderr, groupsUsage)
		os.Exit(1)
	}
}

func runGroupsList(cfg *Config, args []string) {
	fs := newFlagSet("groups list")
	fs.Usage = func() { fmt.Fprint(os.Stderr, groupsUsage) }
	parseFlags(fs, args)

	body, err := NewClient(cfg).Get("/api/v1/groups", nil)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var groups []string
	if err := json.Unmarshal(body, &groups); err != nil {
		die(err)
	}
	printGroupList(groups)
}

func runGroupsMembers(cfg *Config, args []string) {
	fs := newFlagSet("groups members")
	fs.Usage = func() { fmt.Fprint(os.Stderr, groupsUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("GROUP_NAME argument is required")
	}
	groupName := fs.Arg(0)

	body, err := NewClient(cfg).Get("/api/v1/groups/"+url.PathEscape(groupName)+"/members", nil)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var members []GroupMember
	if err := json.Unmarshal(body, &members); err != nil {
		die(err)
	}
	printGroupMemberList(members)
}

func runGroupsSync(cfg *Config, args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, groupsSyncUsage)
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		runGroupsSyncList(cfg, args[1:])
	case "add":
		runGroupsSyncAdd(cfg, args[1:])
	case "remove":
		runGroupsSyncRemove(cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: unknown groups sync subcommand %q\n\n", args[0])
		fmt.Fprint(os.Stderr, groupsSyncUsage)
		os.Exit(1)
	}
}

func runGroupsSyncList(cfg *Config, args []string) {
	fs := newFlagSet("groups sync list")
	fs.Usage = func() { fmt.Fprint(os.Stderr, groupsSyncUsage) }
	parseFlags(fs, args)

	body, err := NewClient(cfg).Get("/api/v1/groups/sync", nil)
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

func runGroupsSyncAdd(cfg *Config, args []string) {
	fs := newFlagSet("groups sync add")
	fs.Usage = func() { fmt.Fprint(os.Stderr, groupsSyncUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("GROUP_NAME argument is required")
	}
	groupName := fs.Arg(0)

	_, body, err := NewClient(cfg).Post("/api/v1/groups/sync", map[string]string{
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

func runGroupsSyncRemove(cfg *Config, args []string) {
	fs := newFlagSet("groups sync remove")
	fs.Usage = func() { fmt.Fprint(os.Stderr, groupsSyncUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("GROUP_NAME argument is required")
	}
	groupName := fs.Arg(0)

	if err := NewClient(cfg).Delete("/api/v1/groups/sync/" + url.PathEscape(groupName)); err != nil {
		die(err)
	}
	fmt.Printf("Sync group %q removed.\n", groupName)
}
