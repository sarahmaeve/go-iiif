package source

import "testing"

// FuzzParseRetryAfter asserts the untrusted Retry-After header parser never
// panics, is deterministic, and never yields a negative duration (a negative
// backoff would break the polite scheduler).
func FuzzParseRetryAfter(f *testing.F) {
	for _, s := range []string{
		"", "0", "1", "120", "-1", "  5  ", "5.5", "abc",
		"Wed, 21 Oct 2015 07:28:00 GMT", "99999999999999999999", "+3",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, v string) {
		d := parseRetryAfter(v)
		if d < 0 {
			t.Fatalf("parseRetryAfter(%q) = %v: negative duration", v, d)
		}
		if d2 := parseRetryAfter(v); d2 != d {
			t.Fatalf("parseRetryAfter(%q) not deterministic: %v vs %v", v, d, d2)
		}
	})
}

// FuzzLooksLikeHTML asserts the bot-wall heuristic never panics on arbitrary
// bodies — including empty and nil, where the bounded slice expression must
// stay in range — and is deterministic.
func FuzzLooksLikeHTML(f *testing.F) {
	for _, s := range [][]byte{
		nil, {}, []byte(" "), []byte("<!DOCTYPE html>"),
		[]byte("<html>"), []byte("  <HTML lang=en>"), []byte("{json:true}"),
		[]byte("\x00\x01\x02"), make([]byte, 1024),
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		got := looksLikeHTML(body)
		if got2 := looksLikeHTML(body); got != got2 {
			t.Fatalf("looksLikeHTML not deterministic for %d bytes", len(body))
		}
	})
}
