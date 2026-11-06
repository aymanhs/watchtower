package demo

import (
	"hash/fnv"
	"math"
	"time"
)

// Every signal is a pure function of (seed, time), so history can be
// backfilled for any past instant and live values continue the same curves.

// bootEpoch anchors cumulative counters (network bytes) so they are
// monotonic and stable across restarts.
const bootEpoch = 1_700_000_000.0

func hash01(seed string, n int64) float64 {
	h := fnv.New64a()
	h.Write([]byte(seed))
	var b [8]byte
	for i := range b {
		b[i] = byte(n >> (8 * i))
	}
	h.Write(b[:])
	return float64(h.Sum64()>>11) / float64(1<<53)
}

func secs(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

// phase derives a stable per-seed phase offset in radians.
func phase(seed string) float64 { return hash01(seed, -1) * 2 * math.Pi }

// wave is a sine of the given period in [-1, 1].
func wave(seed string, t time.Time, period time.Duration) float64 {
	return math.Sin(2*math.Pi*secs(t)/period.Seconds() + phase(seed+period.String()))
}

// noise is per-second jitter in [-1, 1].
func noise(seed string, t time.Time) float64 {
	return hash01(seed, t.Unix())*2 - 1
}

// smoothNoise interpolates per-bucket random values, giving a slowly
// wandering signal in [-1, 1].
func smoothNoise(seed string, t time.Time, bucket time.Duration) float64 {
	x := secs(t) / bucket.Seconds()
	i := int64(math.Floor(x))
	f := x - float64(i)
	f = f * f * (3 - 2*f)
	a, b := hash01(seed, i)*2-1, hash01(seed, i+1)*2-1
	return a + (b-a)*f
}

// burst returns 0..1: a smooth bump lasting dur once per period, at a
// per-seed offset.
func burst(seed string, t time.Time, period, dur time.Duration) float64 {
	off := hash01(seed, -2) * period.Seconds()
	pos := math.Mod(secs(t)+off, period.Seconds())
	if pos > dur.Seconds() {
		return 0
	}
	return math.Sin(math.Pi * pos / dur.Seconds())
}

// sawtooth rises 0..1 over period, then drops back (a leak and its restart).
func sawtooth(seed string, t time.Time, period time.Duration) float64 {
	off := hash01(seed, -3) * period.Seconds()
	return math.Mod(secs(t)+off, period.Seconds()) / period.Seconds()
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// counter is the integral of rate*(1 + amp*sin) since bootEpoch, so the
// derivative of the series follows a periodic pattern.
func counter(seed string, t time.Time, rate, amp float64, period time.Duration) float64 {
	w := 2 * math.Pi / period.Seconds()
	ph := phase(seed + "counter")
	x := secs(t)
	osc := amp * (math.Cos(ph+w*bootEpoch) - math.Cos(ph+w*x)) / w
	return math.Max(0, rate*((x-bootEpoch)+osc))
}
