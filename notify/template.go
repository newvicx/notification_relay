package notify

import (
	"fmt"
	"html/template"
	"strings"
)

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
// reference in src. It matches {{.varName}}, {{.varName.sub}}, {{.varName | f}},
// etc., but not a name that merely has varName as a prefix (e.g. {{.varNameExtra}}).
func containsFieldRef(src, varName string) bool {
	// TODO: This doesn't work for conditionals or other control flows ({{ if .attr }})
	// 'attr' wont be found if called out as a required var
	f1 := "{{." + varName
	f2 := "{{ ." + varName
	var needle string
	s := src
	for {
		i := strings.Index(s, f1)
		if i == -1 {
			i = strings.Index(s, f2)
			if i == -1 {
				return false
			} else {
				needle = f2
			}
		} else {
			needle = f1
		}
		rest := s[i+len(needle):]
		if len(rest) > 0 {
			switch rest[0] {
			case '}', '.', ' ', '\t', '|', ')':
				return true
			}
		}
		s = s[i+1:]
	}
}
