package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
)

const templatesUsage = `Usage: nrcli templates <subcommand> [flags] [arguments]

Subcommands:
  list                      List all email templates
  create                    Create a new email template
  get   TEMPLATE_NAME       Get a template by name
  update TEMPLATE_NAME      Replace a template's content
  delete TEMPLATE_NAME      Delete a template

Flags for 'create' and 'update':
  --subject SUBJ        Email subject; supports Go template syntax (required)
  --body BODY           Email body inline; supports Go template syntax
  --body-file PATH      Read email body from a file (alternative to --body)
  --required-var VAR    Variable name that must be supplied; repeat for multiple
  --description DESC    Human-readable description

Exactly one of --body or --body-file must be provided.

Examples:
  nrcli templates create \
    --subject "Alert: {{.event_name}}" \
    --body-file /etc/nrcli/templates/alert-standard.html \
    --required-var event_name --required-var host --required-var threshold \
    alert-standard

  nrcli templates update alert-standard \
    --subject "ALERT: {{.event_name}}" \
    --body-file ./body.html
`

func runTemplates(cfg *Config, args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, templatesUsage)
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		runTemplatesList(cfg, args[1:])
	case "create":
		runTemplatesCreate(cfg, args[1:])
	case "get":
		runTemplatesGet(cfg, args[1:])
	case "update":
		runTemplatesUpdate(cfg, args[1:])
	case "delete":
		runTemplatesDelete(cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: unknown templates subcommand %q\n\n", args[0])
		fmt.Fprint(os.Stderr, templatesUsage)
		os.Exit(1)
	}
}

// resolveBody returns the template body string from either --body (inline) or
// --body-file (file path). Exactly one must be non-empty; it exits with an
// error if neither or both are provided.
func resolveBody(body, bodyFile string) string {
	if body != "" && bodyFile != "" {
		dief("--body and --body-file are mutually exclusive")
	}
	if body == "" && bodyFile == "" {
		dief("one of --body or --body-file is required")
	}
	if body != "" {
		return body
	}
	data, err := os.ReadFile(bodyFile)
	if err != nil {
		dief("read body file: %v", err)
	}
	return string(data)
}

func runTemplatesList(cfg *Config, args []string) {
	fs := newFlagSet("templates list")
	fs.Usage = func() { fmt.Fprint(os.Stderr, templatesUsage) }
	parseFlags(fs, args)

	body, err := NewClient(cfg).Get("/api/v1/templates", nil)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(body)
		return
	}
	var templates []Template
	if err := json.Unmarshal(body, &templates); err != nil {
		die(err)
	}
	printTemplateList(templates)
}

func runTemplatesCreate(cfg *Config, args []string) {
	fs := newFlagSet("templates create")
	subject := fs.String("subject", "", "email subject (Go template syntax, required)")
	body := fs.String("body", "", "email body inline (Go template syntax)")
	bodyFile := fs.String("body-file", "", "read email body from this file")
	desc := fs.String("description", "", "description")
	var requiredVars stringSlice
	fs.Var(&requiredVars, "required-var", "required template variable (repeat for multiple)")
	fs.Usage = func() { fmt.Fprint(os.Stderr, templatesUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("TEMPLATE_NAME argument is required")
	}
	name := fs.Arg(0)

	if *subject == "" {
		dief("--subject is required")
	}
	bodyContent := resolveBody(*body, *bodyFile)

	req := map[string]any{
		"template_name": name,
		"subject":       *subject,
		"body":          bodyContent,
		"required_vars": []string(requiredVars),
	}
	if *desc != "" {
		req["description"] = *desc
	}

	_, respBody, err := NewClient(cfg).Post("/api/v1/templates", req)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(respBody)
		return
	}
	var t Template
	if err := json.Unmarshal(respBody, &t); err != nil {
		die(err)
	}
	printTemplateDetail(t)
}

func runTemplatesGet(cfg *Config, args []string) {
	fs := newFlagSet("templates get")
	fs.Usage = func() { fmt.Fprint(os.Stderr, templatesUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("TEMPLATE_NAME argument is required")
	}
	name := fs.Arg(0)

	respBody, err := NewClient(cfg).Get("/api/v1/templates/"+url.PathEscape(name), nil)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(respBody)
		return
	}
	var t Template
	if err := json.Unmarshal(respBody, &t); err != nil {
		die(err)
	}
	printTemplateDetail(t)
}

func runTemplatesUpdate(cfg *Config, args []string) {
	fs := newFlagSet("templates update")
	subject := fs.String("subject", "", "email subject (Go template syntax, required)")
	body := fs.String("body", "", "email body inline (Go template syntax)")
	bodyFile := fs.String("body-file", "", "read email body from this file")
	desc := fs.String("description", "", "description")
	var requiredVars stringSlice
	fs.Var(&requiredVars, "required-var", "required template variable (repeat for multiple)")
	fs.Usage = func() { fmt.Fprint(os.Stderr, templatesUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("TEMPLATE_NAME argument is required")
	}
	name := fs.Arg(0)

	if *subject == "" {
		dief("--subject is required")
	}
	bodyContent := resolveBody(*body, *bodyFile)

	req := map[string]any{
		"template_name": name,
		"subject":       *subject,
		"body":          bodyContent,
		"required_vars": []string(requiredVars),
	}
	if *desc != "" {
		req["description"] = *desc
	}

	respBody, err := NewClient(cfg).Put("/api/v1/templates/"+url.PathEscape(name), req)
	if err != nil {
		die(err)
	}
	if cfg.JSON {
		printJSON(respBody)
		return
	}
	var t Template
	if err := json.Unmarshal(respBody, &t); err != nil {
		die(err)
	}
	printTemplateDetail(t)
}

func runTemplatesDelete(cfg *Config, args []string) {
	fs := newFlagSet("templates delete")
	fs.Usage = func() { fmt.Fprint(os.Stderr, templatesUsage) }
	parseFlags(fs, args)

	if fs.NArg() == 0 {
		dief("TEMPLATE_NAME argument is required")
	}
	name := fs.Arg(0)

	if err := NewClient(cfg).Delete("/api/v1/templates/" + url.PathEscape(name)); err != nil {
		die(err)
	}
	fmt.Printf("Template %q deleted.\n", name)
}
