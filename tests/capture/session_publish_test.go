package capture

import (
	"testing"
	"time"
)

// The publish interval is the only UI cadence control in a session: zero selects the default, the rate is capped,
// and a change made while the session exists is what the next publish decision reads.
func TestSessionPublishIntervalNormalizesAndChangesAtRuntime(t *testing.T) {
	for _, tc := range []struct{ in, want time.Duration }{
		{0, DefaultPublishInterval},
		{time.Millisecond, MinPublishInterval},
		{250 * time.Millisecond, 250 * time.Millisecond},
		{time.Second, time.Second},
	} {
		if got := normalizePublishInterval(tc.in); got != tc.want {
			t.Fatalf("normalizePublishInterval(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
	session := &Session{}
	for _, interval := range []time.Duration{time.Second, 500 * time.Millisecond, 0, 750 * time.Millisecond} {
		session.SetPublishInterval(interval)
		if got := time.Duration(session.publishInterval.Load()); got != normalizePublishInterval(interval) {
			t.Fatalf("after SetPublishInterval(%v) the session reads %v", interval, got)
		}
	}
}
