package metric

import (
	"bytes"
	"fmt"
	"text/template"
)

// Render substitutes config into the collector's command — a Go text
// template, so a single generic definition (e.g. "nats") can run against
// any server by varying config rather than hand-writing a command per
// server. This is the brief's own server-config example made real:
//
//	metrics:
//	  nats:
//	    config:
//	      endpoint: localhost:8222
//
// with the definition's command containing "{{.endpoint}}".
//
// config may be empty — the overwhelming majority of definitions (cpu,
// memory, ...) have no template directives at all, so rendering them is a
// no-op; there is deliberately no separate "has config" code path.
//
// A reference to a key config doesn't have is a hard error
// (missingkey=error): a server missing a required parameter must fail
// loudly, the same way a malformed collector output does, never
// silently run with "<no value>" spliced into the command.
func (c Collector) Render(config map[string]any) (string, error) {
	tmpl, err := template.New("collector").Option("missingkey=error").Parse(c.Command)
	if err != nil {
		return "", fmt.Errorf("parse command template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, config); err != nil {
		return "", fmt.Errorf("render command template: %w", err)
	}
	return buf.String(), nil
}
