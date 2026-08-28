package main

import (
	"fmt"
	"os"
	"path/filepath"
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

	got := formatMetricsLine(input, "")
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

func TestReadAccountTag(t *testing.T) {
	cases := []struct {
		name   string
		email  string
		toggle string
		want   string
		noFile bool
	}{
		{name: "truncates to three chars", email: "orli.zhao@stranity.com", want: "orl"},
		{name: "keeps short local part", email: "ab@x.com", want: "ab"},
		{name: "hidden by toggle", email: "orli.zhao@stranity.com", toggle: "0", want: ""},
		{name: "hidden by toggle word", email: "orli.zhao@stranity.com", toggle: "OFF", want: ""},
		{name: "no at sign", email: "orli.zhao", want: ""},
		{name: "missing config file", noFile: true, want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("STATUSLINE_ACCOUNT", tc.toggle)
			if !tc.noFile {
				body := fmt.Sprintf(`{"oauthAccount":{"emailAddress":%q},"projects":{}}`, tc.email)
				if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if got := readAccountTag(); got != tc.want {
				t.Fatalf("readAccountTag() = %q, want %q", got, tc.want)
			}
		})
	}
}
