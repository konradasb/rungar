// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// scheduledScaleSet returns a valid scale set of min_runners 0 with the
// schedule the YAML writes.
func scheduledScaleSet(t *testing.T, schedule string) types.ScaleSetSpec {
	t.Helper()

	set := validScaleSet(nil)
	if err := yaml.Unmarshal([]byte(schedule), &set.Schedule); err != nil {
		t.Fatalf("decode schedule: %v", err)
	}
	if err := set.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}

	return set
}

// at returns the time of the layout "2006-01-02 15:04" in the named zone.
func at(t *testing.T, zone, s string) time.Time {
	t.Helper()

	location, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	when, err := time.ParseInLocation("2006-01-02 15:04", s, location)
	if err != nil {
		t.Fatal(err)
	}

	return when
}

func TestMinRunnersFollowTheScheduleWindows(t *testing.T) {
	// 2026-10-05 is a Monday, and 2026-10-11 a Sunday.
	set := scheduledScaleSet(t, `
timezone: Europe/Vilnius
windows:
  - days: fri
    from: "12:00"
    to: "19:00"
    min_runners: 1
  - days: [mon-fri]
    from: "08:00"
    to: "19:00"
    min_runners: 4
  - days: sun
    from: "22:00"
    to: "02:00"
    min_runners: 2
  - days: sat
    from: "20:00"
    to: "24:00"
    min_runners: 3
`)

	tests := []struct {
		name string
		at   time.Time
		want int
	}{
		{name: "a weekday morning", at: at(t, "Europe/Vilnius", "2026-10-05 08:00"), want: 4},
		{name: "a minute before opening", at: at(t, "Europe/Vilnius", "2026-10-05 07:59"), want: 0},
		{name: "closing is outside the window", at: at(t, "Europe/Vilnius", "2026-10-05 19:00"), want: 0},
		{name: "the first matching window wins", at: at(t, "Europe/Vilnius", "2026-10-09 13:00"), want: 1},
		{name: "a later window where the first does not match", at: at(t, "Europe/Vilnius", "2026-10-09 09:00"), want: 4},
		{name: "a weekend day", at: at(t, "Europe/Vilnius", "2026-10-10 10:00"), want: 0},
		{name: "a window past midnight, before it", at: at(t, "Europe/Vilnius", "2026-10-11 23:00"), want: 2},
		{name: "a window past midnight, after it", at: at(t, "Europe/Vilnius", "2026-10-12 01:59"), want: 2},
		{name: "a window past midnight, closed", at: at(t, "Europe/Vilnius", "2026-10-12 02:00"), want: 0},
		{name: "a window past midnight, the day before it opens", at: at(t, "Europe/Vilnius", "2026-10-11 01:00"), want: 0},
		{name: "a window to the end of the day", at: at(t, "Europe/Vilnius", "2026-10-10 23:59"), want: 3},
		{name: "a time in another zone", at: at(t, "UTC", "2026-10-05 05:00"), want: 4},
		{name: "a time in another zone, outside", at: at(t, "UTC", "2026-10-05 04:59"), want: 0},
		// Clocks go back an hour on 2026-10-25 in Vilnius: 08:00 local is
		// 05:00 UTC before and 06:00 UTC after.
		{name: "wall clock after daylight saving ends", at: at(t, "UTC", "2026-10-26 06:00"), want: 4},
		{name: "wall clock after daylight saving ends, outside", at: at(t, "UTC", "2026-10-26 05:00"), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := set.MinRunnersAt(tt.at); got != tt.want {
				t.Errorf("MinRunnersAt(%s) = %d, want %d", tt.at, got, tt.want)
			}
		})
	}
}

func TestScheduleWithoutTimeZoneIsUTC(t *testing.T) {
	set := scheduledScaleSet(t, `
windows:
  - {days: mon, from: "08:00", to: "09:00", min_runners: 2}
`)

	if got := set.MinRunnersAt(at(t, "UTC", "2026-10-05 08:30")); got != 2 {
		t.Errorf("MinRunnersAt(08:30 UTC) = %d, want 2", got)
	}
	if got := set.MinRunnersAt(at(t, "Europe/Vilnius", "2026-10-05 08:30")); got != 0 {
		t.Errorf("MinRunnersAt(08:30 in Vilnius, 05:30 UTC) = %d, want 0", got)
	}
}

func TestMinRunnersAtWithoutScheduleIsTheScaleSets(t *testing.T) {
	set := validScaleSet(func(s *types.ScaleSetSpec) { s.MinRunners = 2 })

	if got := set.MinRunnersAt(time.Now()); got != 2 {
		t.Errorf("MinRunnersAt() = %d, want the scale set's 2", got)
	}
}

