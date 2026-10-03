package tkv

import (
	"fmt"
	"time"
)

// statusDwell labels how long stamp is before now. An absent, unparseable,
// or future stamp is not a duration, so the card shows nothing.
func statusDwell(stamp string, now time.Time) (string, bool) {
	if stamp == "" {
		return "", false
	}
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return "", false
	}
	d := now.Sub(at)
	if d < 0 {
		return "", false
	}
	switch {
	case d < time.Minute:
		return "<1m", true
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute)), true
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour)), true
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour))), true
	}
}
