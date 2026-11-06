package collect

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// maxExcerpt bounds how much collector output is quoted in an error, which
// becomes an event message (and lands in the events log and exports).
const maxExcerpt = 300

// Excerpt returns s trimmed, with newlines shown as " | " and cut to a
// bounded length, for quoting collector output in an error message.
func Excerpt(s string) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(s), "\n", " | ")), " ")
	if len(s) > maxExcerpt {
		s = s[:maxExcerpt] + "…"
	}
	return s
}

// ParseLineOutput parses the collector output contract: one "key=value"
// pair per line, values numeric. Blank lines are skipped. Any malformed
// line is a hard error — malformed output must never be silently read as
// zero or dropped.
func ParseLineOutput(output string) (map[string]float64, error) {
	result := make(map[string]float64)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		key, valStr, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("malformed output line %q: expected key=value", line)
		}
		key = strings.TrimSpace(key)
		valStr = strings.TrimSpace(valStr)
		if key == "" {
			return nil, fmt.Errorf("malformed output line %q: empty key", line)
		}
		val, err := strconv.ParseFloat(valStr, 64)
		if err != nil {
			return nil, fmt.Errorf("malformed output line %q: value %q is not numeric: %w", line, valStr, err)
		}
		if _, dup := result[key]; dup {
			return nil, fmt.Errorf("duplicate key %q in collector output", key)
		}
		result[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
