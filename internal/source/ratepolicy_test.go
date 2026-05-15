package source

import (
	"context"
	"errors"
	neturl "net/url"
	"testing"
	"testing/synctest"
	"time"
)

func TestDefaultRatePolicy_BaseAndJitter(t *testing.T) {
	d := DefaultRatePolicy().Default
	if d.MinInterval != 750*time.Millisecond {
		t.Fatalf("Default.MinInterval = %v, want 750ms", d.MinInterval)
	}
	if d.Jitter != 600*time.Millisecond {
		t.Fatalf("Default.Jitter = %v, want 600ms", d.Jitter)
	}
	if g := DefaultRatePolicy().For("gallica.bnf.fr"); g.MinInterval != 13*time.Second || g.Jitter != 0 {
		t.Fatalf("Gallica = %+v, want 13s MinInterval and no jitter", g)
	}
}

func TestJitterPad_BoundsAndStep(t *testing.T) {
	const step = 30 * time.Millisecond
	for range 2000 {
		p := jitterPad(600 * time.Millisecond)
		if p < step || p > 600*time.Millisecond {
			t.Fatalf("pad %v outside [30ms, 600ms]", p)
		}
		if p%step != 0 {
			t.Fatalf("pad %v is not a 30ms multiple", p)
		}
	}
}

// recordLimiter is a fake base limiter that never blocks, so jitterLimiter's
// own pad/ctx behavior is what's under test.
type recordLimiter struct{ calls int }

func (r *recordLimiter) Wait(context.Context) error { r.calls++; return nil }

func TestJitterLimiter_WaitsBaseThenPad(t *testing.T) {
	base := &recordLimiter{}
	jl := &jitterLimiter{base: base, maxPad: 30 * time.Millisecond} // steps=1 → exactly 30ms
	start := time.Now()
	if err := jl.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if base.calls != 1 {
		t.Fatalf("base.Wait calls = %d, want 1", base.calls)
	}
	if d := time.Since(start); d < 28*time.Millisecond {
		t.Fatalf("elapsed %v, want the ~30ms pad applied", d)
	}
}

func TestJitterLimiter_HonorsContextDuringPad(t *testing.T) {
	jl := &jitterLimiter{base: &recordLimiter{}, maxPad: 600 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := jl.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait err = %v, want context.Canceled (pad must honor ctx)", err)
	}
}

func TestRatePolicy_For(t *testing.T) {
	rp := RatePolicy{
		Default: HostPolicy{MinInterval: time.Second, Burst: 1},
		ByHost: map[string]HostPolicy{
			"slow.example.org": {MinInterval: 30 * time.Second, Burst: 1},
		},
	}

	if got := rp.For("unknown.example.org"); got != rp.Default {
		t.Fatalf("unknown host = %+v, want Default %+v", got, rp.Default)
	}
	if got := rp.For("slow.example.org"); got.MinInterval != 30*time.Second {
		t.Fatalf("slow host MinInterval = %v, want 30s", got.MinInterval)
	}
}

func TestDefaultRatePolicy_GallicaIsGentle(t *testing.T) {
	rp := DefaultRatePolicy()

	if got := rp.For("gallica.bnf.fr").MinInterval; got != 13*time.Second {
		t.Fatalf("Gallica MinInterval = %v, want 13s built-in", got)
	}
	if got := rp.For("iiif.bodleian.ox.ac.uk"); got != rp.Default {
		t.Fatalf("non-overridden host = %+v, want Default %+v", got, rp.Default)
	}
	if rp.Default.MinInterval <= 0 {
		t.Fatalf("Default.MinInterval = %v, want a positive gentle default", rp.Default.MinInterval)
	}
}

// stampingFetcher records the synthetic time of each Fetch, keyed by host.
type stampingFetcher struct {
	start time.Time
	at    map[string][]time.Duration
}

func (s *stampingFetcher) Fetch(_ context.Context, rawURL string) ([]byte, error) {
	u, _ := neturl.Parse(rawURL)
	s.at[u.Host] = append(s.at[u.Host], time.Since(s.start))
	return []byte("x"), nil
}

// TestWithRatePolicy_SpacesRequestsPerHost asserts the *observable* behavior:
// requests to a host are spaced by that host's MinInterval, and a different
// host uses the Default. Deterministic via testing/synctest — no real waiting,
// no peeking at limiter internals.
func TestWithRatePolicy_SpacesRequestsPerHost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &stampingFetcher{start: time.Now(), at: map[string][]time.Duration{}}
		pf := NewPoliteFetcher(rec, WithRatePolicy(RatePolicy{
			Default: HostPolicy{MinInterval: 2 * time.Second, Burst: 1},
			ByHost:  map[string]HostPolicy{"slow.example.org": {MinInterval: 13 * time.Second, Burst: 1}},
		}))
		ctx := context.Background()

		for range 3 {
			if _, err := pf.Fetch(ctx, "https://slow.example.org/x"); err != nil {
				t.Fatalf("Fetch slow: %v", err)
			}
		}
		for range 2 {
			if _, err := pf.Fetch(ctx, "https://def.example.org/y"); err != nil {
				t.Fatalf("Fetch def: %v", err)
			}
		}

		slow := rec.at["slow.example.org"]
		if len(slow) != 3 {
			t.Fatalf("slow host calls = %d, want 3", len(slow))
		}
		if slow[0] != 0 || slow[1]-slow[0] != 13*time.Second || slow[2]-slow[1] != 13*time.Second {
			t.Fatalf("slow host timings = %v, want first immediate then 13s apart", slow)
		}

		def := rec.at["def.example.org"]
		if len(def) != 2 {
			t.Fatalf("default host calls = %d, want 2", len(def))
		}
		if def[1]-def[0] != 2*time.Second {
			t.Fatalf("default host spacing = %v, want Default 2s", def[1]-def[0])
		}
	})
}
