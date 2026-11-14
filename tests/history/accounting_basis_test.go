package history

import (
	"database/sql"
	"testing"
)

// The analyser identity a span stored decides whether its encounters include server-executed damage
// (capture/damage_notify.go): official v0.7.0 builds up to 36 counted damage responses only.
func TestNotifiedDamageCountedFollowsTheRecordingBuild(t *testing.T) {
	version := func(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }
	build := func(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: true} }
	cases := []struct {
		name    string
		version sql.NullString
		build   sql.NullInt64
		want    bool
	}{
		{"no product version stored", sql.NullString{}, build(31), false},
		{"an earlier product version", version("0.6.0"), build(20), false},
		{"the owner's Build 31", version("0.7.0"), build(31), false},
		{"the last build without it", version("0.7.0"), build(lastBuildWithoutNotifiedDamage), false},
		{"the first build with it", version("0.7.0"), build(lastBuildWithoutNotifiedDamage + 1), true},
		{"an unofficial build of the current source", version("0.7.0"), sql.NullInt64{}, true},
		{"a later product version", version("0.7.1"), build(1), true},
		{"a later minor version", version("0.10.0"), sql.NullInt64{}, true},
		{"an unparsable version", version("x.y"), build(40), false},
	}
	for _, c := range cases {
		if got := notifiedDamageCounted(c.version, c.build); got != c.want {
			t.Errorf("%s: notifiedDamageCounted(%v, %v) = %v, want %v", c.name, c.version, c.build, got, c.want)
		}
	}
}

func TestCompareProductVersionOrdersNumerically(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"0.7.0", "0.7.0", 0}, {"0.6.9", "0.7.0", -1}, {"0.10.0", "0.9.9", 1}, {"1.0.0", "0.99.99", 1}, {"bad", "0.7.0", -1}} {
		if got := compareProductVersion(c.a, c.b); got != c.want {
			t.Errorf("compareProductVersion(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
