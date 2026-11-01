package metric

import (
	"bytes"
	"fmt"
	"text/template"
)

// renderTemplate substitutes config into tmplStr — a Go text template, so
// a single generic definition (e.g. "nats") can run against any server by
// varying config rather than hand-writing a command per server. This is
// the brief's own server-config example made real:
//
//	metrics:
//	  nats:
//	    config:
//	      endpoint: localhost:8222
//
// with the definition's command or url containing "{{.endpoint}}".
//
// config may be empty — the overwhelming majority of definitions (cpu,
// memory, ...) have no template directives at all, so rendering them is a
// no-op; there is deliberately no separate "has config" code path.
//
// A reference to a key config doesn't have is a hard error
// (missingkey=error): a server missing a required parameter must fail
// loudly, the same way a malformed collector output does, never silently
// run with "<no value>" spliced into the command or URL.
func renderTemplate(tmplStr string, config map[string]any) (string, error) {
	tmpl, err := template.New("collector").Option("missingkey=error").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, config); err != nil {
		return "", fmt.Errorf("render template: %w", err)
	}
	return buf.String(), nil
}

// Render substitutes config into the shell collector's Command. See
// renderTemplate.
func (c Collector) Render(config map[string]any) (string, error) {
	return renderTemplate(c.Command, config)
}

// RenderURL substitutes config into the http collector's URL. See
// renderTemplate.
func (c Collector) RenderURL(config map[string]any) (string, error) {
	return renderTemplate(c.URL, config)
}
