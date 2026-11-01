package event

import (
	"sort"
	"sync"
	"time"

	"aymanhs/watchtower/internal/metric"
)

// thresholdState is the per-measurement hysteresis state tracked between
// evaluations. severity == "" means normal.
type thresholdState struct {
	severity metric.Severity
	ruleName string // the currently-active rule; its own Recovered() gates stepping down
}

// operationalState tracks whether a (server, metric) collector is
// currently considered failing.
type operationalState struct {
	failing bool
}

// Engine evaluates observations against event rules and keeps lifecycle
// state and a bounded history, all in memory.
type Engine struct {
	mu          sync.Mutex
	thresholds  map[string]*thresholdState
	operational map[string]*operationalState
	history     []Event
	maxHistory  int
	nextID      int64
}

// NewEngine returns an Engine that keeps up to maxHistory events (oldest
// dropped first). maxHistory <= 0 defaults to 1000.
func NewEngine(maxHistory int) *Engine {
	if maxHistory <= 0 {
		maxHistory = 1000
	}
	return &Engine{
		thresholds:  make(map[string]*thresholdState),
		operational: make(map[string]*operationalState),
		maxHistory:  maxHistory,
	}
}

// EvaluateThreshold checks one measurement's current value against the
// subset of def.Events targeting it, returning any Event the transition
// produced (nil if the state didn't change). Call once per observation.
func (e *Engine) EvaluateThreshold(serverName string, def *metric.Definition, measurement string, value float64, ts time.Time) *Event {
	rules := rulesFor(def, measurement)
	if len(rules) == 0 {
		return nil
	}
	key := serverName + "/" + def.Name + "." + measurement
	metricName := def.Name + "." + measurement

	e.mu.Lock()
	defer e.mu.Unlock()

	st, ok := e.thresholds[key]
	if !ok {
		st = &thresholdState{}
		e.thresholds[key] = st
	}

	breaching := highestBreaching(rules, value)

	if st.severity == "" {
		if breaching == nil {
			return nil
		}
		st.severity, st.ruleName = breaching.Severity, breaching.Name
		ev := e.record(Event{
			Category: CategoryThreshold, Kind: KindRaised,
			Server: serverName, Metric: metricName,
			RuleName: breaching.Name, Severity: breaching.Severity,
			Value: value, Threshold: breaching.Threshold, Timestamp: ts,
		})
		return &ev
	}

	// Already active at some severity.
	if breaching != nil && breaching.Name == st.ruleName {
		return nil // unchanged — dedup
	}
	if breaching != nil && severityRank(breaching.Severity) > severityRank(st.severity) {
		st.severity, st.ruleName = breaching.Severity, breaching.Name
		ev := e.record(Event{
			Category: CategoryThreshold, Kind: KindRaised,
			Server: serverName, Metric: metricName,
			RuleName: breaching.Name, Severity: breaching.Severity,
			Value: value, Threshold: breaching.Threshold, Timestamp: ts,
		})
		return &ev
	}

	// breaching is nil, or a lower severity than the currently active
	// rule: only step down once the *active* rule's own recovery
	// condition is satisfied (hysteresis) — don't flap on every dip below
	// the original threshold.
	active := ruleByName(rules, st.ruleName)
	if active == nil || !active.Recovered(value) {
		return nil
	}
	if breaching != nil {
		st.severity, st.ruleName = breaching.Severity, breaching.Name
		ev := e.record(Event{
			Category: CategoryThreshold, Kind: KindRecovered,
			Server: serverName, Metric: metricName,
			RuleName: breaching.Name, Severity: breaching.Severity,
			Value: value, Threshold: breaching.Threshold, Timestamp: ts,
		})
		return &ev
	}
	st.severity, st.ruleName = "", ""
	ev := e.record(Event{
		Category: CategoryThreshold, Kind: KindRecovered,
		Server: serverName, Metric: metricName,
		Value: value, Timestamp: ts, Message: "recovered to normal",
	})
	return &ev
}

// EvaluateCollectorResult tracks whether a (server, metric) collector is
// currently failing, emitting an operational Event only on the transition
// into or out of failure — never on every repeated failure.
func (e *Engine) EvaluateCollectorResult(serverName, metricName string, collectErr error, ts time.Time) *Event {
	key := serverName + "/" + metricName

	e.mu.Lock()
	defer e.mu.Unlock()

	st, ok := e.operational[key]
	if !ok {
		st = &operationalState{}
		e.operational[key] = st
	}

	switch {
	case collectErr != nil && !st.failing:
		st.failing = true
		ev := e.record(Event{
			Category: CategoryOperational, Kind: KindRaised,
			Server: serverName, Metric: metricName,
			Severity: metric.SeverityCritical,
			Message:  collectErr.Error(), Timestamp: ts,
		})
		return &ev
	case collectErr == nil && st.failing:
		st.failing = false
		ev := e.record(Event{
			Category: CategoryOperational, Kind: KindRecovered,
			Server: serverName, Metric: metricName,
			Message: "collector recovered", Timestamp: ts,
		})
		return &ev
	default:
		return nil
	}
}

// History returns a snapshot of all events recorded so far, oldest first.
func (e *Engine) History() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Event, len(e.history))
	copy(out, e.history)
	return out
}

// Acknowledge marks the event with the given ID as acknowledged. It
// reports whether an event with that ID was found.
func (e *Engine) Acknowledge(id int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.history {
		if e.history[i].ID == id {
			e.history[i].Acknowledged = true
			return true
		}
	}
	return false
}

// record assigns an ID and appends to history. Caller must hold e.mu.
func (e *Engine) record(ev Event) Event {
	e.nextID++
	ev.ID = e.nextID
	e.history = append(e.history, ev)
	if len(e.history) > e.maxHistory {
		e.history = e.history[len(e.history)-e.maxHistory:]
	}
	return ev
}

func severityRank(s metric.Severity) int {
	switch s {
	case metric.SeverityCritical:
		return 2
	case metric.SeverityWarning:
		return 1
	default:
		return 0
	}
}

// rulesFor returns def's event rules targeting measurement, highest
// severity first, so the first breaching rule found is the worst one.
func rulesFor(def *metric.Definition, measurement string) []metric.EventRule {
	var out []metric.EventRule
	for _, r := range def.Events {
		if r.Measurement == measurement {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return severityRank(out[i].Severity) > severityRank(out[j].Severity) })
	return out
}

func highestBreaching(rules []metric.EventRule, value float64) *metric.EventRule {
	for i := range rules {
		if rules[i].Breached(value) {
			return &rules[i]
		}
	}
	return nil
}

func ruleByName(rules []metric.EventRule, name string) *metric.EventRule {
	for i := range rules {
		if rules[i].Name == name {
			return &rules[i]
		}
	}
	return nil
}
