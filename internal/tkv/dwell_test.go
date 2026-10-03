package tkv

import (
	"testing"
	"time"
)

func TestStatusDwell(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.FixedZone("AEST", 10*3600))
	stamp := func(d time.Duration) string {
		return now.Add(d).Format(time.RFC3339)
	}
	z90m := now.Add(-90 * time.Minute).UTC().Format(time.RFC3339)

	tests := []struct {
		name  string
		stamp string
		want  string
		ok    bool
	}{
		{name: "absent", stamp: "", ok: false},
		{name: "unparseable", stamp: "not-a-time", ok: false},
		{name: "future", stamp: stamp(time.Hour), ok: false},
		{name: "now", stamp: stamp(0), want: "<1m", ok: true},
		{name: "thirty seconds", stamp: stamp(-30 * time.Second), want: "<1m", ok: true},
		{name: "one minute", stamp: stamp(-time.Minute), want: "1m", ok: true},
		{name: "fifty nine minutes", stamp: stamp(-59*time.Minute - 59*time.Second), want: "59m", ok: true},
		{name: "one hour", stamp: stamp(-time.Hour), want: "1h", ok: true},
		{name: "ninety minutes as Z", stamp: z90m, want: "1h", ok: true},
		{name: "twenty three hours", stamp: stamp(-23*time.Hour - 59*time.Minute), want: "23h", ok: true},
		{name: "one day", stamp: stamp(-24 * time.Hour), want: "1d", ok: true},
		{name: "thirty six days", stamp: stamp(-36 * 24 * time.Hour), want: "36d", ok: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := statusDwell(tc.stamp, now)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("statusDwell(%q) = %q, %v; want %q, %v", tc.stamp, got, ok, tc.want, tc.ok)
			}
		})
	}
}
