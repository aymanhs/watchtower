package state

import (
	"fmt"
	"sort"

	"aymanhs/watchtower/internal/metric"
)

// ResolveScale turns a metric.Scale (as written in YAML — a literal Max,
// or a MaxRef naming a sibling measurement) into a concrete Scale for this
// collection cycle. siblingValues holds every measurement's raw value
// from the same cycle, keyed by measurement key, so a MaxRef like "total"
// always reflects the actual reading, never a hardcoded number.
//
// s == nil returns (nil, nil): no scale configured, nothing to resolve.
func ResolveScale(s *metric.Scale, siblingValues map[string]float64) (*Scale, error) {
	if s == nil {
		return nil, nil
	}
	max := s.Max
	if s.MaxRef != "" {
		v, ok := siblingValues[s.MaxRef]
		if !ok {
			return nil, fmt.Errorf("scale.max_ref %q not present in this cycle's measurements", s.MaxRef)
		}
		max = v
	}
	if max == s.Min {
		return nil, fmt.Errorf("resolved scale max (%v) equals min (%v)", max, s.Min)
	}
	return &Scale{Min: s.Min, Max: max}, nil
}

// ResolveZones derives a Gauge's colored threshold bands directly from the
// measurement's own event rules — not a separate config, so the dial
// always matches whatever actually triggers an alert. Direction-aware:
// an "above" rule (e.g. disk.used_percent) colors high values worse; a
// "below" rule (e.g. memory.available) colors low values worse. Rules are
// assumed to share one direction; if they don't, the first rule's
// direction wins (a genuinely mixed-direction rule set on one measurement
// would be a self-contradictory config anyway).
//
// Returns nil if rules is empty — a measurement with no alerting rules
// gets a plain, unzoned gauge.
func ResolveZones(rules []metric.EventRule, scale Scale) []Zone {
	if len(rules) == 0 {
		return nil
	}

	type point struct {
		threshold float64
		severity  metric.Severity
	}
	pts := make([]point, len(rules))
	below := false
	for i, r := range rules {
		pts[i] = point{r.Threshold, r.Severity}
		if r.Condition == metric.ConditionBelow {
			below = true
		}
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].threshold < pts[j].threshold })

	zones := make([]Zone, 0, len(pts)+1)
	if below {
		// Smallest threshold is the most severe: below it is the worst
		// zone. Each subsequent threshold's band ends at that threshold,
		// colored by that threshold's own severity. Above the highest
		// threshold is normal.
		prev := scale.Min
		for _, p := range pts {
			zones = append(zones, Zone{From: prev, To: p.threshold, Severity: p.severity})
			prev = p.threshold
		}
		zones = append(zones, Zone{From: prev, To: scale.Max, Severity: ""})
	} else {
		// Below the lowest threshold is normal. Each threshold's band
		// starts there and runs to the next threshold (or Max), colored
		// by that starting threshold's own severity.
		zones = append(zones, Zone{From: scale.Min, To: pts[0].threshold, Severity: ""})
		prev := pts[0].threshold
		for i, p := range pts {
			to := scale.Max
			if i+1 < len(pts) {
				to = pts[i+1].threshold
			}
			zones = append(zones, Zone{From: prev, To: to, Severity: p.severity})
			prev = to
		}
	}
	return zones
}