func TestScheduleIsInvalid(t *testing.T) {
	tests := []struct {
		name     string
		schedule string
	}{
		{name: "no days", schedule: `windows: [{from: "08:00", to: "09:00", min_runners: 1}]`},
		{name: "a window of no length", schedule: `windows: [{days: mon, from: "08:00", to: "08:00"}]`},
		{name: "opening at 24:00", schedule: `windows: [{days: mon, from: "24:00", to: "08:00"}]`},
		{name: "a negative min_runners", schedule: `windows: [{days: mon, from: "08:00", to: "09:00", min_runners: -1}]`},
		{name: "more than max_runners", schedule: `windows: [{days: mon, from: "08:00", to: "09:00", min_runners: 11}]`},
		{name: "a time zone without windows", schedule: `timezone: Europe/Vilnius`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := validScaleSet(nil)
			if err := yaml.Unmarshal([]byte(tt.schedule), &set.Schedule); err != nil {
				t.Fatalf("decode schedule: %v", err)
			}

			err := set.Validate()
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Fatalf("Validate() = %v, want an invalid argument error", err)
			}
			if !strings.Contains(err.Error(), "schedule") {
				t.Errorf("Validate() = %q, want it to name the schedule", err)
			}
		})
	}
}

func TestScheduleDoesNotDecode(t *testing.T) {
	tests := []struct {
		name     string
		schedule string
	}{
		{name: "an unknown time zone", schedule: `timezone: Mars/Olympus`},
		{name: "the host's time zone", schedule: `timezone: Local`},
		{name: "an unknown day", schedule: `windows: [{days: monday, from: "08:00", to: "09:00"}]`},
		{name: "a range of an unknown day", schedule: `windows: [{days: mon-fry, from: "08:00", to: "09:00"}]`},
		{name: "a time without minutes", schedule: `windows: [{days: mon, from: "8", to: "09:00"}]`},
		{name: "a time of one digit hours", schedule: `windows: [{days: mon, from: "8:00", to: "09:00"}]`},
		{name: "an hour past the day", schedule: `windows: [{days: mon, from: "25:00", to: "09:00"}]`},
		{name: "past 24:00", schedule: `windows: [{days: mon, from: "08:00", to: "24:01"}]`},
		{name: "a minute past the hour", schedule: `windows: [{days: mon, from: "08:60", to: "09:00"}]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var schedule types.Schedule
			if err := yaml.Unmarshal([]byte(tt.schedule), &schedule); !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("decode = %v, want an invalid argument error", err)
			}
		})
	}
}

func TestWeekdaysAreNormalisedAndMerged(t *testing.T) {
	tests := []struct {
		items []string
		want  string
	}{
		{items: []string{"mon"}, want: "mon"},
		{items: []string{"mon-fri"}, want: "mon-fri"},
		{items: []string{"Mon", "WED"}, want: "mon,wed"},
		{items: []string{"sat-sun", "mon"}, want: "mon,sat-sun"},
		{items: []string{"fri-mon"}, want: "mon,fri-sun"},
		{items: []string{"mon-sun"}, want: "mon-sun"},
		{items: []string{"sun-sat"}, want: "mon-sun"},
		{items: []string{"mon-fri", "wed"}, want: "mon-fri"},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.items, ","), func(t *testing.T) {
			days, err := types.ParseWeekdays(tt.items...)
			if err != nil {
				t.Fatal(err)
			}
			if got := days.String(); got != tt.want {
				t.Errorf("ParseWeekdays(%q) = %s, want %s", tt.items, got, tt.want)
			}
		})
	}
}

// TestScheduleRoundTrips checks a schedule is written as it is read, as
// rungar config print shows it.
func TestScheduleRoundTrips(t *testing.T) {
	const in = `timezone: Europe/Vilnius
windows:
    - days:
        - mon-fri
        - sun
      from: "08:00"
      to: "24:00"
      min_runners: 4
`

	var schedule types.Schedule
	if err := yaml.Unmarshal([]byte(in), &schedule); err != nil {
		t.Fatal(err)
	}

	out, err := yaml.Marshal(schedule)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != in {
		t.Errorf("marshalled as\n%s\nwant\n%s", out, in)
	}
}

func TestZeroScheduleIsOmitted(t *testing.T) {
	out, err := yaml.Marshal(validScaleSet(nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "schedule") {
		t.Errorf("marshalled as\n%s\nwant no schedule", out)
	}
}
