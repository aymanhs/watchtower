package metric

import (
	"encoding/json"
	"testing"
)

func TestEmit(t *testing.T) {
	var v any
	json.Unmarshal([]byte(`[{"name":"web","state":"running","cpu":2.5},{"name":"api","state":"restarting","cpu":7.5},{"name":"db","state":"running","cpu":1}]`), &v)
	for expr, want := range map[string]float64{
		"count":                        3,
		"count where state != running": 1,
		"count where state = running":  2,
		"count where cpu > 2":          2,
		"sum cpu":                      11,
		"max cpu":                      7.5,
		"min cpu":                      1,
		"value 1.cpu":                  7.5,
		"count where name=\"web\"":     1,
	} {
		e, err := ParseEmit(expr)
		if err != nil {
			t.Fatalf("%q: %v", expr, err)
		}
		if got, ok := e.Eval(v); !ok || got != want {
			t.Errorf("%q = %v,%v want %v", expr, got, ok, want)
		}
	}
	for _, bad := range []string{"", "median x", "count x", "max", "count where state"} {
		if _, err := ParseEmit(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
