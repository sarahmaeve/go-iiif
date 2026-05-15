package source

import (
	"context"
	"math/rand/v2"
	"time"

	"golang.org/x/time/rate"
)

// jitterStep is the granularity of the random politeness pad: the pad is a
// uniform multiple of this in (0, HostPolicy.Jitter].
const jitterStep = 30 * time.Millisecond

// HostPolicy is the politeness contract for one host.
type HostPolicy struct {
	// MinInterval is the minimum spacing between requests to the host.
	MinInterval time.Duration
	// Burst is the token-bucket burst; 1 means strict spacing.
	Burst int
	// Jitter, if > 0, adds a random pad after each request — a uniform
	// multiple of jitterStep in [jitterStep, Jitter] — so request timing
	// is not perfectly periodic. Zero means no pad (e.g. Gallica, whose
	// fixed 13s spacing is deliberate).
	Jitter time.Duration
}

// RatePolicy maps hosts to politeness policies, with a fallback Default.
type RatePolicy struct {
	Default HostPolicy
	ByHost  map[string]HostPolicy // keyed by URL host, e.g. "gallica.bnf.fr"
}

// For returns the policy for host, falling back to Default.
func (rp RatePolicy) For(host string) HostPolicy {
	if p, ok := rp.ByHost[host]; ok {
		return p
	}
	return rp.Default
}

// DefaultRatePolicy is the built-in politeness baseline: a gentle default for
// any host, plus a much slower rate for Gallica/BnF, which is known to be
// strongly rate-sensitive (the reference tool spaces Gallica requests by
// ~12s; we use 13s). Overridable via WithRatePolicy.
func DefaultRatePolicy() RatePolicy {
	return RatePolicy{
		Default: HostPolicy{
			MinInterval: 750 * time.Millisecond,
			Burst:       1,
			Jitter:      600 * time.Millisecond,
		},
		ByHost: map[string]HostPolicy{
			// Gallica/BnF is strongly rate-sensitive; the fixed 13s
			// spacing is deliberate, so no jitter here.
			"gallica.bnf.fr": {MinInterval: 13 * time.Second, Burst: 1},
		},
	}
}

// jitterPad returns a uniform random multiple of jitterStep in
// [jitterStep, maxPad]. maxPad is assumed a positive multiple of jitterStep.
func jitterPad(maxPad time.Duration) time.Duration {
	steps := max(int(maxPad/jitterStep), 1)
	//nolint:gosec // G404: jitter is politeness timing, not security-sensitive
	return jitterStep * time.Duration(1+rand.IntN(steps))
}

// jitterLimiter wraps a base limiter and adds a random pad after the base
// wait, so request timing to a host is not perfectly periodic.
type jitterLimiter struct {
	base   RateLimiter
	maxPad time.Duration
}

func (j *jitterLimiter) Wait(ctx context.Context) error {
	if err := j.base.Wait(ctx); err != nil {
		return err
	}
	t := time.NewTimer(jitterPad(j.maxPad))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// newRateLimiter builds the production RateLimiter for a HostPolicy.
func newRateLimiter(p HostPolicy) RateLimiter {
	burst := max(p.Burst, 1)
	var base RateLimiter
	if p.MinInterval <= 0 {
		base = rate.NewLimiter(rate.Inf, burst)
	} else {
		base = rate.NewLimiter(rate.Every(p.MinInterval), burst)
	}
	if p.Jitter > 0 {
		return &jitterLimiter{base: base, maxPad: p.Jitter}
	}
	return base
}
