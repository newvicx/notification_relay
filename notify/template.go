package notify

import (
	"fmt"
	"html/template"
	"regexp"
	"strings"
)

// actionRe matches the contents of a single {{...}} template action.
var actionRe = regexp.MustCompile(`(?s)\{\{(.*?)\}\}`)

// ValidateTemplate parses subject and body as html/template sources and
// verifies that every name in requiredVars is referenced in at least one of
// them. A required var "foo" matches {{.foo}}, {{.foo.bar}}, {{.foo | pipe}},
// etc. Returns a descriptive error if parsing fails or a required variable is
// absent from both subject and body.
func ValidateTemplate(subject, body string, requiredVars []string) error {
	if _, err := template.New("body").Parse(body); err != nil {
		return fmt.Errorf("invalid body template: %w", err)
	}
	if _, err := template.New("subject").Parse(subject); err != nil {
		return fmt.Errorf("invalid subject template: %w", err)
	}
	for _, v := range requiredVars {
		if !containsFieldRef(body, v) && !containsFieldRef(subject, v) {
			return fmt.Errorf("required variable %q not referenced in subject or body", v)
		}
	}
	return nil
}

// RenderTemplate executes subject and body as html/template sources with vars
// as the data object. vars may be any JSON-compatible value: a flat
// map[string]any, a nested map, a struct, etc. Template variables are accessed
// as {{.key}} for top-level fields and {{.key.subkey}} for nested fields.
// Missing keys cause a render error (missingkey=error).
// Returns the rendered subject and body strings.
func RenderTemplate(subject, body string, vars map[string]any) (string, string, error) {
	renderedSubject, err := renderOne("subject", subject, vars)
	if err != nil {
		return "", "", fmt.Errorf("render subject: %w", err)
	}
	renderedBody, err := renderOne("body", body, vars)
	if err != nil {
		return "", "", fmt.Errorf("render body: %w", err)
	}
	return renderedSubject, renderedBody, nil
}

func renderOne(name, src string, vars map[string]any) (string, error) {
	tmpl, err := template.New(name).Option("missingkey=zero").Parse(src)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// containsFieldRef reports whether varName appears as a template field
// reference in src. It matches {{.varName}}, {{.varName.sub}},
// {{.varName | f}}, etc., anywhere inside a template action — including
// inside conditionals, ranges, and other control-flow keywords (e.g.
// {{if eq .varName "x"}}), not just a bare {{.varName}} action — but not a
// name that merely has varName as a prefix (e.g. {{.varNameExtra}}) or that
// appears as a sub-field of some other value (e.g. {{.other.varName}}).
func containsFieldRef(src, varName string) bool {
	fieldRe := regexp.MustCompile(`(?:^|[^.\w])\.` + regexp.QuoteMeta(varName) + `\b`)
	for _, m := range actionRe.FindAllStringSubmatch(src, -1) {
		if fieldRe.MatchString(m[1]) {
			return true
		}
	}
	return false
}
