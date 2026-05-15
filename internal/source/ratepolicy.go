package source

import (
	"time"

	"golang.org/x/time/rate"
)

// HostPolicy is the politeness contract for one host.
type HostPolicy struct {
	// MinInterval is the minimum spacing between requests to the host.
	MinInterval time.Duration
	// Burst is the token-bucket burst; 1 means strict spacing.
	Burst int
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
		Default: HostPolicy{MinInterval: time.Second, Burst: 1},
		ByHost: map[string]HostPolicy{
			"gallica.bnf.fr": {MinInterval: 13 * time.Second, Burst: 1},
		},
	}
}

// newRateLimiter builds the production RateLimiter for a HostPolicy.
func newRateLimiter(p HostPolicy) RateLimiter {
	burst := max(p.Burst, 1)
	if p.MinInterval <= 0 {
		return rate.NewLimiter(rate.Inf, burst)
	}
	return rate.NewLimiter(rate.Every(p.MinInterval), burst)
}
