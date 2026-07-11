package cli

import (
	"fmt"
	"time"
)

// ParseTime parses a flexible time string into a time.Time.
// Accepted formats (in order of precedence):
//   - RFC3339:         "2026-07-09T14:00:00Z"
//   - Short RFC3339:   "2026-07-09T14:00:00"   (local timezone assumed)
//   - HH:MM:SS:        "14:00:00"              (today, local timezone)
//   - HH:MM:           "14:00"                 (today, local timezone)
//   - Relative:        "-45m", "-2h", "-1h30m" (relative to now)
//
// The relative format is the most common in incident investigation:
// `rewind investigate --from -2h --to -0m`
func ParseTime(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time string")
	}

	// Relative duration: starts with '-' followed by a duration string.
	if len(s) > 1 && s[0] == '-' {
		d, err := time.ParseDuration(s[1:])
		if err == nil {
			return now.Add(-d), nil
		}
	}

	// RFC3339 with timezone.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	// RFC3339 without timezone — assume local.
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", s, time.Local); err == nil {
		return t, nil
	}

	// Date only.
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}

	// HH:MM:SS today.
	today := now.Format("2006-01-02")
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", today+" "+s, time.Local); err == nil {
		return t, nil
	}

	// HH:MM today.
	if t, err := time.ParseInLocation("2006-01-02 15:04", today+" "+s, time.Local); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("unrecognised time format %q (accepted: RFC3339, HH:MM, HH:MM:SS, -45m)", s)
}
