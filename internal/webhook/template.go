package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
)

var templateFuncs = template.FuncMap{
	"toJSON": func(v any) (string, error) {
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b), nil
	},
	"toUpper": strings.ToUpper,
	"toLower": strings.ToLower,
	"replace": strings.ReplaceAll,
}

// ValidateTemplate parses the template and executes it against a sample payload
// to catch errors at creation time. Returns nil for empty string.
func ValidateTemplate(tmplStr string) error {
	if tmplStr == "" {
		return nil
	}

	tmpl, err := template.New("webhook").Funcs(templateFuncs).Parse(tmplStr)
	if err != nil {
		return fmt.Errorf("invalid template syntax: %w", err)
	}

	sample := WebhookPayload{
		Event:     "push",
		Timestamp: "2024-01-01T00:00:00Z",
		Repository: RepositoryPayload{
			Namespace: "example",
			Name:      "repo",
			FullName:  "example/repo",
		},
		Tag:    "latest",
		Digest: "sha256:abc123",
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, sample); err != nil {
		return fmt.Errorf("template execution failed: %w", err)
	}

	return nil
}

// RenderTemplate renders the template with the given payload.
// Returns nil, nil for empty string (caller falls back to default).
func RenderTemplate(tmplStr string, payload WebhookPayload) ([]byte, error) {
	if tmplStr == "" {
		return nil, nil
	}

	tmpl, err := template.New("webhook").Funcs(templateFuncs).Parse(tmplStr)
	if err != nil {
		return nil, fmt.Errorf("template parse error: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, payload); err != nil {
		return nil, fmt.Errorf("template render error: %w", err)
	}

	return buf.Bytes(), nil
}
