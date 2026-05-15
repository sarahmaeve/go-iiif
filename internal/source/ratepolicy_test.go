package source

import (
	"testing"
	"time"

	"golang.org/x/time/rate"
)

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

func TestWithRatePolicy_BuildsPerHostLimiter(t *testing.T) {
	pf := NewPoliteFetcher(nil, WithRatePolicy(RatePolicy{
		Default: HostPolicy{MinInterval: 2 * time.Second, Burst: 1},
		ByHost:  map[string]HostPolicy{"gallica.bnf.fr": {MinInterval: 13 * time.Second, Burst: 2}},
	}))

	g, ok := pf.limiterFor("gallica.bnf.fr").(*rate.Limiter)
	if !ok {
		t.Fatalf("gallica limiter is %T, want *rate.Limiter", pf.limiterFor("gallica.bnf.fr"))
	}
	if g.Limit() != rate.Every(13*time.Second) || g.Burst() != 2 {
		t.Fatalf("gallica limiter = %v/%d, want %v/2", g.Limit(), g.Burst(), rate.Every(13*time.Second))
	}

	o := pf.limiterFor("other.example.org").(*rate.Limiter)
	if o.Limit() != rate.Every(2*time.Second) || o.Burst() != 1 {
		t.Fatalf("default limiter = %v/%d, want %v/1", o.Limit(), o.Burst(), rate.Every(2*time.Second))
	}
}
