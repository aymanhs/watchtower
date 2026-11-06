package storage

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
	"time"
)

func TestAggregatePoints_BucketsAndClips(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 30, 0, time.UTC)
	pts := []Point{
		{base, 1},
		{base.Add(10 * time.Second), 3},
		{base.Add(5 * time.Minute), 10}, // 10:05:30, skips the 10:01-10:04 windows
	}
	from, to := base, base.Add(6*time.Minute)
	got := AggregatePoints(pts, from, to, time.Minute)
	if len(got) != 2 {
		t.Fatalf("want 2 buckets (empty windows skipped), got %d: %+v", len(got), got)
	}
	b := got[0]
	if b.Count != 2 || b.Min != 1 || b.Max != 3 || b.Avg() != 2 {
		t.Fatalf("bucket 0 = %+v", b)
	}
	if !b.Start.Equal(from) { // clipped: window starts 10:00:00 but range starts 10:00:30
		t.Fatalf("first bucket start not clipped: %v", b.Start)
	}
	if !got[1].Start.Equal(time.Date(2026, 1, 1, 10, 5, 0, 0, time.UTC)) {
		t.Fatalf("bucket 1 start = %v", got[1].Start)
	}
}

func TestAggregatePoints_OutOfRangeIgnored(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := AggregatePoints([]Point{{base.Add(-time.Hour), 1}, {base.Add(2 * time.Hour), 2}}, base, base.Add(time.Hour), time.Minute)
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestStore_QueryAggAcrossDaysAndNames(t *testing.T) {
	s, err := Open(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d1 := time.Date(2026, 3, 1, 23, 59, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		ts := d1.Add(time.Duration(i) * 30 * time.Second) // crosses midnight
		if err := s.Append("a", ts, float64(i)); err != nil {
			t.Fatal(err)
		}
		if err := s.Append("b", ts, 100); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.QueryAgg([]string{"a", "b", "missing"}, d1, d1.Add(5*time.Minute), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res["missing"]; ok {
		t.Fatal("unknown name must be absent")
	}
	a := res["a"]
	if len(a) != 2 { // 23:00 hour and 00:00 hour
		t.Fatalf("a buckets = %+v", a)
	}
	if a[0].Count+a[1].Count != 4 || math.Abs(res["b"][0].Avg()-100) > 1e-9 {
		t.Fatalf("unexpected: %+v %+v", a, res["b"])
	}
}

// BenchmarkQueryAgg_30d measures a 30-day, 400-series scan at 15s cadence
// to decide whether stored rollups are needed.
func BenchmarkQueryAgg_30d(b *testing.B) {
	s, err := Open(b.TempDir(), 0)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	const series, days = 400, 30
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	names := make([]string, series)
	for i := range names {
		names[i] = "srv" + string(rune('a'+i%26)) + "." + string(rune('a'+i/26)) + ".m"
	}
	ids := make([]uint16, series)
	for i, n := range names {
		id, err := s.catalog.idFor(n)
		if err != nil {
			b.Fatal(err)
		}
		ids[i] = id
	}
	// Day files are written directly: 69M Append syscalls would dominate setup.
	for d := 0; d < days; d++ {
		day := start.AddDate(0, 0, d)
		buf := make([]byte, 0, (86400/15)*series*recordSize)
		var rec [recordSize]byte
		for step := 0; step < 86400/15; step++ {
			ts := day.Add(time.Duration(step) * 15 * time.Second)
			for i := range names {
				binary.LittleEndian.PutUint16(rec[0:2], ids[i])
				binary.LittleEndian.PutUint64(rec[2:10], uint64(ts.UnixNano()))
				binary.LittleEndian.PutUint64(rec[10:18], math.Float64bits(float64((step+i)%100)))
				buf = append(buf, rec[:]...)
			}
		}
		if err := os.WriteFile(s.partitionPath(day.Format(dateLayout)), buf, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	to := start.AddDate(0, 0, days)
	for _, tc := range []struct {
		name string
		span time.Duration
		win  time.Duration
		n    int
	}{{"1h", time.Hour, time.Minute, 1}, {"1d", 24 * time.Hour, 5 * time.Minute, 1}, {"7d", 7 * 24 * time.Hour, time.Hour, 1}, {"30d", 30 * 24 * time.Hour, 6 * time.Hour, 1}, {"30d-all", 30 * 24 * time.Hour, 6 * time.Hour, series}} {
		b.Run(tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := s.QueryAgg(names[:tc.n], to.Add(-tc.span), to, tc.win); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
