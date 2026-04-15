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
