package cli

import (
	"testing"
	"time"
)

func TestParseTime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 9, 15, 0, 0, 0, time.UTC)

	cases := []struct {
		input   string
		want    time.Time
		wantErr bool
	}{
		// RFC3339
		{"2026-07-09T14:00:00Z", time.Date(2026, 7, 9, 14, 0, 0, 0, time.UTC), false},
		// Relative
		{"-45m", now.Add(-45 * time.Minute), false},
		{"-2h", now.Add(-2 * time.Hour), false},
		{"-0m", now, false},
		// Bad
		{"not-a-time", time.Time{}, true},
		{"", time.Time{}, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got, err := ParseTime(tc.input, now)
			if tc.wantErr {
				if err == nil {
					t.Errorf("ParseTime(%q) expected error, got %v", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTime(%q) unexpected error: %v", tc.input, err)
			}
			if !got.Equal(tc.want) {
				t.Errorf("ParseTime(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
