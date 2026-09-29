// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
)

// Schedule sets a scale set's min_runners by time of day, so that runners are
// kept ready in working hours and none at night.
//
//	schedule:
//	  timezone: Europe/Vilnius
//	  windows:
//	    - days: [mon-fri]
//	      from: "08:00"
//	      to: "19:00"
//	      min_runners: 4
//
// The zero Schedule has no windows.
type Schedule struct {
	// TimeZone is the IANA time zone the windows are in, such as
	// Europe/Vilnius; a window keeps to the wall clock there across
	// daylight saving. Unset is UTC.
	TimeZone TimeZone `yaml:"timezone,omitempty"`

	// Windows are the times min_runners differs from the scale set's own.
	// The first window, in the order written, that the time falls in is in
	// force; outside every window, the scale set's min_runners is.
	Windows []ScheduleWindow `yaml:"windows,omitempty"`
}

// WindowAt returns the index in Windows of the window in force at t, the
// first containing it, and false if none does.
func (s Schedule) WindowAt(t time.Time) (int, bool) {
	t = t.In(s.TimeZone.Location())
	for i, w := range s.Windows {
		if w.Contains(t) {
			return i, true
		}
	}

	return 0, false
}

// validate reports whether the schedule can be run by a scale set of at most
// maxRunners runners.
func (s Schedule) validate(maxRunners int) error {
	if len(s.Windows) == 0 && !s.TimeZone.IsZero() {
		return errdefs.InvalidArgument("schedule: timezone is set but there are no windows")
	}

	for i, w := range s.Windows {
		if err := w.validate(maxRunners); err != nil {
			return fmt.Errorf("schedule: window %d: %w", i+1, err)
		}
	}

	return nil
}

// ScheduleWindow is a time of the week when a scale set keeps a number of
// runners ready.
type ScheduleWindow struct {
	// Days are the days the window opens on: mon, tue, wed, thu, fri, sat
	// and sun, or ranges of them such as mon-fri. It is required.
	Days Weekdays `yaml:"days"`

	// From is when the window opens, as HH:MM. It is required.
	From TimeOfDay `yaml:"from"`

	// To is when the window closes, as HH:MM, or 24:00 for the end of the
	// day. One earlier than from closes the next day: a window from 22:00
	// to 06:00 on fri runs into Saturday morning. It is required.
	To TimeOfDay `yaml:"to"`

	// MinRunners are kept idle and ready while the window is in force, in
	// place of the scale set's min_runners. Unset is 0; it may not be more
	// than max_runners.
	MinRunners int `yaml:"min_runners"`
}

// Contains reports whether t, on the clock of the schedule's time zone, falls
// in the window. A window past midnight belongs to the day it opens on.
func (w ScheduleWindow) Contains(t time.Time) bool {
	now := TimeOfDay(t.Hour()*60 + t.Minute())
	day := t.Weekday()

	if w.From < w.To {
		return w.Days.Has(day) && w.From <= now && now < w.To
	}

	yesterday := (day + 6) % 7

	return w.Days.Has(day) && now >= w.From || w.Days.Has(yesterday) && now < w.To
}

// String formats the window as "mon-fri 08:00-19:00".
func (w ScheduleWindow) String() string {
	return fmt.Sprintf("%s %s-%s", w.Days, w.From, w.To)
}

// validate reports whether the window can be run by a scale set of at most
// maxRunners runners.
func (w ScheduleWindow) validate(maxRunners int) error {
	switch {
	case w.Days == 0:
		return errdefs.InvalidArgument("days is required: the days it opens on, such as [mon-fri]")
	case w.From == endOfDay:
		return errdefs.InvalidArgument("from is 24:00: a window opens by 23:59")
	case w.From == w.To:
		return errdefs.InvalidArgument("from and to are both %s: a window needs a length", w.From)
	case w.MinRunners < 0:
		return errdefs.InvalidArgument("min_runners cannot be negative")
	case w.MinRunners > maxRunners:
		return errdefs.InvalidArgument("min_runners (%d) is more than max_runners (%d)", w.MinRunners, maxRunners)
	}

	return nil
}

// TimeZone is an IANA time zone. The zero TimeZone is UTC.
type TimeZone struct {
	location *time.Location
}

// ParseTimeZone returns the IANA time zone of that name, such as
// Europe/Vilnius.
func ParseTimeZone(name string) (TimeZone, error) {
	// Local is whatever the daemon's host has, which a schedule should
	// not depend on.
	if name == "" || name == "Local" {
		return TimeZone{}, errdefs.InvalidArgument("invalid time zone %q: want an IANA name, such as "+
			"Europe/Vilnius or UTC", name)
	}

	location, err := time.LoadLocation(name)
	if err != nil {
		return TimeZone{}, errdefs.InvalidArgument("unknown time zone %q: want an IANA name, such as "+
			"Europe/Vilnius or UTC", name)
	}

	return TimeZone{location: location}, nil
}

// Location returns the time zone's location, UTC for the zero TimeZone.
func (z TimeZone) Location() *time.Location {
	if z.location == nil {
		return time.UTC
	}

	return z.location
}

// IsZero reports whether the time zone is unset.
func (z TimeZone) IsZero() bool {
	return z.location == nil
}

