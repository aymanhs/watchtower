package metric

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Emitter turns an object's decoded JSON value into one number, so an
// object can feed ordinary metrics (charts, history, alert rules). Forms:
//
//	count                      rows in a list (1 for anything else)
//	count where FIELD OP VALUE rows matching, OP one of = != > >= < <=
//	sum|min|max|avg FIELD      over the rows' numeric FIELD
//	value PATH                 a number at a dotted path (items.0.n)
//
// FIELD and PATH are dotted paths into each row / the whole value.
type Emitter struct {
	op, field, cmp, arg string
}

var whereRe = regexp.MustCompile(`^(\S+?)\s*(>=|<=|!=|==|=|>|<)\s*(.+)$`)

// ParseEmit parses one emit expression.
func ParseEmit(expr string) (Emitter, error) {
	op, rest, _ := strings.Cut(strings.TrimSpace(expr), " ")
	rest = strings.TrimSpace(rest)
	switch op {
	case "count":
		if rest == "" {
			return Emitter{op: op}, nil
		}
		cond, ok := strings.CutPrefix(rest, "where ")
		m := whereRe.FindStringSubmatch(strings.TrimSpace(cond))
		if !ok || m == nil {
			return Emitter{}, fmt.Errorf("emit %q: want \"count\" or \"count where FIELD OP VALUE\"", expr)
		}
		return Emitter{op: op, field: m[1], cmp: m[2], arg: strings.Trim(m[3], `"'`)}, nil
	case "sum", "min", "max", "avg", "value":
		if rest == "" || strings.ContainsAny(rest, " \t") {
			return Emitter{}, fmt.Errorf("emit %q: want \"%s PATH\"", expr, op)
		}
		return Emitter{op: op, field: rest}, nil
	}
	return Emitter{}, fmt.Errorf("emit %q: unknown form", expr)
}

// Eval computes the number from v (a json.Unmarshal into any result). ok
// is false when there is nothing to compute (no matching rows, missing
// path).
func (e Emitter) Eval(v any) (float64, bool) {
	rows, isList := v.([]any)
	if !isList {
		rows = []any{v}
	}
	switch e.op {
	case "count":
		n := 0
		for _, r := range rows {
			if e.field == "" || e.matches(r) {
				n++
			}
		}
		return float64(n), true
	case "value":
		f, ok := asNumber(lookup(v, e.field))
		return f, ok
	}
	var vals []float64
	for _, r := range rows {
		if f, ok := asNumber(lookup(r, e.field)); ok {
			vals = append(vals, f)
		}
	}
	if len(vals) == 0 {
		return 0, e.op == "sum"
	}
	acc := vals[0]
	total := 0.0
	for _, f := range vals {
		total += f
		if (e.op == "min" && f < acc) || (e.op == "max" && f > acc) {
			acc = f
		}
	}
	switch e.op {
	case "sum":
		return total, true
	case "avg":
		return total / float64(len(vals)), true
	}
	return acc, true
}

func (e Emitter) matches(row any) bool {
	got := lookup(row, e.field)
	if got == nil {
		return false
	}
	gf, gnum := asNumber(got)
	af, anum := strconv.ParseFloat(e.arg, 64)
	if gnum && anum == nil {
		switch e.cmp {
		case "=", "==":
			return gf == af
		case "!=":
			return gf != af
		case ">":
			return gf > af
		case ">=":
			return gf >= af
		case "<":
			return gf < af
		case "<=":
			return gf <= af
		}
	}
	s := fmt.Sprint(got)
	switch e.cmp {
	case "=", "==":
		return s == e.arg
	case "!=":
		return s != e.arg
	}
	return false
}

func lookup(v any, path string) any {
	for _, seg := range strings.Split(path, ".") {
		switch x := v.(type) {
		case map[string]any:
			v = x[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(x) {
				return nil
			}
			v = x[i]
		default:
			return nil
		}
	}
	return v
}

func asNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}
