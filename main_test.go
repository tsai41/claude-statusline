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
		name     string
		email    string
		toggle   string
		want     string
		noFile   bool
		inConfig bool
	}{
		{name: "truncates to three chars", email: "orli.zhao@stranity.com", want: "orl"},
		{name: "keeps short local part", email: "ab@x.com", want: "ab"},
		{name: "hidden by toggle", email: "orli.zhao@stranity.com", toggle: "0", want: ""},
		{name: "hidden by toggle word", email: "orli.zhao@stranity.com", toggle: "OFF", want: ""},
		{name: "no at sign", email: "orli.zhao", want: ""},
		{name: "missing config file", noFile: true, want: ""},
		// The whole point of the config dir: which account this session
		// runs as is the dir it was started with, not whoever the default
		// home last logged in as.
		{name: "reads the config dir this session runs in", email: "illie.cheng@stranity.com", inConfig: true, want: "ill"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("STATUSLINE_ACCOUNT", tc.toggle)
			configDir := filepath.Join(home, ".claude-2")
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			if tc.inConfig {
				t.Setenv("CLAUDE_CONFIG_DIR", configDir)
			}
			if !tc.noFile {
				body := fmt.Sprintf(`{"oauthAccount":{"emailAddress":%q},"projects":{}}`, tc.email)
				target := filepath.Join(home, ".claude.json")
				if tc.inConfig {
					target = filepath.Join(configDir, ".claude.json")
				}
				if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if got := readAccountTag(); got != tc.want {
				t.Fatalf("readAccountTag() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The ccs registry names the account; the email is only the fallback for a
// home it has never heard of.
func TestReadAccountTagPrefersTheCcsRegistryName(t *testing.T) {
	cases := []struct {
		name  string
		alias string
		want  string
	}{
		{name: "numbered and aliased", alias: "orli", want: "#4 orli"},
		{name: "numbered when the alias is empty", alias: "", want: "#4"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("STATUSLINE_ACCOUNT", "")
			configDir := filepath.Join(home, ".claude-4")
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CLAUDE_CONFIG_DIR", configDir)
			writeAccountConfig(t, filepath.Join(configDir, ".claude.json"), "orli.zhao@stranity.com")
			writeCcsRegistry(t, home, configDir, tc.alias)

			if got := readAccountTag(); got != tc.want {
				t.Fatalf("readAccountTag() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A registry that lists other accounts but not this home must not label
// the session with one of them.
func TestReadAccountTagFallsBackToTheEmailForAnUnregisteredHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("STATUSLINE_ACCOUNT", "")
	configDir := filepath.Join(home, ".claude-9")
	elsewhere := filepath.Join(home, ".claude-4")
	for _, dir := range []string{configDir, elsewhere} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	writeAccountConfig(t, filepath.Join(configDir, ".claude.json"), "orli.zhao@stranity.com")
	writeCcsRegistry(t, home, elsewhere, "illie")

	if got := readAccountTag(); got != "orl" {
		t.Fatalf("readAccountTag() = %q, want %q", got, "orl")
	}
}

// STATUSLINE_ACCOUNT_REGISTRY lets the program read someone else's roster
// of accounts instead of the ccs default.
func TestReadAccountTagHonorsRegistryOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("STATUSLINE_ACCOUNT", "")
	configDir := filepath.Join(home, ".claude-4")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	writeAccountConfig(t, filepath.Join(configDir, ".claude.json"), "orli.zhao@stranity.com")

	registryDir := t.TempDir()
	registryPath := filepath.Join(registryDir, "roster.json")
	body := fmt.Sprintf(`{"version":1,"accounts":[{"id":"claude-4","provider":"claude","number":4,"home":%q,"alias":"orli"}]}`, configDir)
	if err := os.WriteFile(registryPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STATUSLINE_ACCOUNT_REGISTRY", registryPath)

	if got := readAccountTag(); got != "#4 orli" {
		t.Fatalf("readAccountTag() = %q, want %q", got, "#4 orli")
	}
}

func writeAccountConfig(t *testing.T, path, email string) {
	t.Helper()
	body := fmt.Sprintf(`{"oauthAccount":{"emailAddress":%q},"projects":{}}`, email)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeCcsRegistry(t *testing.T, home, accountHome, alias string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "agent-conversation-handoff")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The codex entry is listed first and shares the home on purpose: a
	// tag that ignored the provider would name that one.
	body := fmt.Sprintf(`{"version":1,"accounts":[
		{"id":"codex-1","provider":"codex","number":1,"home":%q,"alias":"codex"},
		{"id":"claude-4","provider":"claude","number":4,"home":%q,"alias":%q}
	]}`, accountHome, accountHome, alias)
	if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
