package main

import (
	"testing"
	"time"
)

func TestFormatResetDurationUsesDaysAndHoursForLongDurations(t *testing.T) {
	got := formatResetDuration(34 * time.Hour)
	if got != "1d10h" {
		t.Fatalf("formatResetDuration(34h) = %q, want %q", got, "1d10h")
	}
}

func TestFormatResetDurationKeepsHoursAndMinutesForDurationsUnderADay(t *testing.T) {
	got := formatResetDuration(19*time.Hour + 50*time.Minute)
	if got != "19h50m" {
		t.Fatalf("formatResetDuration(19h50m) = %q, want %q", got, "19h50m")
	}
}