// String returns the time zone's name: "Europe/Vilnius".
func (z TimeZone) String() string {
	return z.Location().String()
}

// UnmarshalYAML reads a time zone by its IANA name.
func (z *TimeZone) UnmarshalYAML(node *yaml.Node) error {
	var name string
	if err := node.Decode(&name); err != nil {
		return err
	}

	parsed, err := ParseTimeZone(name)
	if err != nil {
		return err
	}
	*z = parsed

	return nil
}

// MarshalYAML writes the time zone by its IANA name.
func (z TimeZone) MarshalYAML() (any, error) {
	return z.String(), nil
}

// Weekdays is a set of days of the week.
type Weekdays uint8

// dayNames are the days as a configuration writes them, by time.Weekday.
var dayNames = [7]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// week is the days in the order they are written: Monday first.
var week = [7]time.Weekday{
	time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday,
}

// ParseWeekdays returns the days of items, each a day such as mon or a range
// such as mon-fri. A range may wrap past the week's end: fri-mon.
func ParseWeekdays(items ...string) (Weekdays, error) {
	var days Weekdays

	for _, item := range items {
		first, last, isRange := strings.Cut(item, "-")

		from, err := parseDay(first)
		if err != nil {
			return 0, err
		}
		to := from
		if isRange {
			if to, err = parseDay(last); err != nil {
				return 0, err
			}
		}

		for day := from; ; day = (day + 1) % 7 {
			days |= 1 << day
			if day == to {
				break
			}
		}
	}

	return days, nil
}

// parseDay returns the day of name, such as mon.
func parseDay(name string) (time.Weekday, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	for day, dayName := range dayNames {
		if name == dayName {
			return time.Weekday(day), nil
		}
	}

	return 0, errdefs.InvalidArgument("unknown day %q: want mon, tue, wed, thu, fri, sat or sun, "+
		"or a range such as mon-fri", name)
}

// Has reports whether day is one of the days.
func (d Weekdays) Has(day time.Weekday) bool {
	return d&(1<<day) != 0
}

// String formats the days as a configuration writes them, Monday first and
// runs of days as ranges: "mon-fri,sun".
func (d Weekdays) String() string {
	return strings.Join(d.ranges(), ",")
}

// ranges returns the days as runs, Monday first: ["mon-fri", "sun"].
func (d Weekdays) ranges() []string {
	var out []string

	for i := 0; i < len(week); i++ {
		if !d.Has(week[i]) {
			continue
		}

		start := i
		for i+1 < len(week) && d.Has(week[i+1]) {
			i++
		}

		if start == i {
			out = append(out, dayNames[week[i]])
		} else {
			out = append(out, dayNames[week[start]]+"-"+dayNames[week[i]])
		}
	}

	return out
}

// UnmarshalYAML reads a list of days and ranges, or a single one.
func (d *Weekdays) UnmarshalYAML(node *yaml.Node) error {
	var items []string
	if node.Kind == yaml.ScalarNode {
		items = []string{node.Value}
	} else if err := node.Decode(&items); err != nil {
		return errdefs.InvalidArgument("invalid days: want a list of days, such as [mon-fri, sun]")
	}

	days, err := ParseWeekdays(items...)
	if err != nil {
		return err
	}
	*d = days

	return nil
}

// MarshalYAML writes the days as a list of days and ranges.
func (d Weekdays) MarshalYAML() (any, error) {
	return d.ranges(), nil
}

// TimeOfDay is a time on the clock, in minutes since midnight, from 00:00 to
// 24:00.
type TimeOfDay int

// endOfDay is 24:00, the time of day a window may close at, but not open at.
const endOfDay TimeOfDay = 24 * 60

// ParseTimeOfDay returns the time of day s writes as HH:MM, from 00:00 to
// 24:00.
func ParseTimeOfDay(s string) (TimeOfDay, error) {
	invalid := errdefs.InvalidArgument("invalid time of day %q: want HH:MM, such as 08:00 or 24:00", s)

	hours, minutes, ok := strings.Cut(s, ":")
	if !ok || len(hours) != 2 || len(minutes) != 2 {
		return 0, invalid
	}

	h, err := strconv.Atoi(hours)
	if err != nil || h < 0 || h > 24 {
		return 0, invalid
	}
	m, err := strconv.Atoi(minutes)
	if err != nil || m < 0 || m > 59 {
		return 0, invalid
	}

	t := TimeOfDay(h*60 + m)
	if t > endOfDay {
		return 0, invalid
	}

	return t, nil
}

// String formats the time of day as HH:MM: "08:00".
func (t TimeOfDay) String() string {
	return fmt.Sprintf("%02d:%02d", t/60, t%60)
}

// UnmarshalYAML reads a time of day written as HH:MM.
func (t *TimeOfDay) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return errdefs.InvalidArgument("invalid time of day: want HH:MM, such as 08:00")
	}

	parsed, err := ParseTimeOfDay(node.Value)
	if err != nil {
		return err
	}
	*t = parsed

	return nil
}

// MarshalYAML writes the time of day as HH:MM.
func (t TimeOfDay) MarshalYAML() (any, error) {
	return t.String(), nil
}
