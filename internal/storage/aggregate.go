package storage

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"sort"
	"time"
)

// Bucket summarizes the points of one series that fall in one window.
// Start/End are clipped to the queried range; empty windows are not
// returned, so a gap in the result is a gap in the data.
type Bucket struct {
	Start, End time.Time
	Min, Max   float64
	Sum        float64
	Count      int
}

// Avg is the mean of the bucket's points.
func (b Bucket) Avg() float64 {
	if b.Count == 0 {
		return 0
	}
	return b.Sum / float64(b.Count)
}

// windowFloor returns the start of the window containing ts. Windows are
// aligned to multiples of window since the epoch; windows of a day or more
// align to local midnight so daily buckets match the user's calendar.
func windowFloor(ts time.Time, window time.Duration) time.Time {
	w := int64(window)
	n := ts.UnixNano()
	if window >= 24*time.Hour {
		_, off := ts.Zone()
		o := int64(off) * int64(time.Second)
		n += o
		return time.Unix(0, n-floorMod(n, w)-o)
	}
	return time.Unix(0, n-floorMod(n, w))
}

func floorMod(a, b int64) int64 {
	m := a % b
	if m < 0 {
		m += b
	}
	return m
}

// aggregator accumulates streaming min/max/sum/count per window for one
// series. Records may arrive slightly out of order, so buckets are keyed
// by window start and sorted at the end.
type aggregator struct {
	window  time.Duration
	from    time.Time
	to      time.Time
	buckets map[int64]*Bucket
}

func newAggregator(from, to time.Time, window time.Duration) *aggregator {
	return &aggregator{window: window, from: from, to: to, buckets: map[int64]*Bucket{}}
}

func (a *aggregator) add(ts time.Time, v float64) {
	if ts.Before(a.from) || ts.After(a.to) {
		return
	}
	start := windowFloor(ts, a.window)
	key := start.UnixNano()
	b := a.buckets[key]
	if b == nil {
		s, e := start, start.Add(a.window)
		if s.Before(a.from) {
			s = a.from
		}
		if e.After(a.to) {
			e = a.to
		}
		b = &Bucket{Start: s, End: e, Min: v, Max: v}
		a.buckets[key] = b
	}
	if v < b.Min {
		b.Min = v
	}
	if v > b.Max {
		b.Max = v
	}
	b.Sum += v
	b.Count++
}

func (a *aggregator) result() []Bucket {
	out := make([]Bucket, 0, len(a.buckets))
	for _, b := range a.buckets {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// AggregatePoints buckets an in-memory point stream. It is the pure core
// of QueryAgg.
func AggregatePoints(points []Point, from, to time.Time, window time.Duration) []Bucket {
	a := newAggregator(from, to, window)
	for _, p := range points {
		a.add(p.Timestamp, p.Value)
	}
	return a.result()
}

// QueryAgg returns, for each requested name, one Bucket per non-empty
// window of [from, to]. Each day file is streamed once however many names
// are requested. Unknown names are absent from the result.
func (s *Store) QueryAgg(names []string, from, to time.Time, window time.Duration) (map[string][]Bucket, error) {
	if window <= 0 {
		return nil, fmt.Errorf("storage: aggregate window must be positive")
	}
	byID := map[uint16]*aggregator{}
	nameOf := map[uint16]string{}
	for _, n := range names {
		if id, ok := s.catalog.lookupID(n); ok {
			byID[id] = newAggregator(from, to, window)
			nameOf[id] = n
		}
	}
	out := map[string][]Bucket{}
	if len(byID) == 0 {
		return out, nil
	}

	for _, date := range datesBetween(from, to) {
		if err := s.scanPartition(date, byID); err != nil {
			return nil, err
		}
	}
	for id, a := range byID {
		if r := a.result(); len(r) > 0 {
			out[nameOf[id]] = r
		}
	}
	return out, nil
}

func (s *Store) scanPartition(date string, byID map[uint16]*aggregator) error {
	f, err := os.Open(s.partitionPath(date))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("storage: read partition %s: %w", date, err)
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 1<<20)
	var rec [recordSize]byte
	for {
		if _, err := io.ReadFull(r, rec[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil // a torn trailing record is ignored, as in Query
			}
			return fmt.Errorf("storage: read partition %s: %w", date, err)
		}
		a := byID[binary.LittleEndian.Uint16(rec[0:2])]
		if a == nil {
			continue
		}
		ts := time.Unix(0, int64(binary.LittleEndian.Uint64(rec[2:10])))
		a.add(ts, math.Float64frombits(binary.LittleEndian.Uint64(rec[10:18])))
	}
}
