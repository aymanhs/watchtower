package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aymanhs/watchtower/internal/metric"
)

// executeHTTP fetches def's URL (through dialer — tunneled via SSH or
// direct, dialer doesn't care which) and decodes it as JSON. One
// Observation comes out per measurement whose configured field (default:
// a JSON field of the same name) resolves to a JSON number.
func executeHTTP(ctx context.Context, dialer Dialer, serverName string, def *metric.Definition, timeout time.Duration, config map[string]any) ([]Observation, error) {
	url, err := def.Collector.RenderURL(config)
	if err != nil {
		return nil, fmt.Errorf("metric %q on %s: %w", def.Name, serverName, err)
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
	}

	req, err := http.NewRequestWithContext(runCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("metric %q on %s: build request for %s: %w", def.Name, serverName, url, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("metric %q on %s: fetch %s: %w", def.Name, serverName, url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("metric %q on %s: read response from %s: %w", def.Name, serverName, url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metric %q on %s: %s returned HTTP %d: %s", def.Name, serverName, url, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("metric %q on %s: decode JSON from %s: %w", def.Name, serverName, url, err)
	}

	values := make(map[string]float64, len(def.Metrics))
	for key := range def.Metrics {
		field := key
		if mapped, ok := def.Collector.Fields[key]; ok {
			field = mapped
		}
		if v, ok := lookupNumber(decoded, field); ok {
			values[key] = v
		}
		// Missing or non-numeric: skipped, not an error — same tolerance
		// executeShell gives an absent key in its line-oriented output.
	}

	return observationsFrom(serverName, def, values), nil
}

// lookupNumber walks data by path's dot-separated segments (one level of
// nesting is all most monitoring JSON needs, e.g. "cluster.name") and
// reports whether what it finds is a JSON number. encoding/json decodes
// every JSON number into data as float64 when the target is `any`, which
// is exactly the type Observation.Value already uses everywhere.
func lookupNumber(data any, path string) (float64, bool) {
	cur := data
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0, false
		}
		cur, ok = m[part]
		if !ok {
			return 0, false
		}
	}
	f, ok := cur.(float64)
	return f, ok
}
