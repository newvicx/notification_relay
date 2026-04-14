package notify

import (
	"fmt"
	"html/template"
	"strings"
)

// ValidateTemplate parses subject and body as html/template sources and
// verifies that every name in requiredVars is referenced in at least one of
// them. Returns a descriptive error if parsing fails or a required variable
// is absent from both subject and body.
func ValidateTemplate(subject, body string, requiredVars []string) error {
	if _, err := template.New("body").Parse(body); err != nil {
		return fmt.Errorf("invalid body template: %w", err)
	}
	if _, err := template.New("subject").Parse(subject); err != nil {
		return fmt.Errorf("invalid subject template: %w", err)
	}
	for _, v := range requiredVars {
		ref := "{{." + v + "}}"
		if !strings.Contains(body, ref) && !strings.Contains(subject, ref) {
			return fmt.Errorf("required variable %q not referenced in subject or body", v)
		}
	}
	return nil
}

// RenderTemplate executes subject and body as html/template sources with vars
// as the data object. Template variables are accessed as {{.key}}.
// Returns the rendered subject and body strings.
func RenderTemplate(subject, body string, vars map[string]string) (string, string, error) {
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

func renderOne(name, src string, vars map[string]string) (string, error) {
	tmpl, err := template.New(name).Parse(src)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", err
	}
	return buf.String(), nil
}
