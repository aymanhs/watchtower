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
	mu             sync.Mutex
	thresholds     map[string]*thresholdState
	operational    map[string]*operationalState
	history        []Event
	maxHistory     int
	nextID         int64
	listeners      map[int]func(Event)
	nextListenerID int
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
// subset of def.Events targeting ruleKey, returning any Event the
// transition produced (nil if the state didn't change). Call once per
// observation.
//
// measurement and ruleKey are the same string for an ordinarily-declared
// measurement — they only diverge for a dynamically per-instance one
// (disk.yaml's per-volume stats: "root_used_percent", "data_used_percent"
// emitted from one declared "used_percent" rule set). measurement keeps
// each instance's alarm state and identity separate ("disk.root is at
// 92%", not one ambiguous "disk is at 92%" shared across every volume);
// ruleKey is the name event rules were actually declared under, so one
// rule set still applies to however many instances a collector emits.
func (e *Engine) EvaluateThreshold(serverName string, def *metric.Definition, measurement, ruleKey string, value float64, ts time.Time) *Event {
	rules := rulesFor(def, ruleKey)
	if len(rules) == 0 {
		return nil
	}
	ev := e.evaluateThresholdLocked(serverName, def, measurement, rules, value, ts)
	if ev != nil {
		e.fire(*ev)
	}
	return ev
}

// evaluateThresholdLocked holds e.mu for the whole decision — the exact
// body EvaluateThreshold itself used to run under its own defer'd unlock,
// split out so EvaluateThreshold can fire listeners (OnEvent) after the
// lock is released instead of while still held.
func (e *Engine) evaluateThresholdLocked(serverName string, def *metric.Definition, measurement string, rules []metric.EventRule, value float64, ts time.Time) *Event {
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
	ev := e.evaluateCollectorResultLocked(serverName, metricName, collectErr, ts)
	if ev != nil {
		e.fire(*ev)
	}
	return ev
}

// evaluateCollectorResultLocked is EvaluateCollectorResult's own former
// body, split out for the same reason as evaluateThresholdLocked.
func (e *Engine) evaluateCollectorResultLocked(serverName, metricName string, collectErr error, ts time.Time) *Event {
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

// CurrentSeverity returns the active severity for one measurement — "" if
// normal or never evaluated. measurement is "metricName.measurementKey"
// (e.g. "memory.available"), matching the key shape EvaluateThreshold uses
// internally. This is a steady-state query, independent of whether the
// last evaluation produced a transition Event.
func (e *Engine) CurrentSeverity(serverName, measurement string) metric.Severity {
	e.mu.Lock()
	defer e.mu.Unlock()
	if st, ok := e.thresholds[serverName+"/"+measurement]; ok {
		return st.severity
	}
	return ""
}

// IsFailing reports whether a (server, metric) collector is currently
// considered failing — the steady-state counterpart to
// EvaluateCollectorResult's transition events.
func (e *Engine) IsFailing(serverName, metricName string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if st, ok := e.operational[serverName+"/"+metricName]; ok {
		return st.failing
	}
	return false
}

// History returns a snapshot of all events recorded so far, oldest first.
func (e *Engine) History() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Event, len(e.history))
	copy(out, e.history)
	return out
}

// Seed loads previously persisted events (oldest first) into the history
// and continues ID assignment after the highest one. It restores history
// only, not alarm state: a still-failing target simply raises again on
// its next evaluation. Call before any evaluation.
func (e *Engine) Seed(events []Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ev := range events {
		if ev.ID > e.nextID {
			e.nextID = ev.ID
		}
	}
	e.history = append(append([]Event(nil), events...), e.history...)
	if len(e.history) > e.maxHistory {
		e.history = e.history[len(e.history)-e.maxHistory:]
	}
}

// OnEvent registers fn to be called, outside the engine's own lock,
// whenever EvaluateThreshold or EvaluateCollectorResult produces a
// transition Event — the hook a presentation layer uses to react to new
// events as they happen (a desktop notification, say) instead of diffing
// History() on a timer. Call off to unregister.
func (e *Engine) OnEvent(fn func(Event)) (off func()) {
	e.mu.Lock()
	if e.listeners == nil {
		e.listeners = make(map[int]func(Event))
	}
	id := e.nextListenerID
	e.nextListenerID++
	e.listeners[id] = fn
	e.mu.Unlock()
	return func() {
		e.mu.Lock()
		delete(e.listeners, id)
		e.mu.Unlock()
	}
}

// fire runs every registered OnEvent listener with ev. Listeners are
// snapshotted under the lock then run without it held, so a listener is
// free to call back into the Engine (History, Acknowledge, ...) without
// deadlocking.
func (e *Engine) fire(ev Event) {
	e.mu.Lock()
	listeners := make([]func(Event), 0, len(e.listeners))
	for _, fn := range e.listeners {
		listeners = append(listeners, fn)
	}
	e.mu.Unlock()
	for _, fn := range listeners {
		fn(ev)
	}
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
