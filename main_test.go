package main

import (
	"strings"
	"testing"
	"time"
)

func TestFormatMetricsLineDoesNotIncludeSessionTime(t *testing.T) {
	input := Input{
		ContextWindow: &ContextWindow{UsedPercentage: 92},
		RateLimits: &RateLimits{
			FiveHour: &RateLimit{UsedPercentage: 87},
			SevenDay: &RateLimit{UsedPercentage: 84},
		},
	}

	got := formatMetricsLine(input)
	if strings.Contains(got, "1h2m") {
		t.Fatalf("formatMetricsLine() = %q, must not include session time", got)
	}
	if !strings.Contains(got, "5h:") || !strings.Contains(got, "7d:") {
		t.Fatalf("formatMetricsLine() = %q, want rate limit metrics", got)
	}
}

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
