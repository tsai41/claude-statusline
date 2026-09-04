package main

import (
	"bytes"
	"encoding/json"
	"errors"
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

func TestNormalizeResetsAt(t *testing.T) {
	tests := []struct {
		name string
		in   interface{}
		want string
		ok   bool
	}{
		{name: "unix timestamp", in: float64(1767513600), want: "2026-01-04T08:00:00Z", ok: true},
		{name: "rfc3339 string already utc", in: "2026-09-05T02:00:00Z", want: "2026-09-05T02:00:00Z", ok: true},
		{name: "rfc3339 with offset normalizes to utc", in: "2026-09-05T10:00:00+08:00", want: "2026-09-05T02:00:00Z", ok: true},
		{name: "unparseable string", in: "not-a-time", want: "", ok: false},
		{name: "unexpected type", in: true, want: "", ok: false},
		{name: "nil", in: nil, want: "", ok: false},
		{name: "unix timestamp given in milliseconds is implausible", in: float64(1767513600000), want: "", ok: false},
		{name: "unix timestamp far in the past is implausible", in: float64(0), want: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := normalizeResetsAt(tt.in)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("normalizeResetsAt(%v) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestBuildUsageSnapshotOmitsMissingWindow(t *testing.T) {
	now := time.Date(2026, 9, 4, 7, 12, 33, 0, time.UTC)
	rateLimits := &RateLimits{FiveHour: &RateLimit{UsedPercentage: 55}}

	snapshot := buildUsageSnapshot("/home/orli/.claude", rateLimits, now)

	if snapshot.FiveHour == nil || snapshot.FiveHour.UsedPercentage != 55 {
		t.Fatalf("snapshot.FiveHour = %+v, want UsedPercentage 55", snapshot.FiveHour)
	}
	if snapshot.SevenDay != nil {
		t.Fatalf("snapshot.SevenDay = %+v, want nil", snapshot.SevenDay)
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "seven_day") {
		t.Fatalf("marshaled snapshot = %s, must omit seven_day key", data)
	}
}

func TestUsageSnapshotPathIsStableAndCollisionFree(t *testing.T) {
	dir := t.TempDir()

	// Naive "replace / with _" would collide these two distinct paths;
	// the hash must not.
	a := usageSnapshotPath(dir, "/a/b_c")
	b := usageSnapshotPath(dir, "/a_b/c")
	if a == b {
		t.Fatalf("usageSnapshotPath collided for /a/b_c and /a_b/c: both = %q", a)
	}

	again := usageSnapshotPath(dir, "/a/b_c")
	if again != a {
		t.Fatalf("usageSnapshotPath(%q) = %q then %q, want stable filename", "/a/b_c", a, again)
	}
}

func TestWriteFileAtomicLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")

	if ok := writeFileAtomic(dir, path, []byte(`{"version":1}`)); !ok {
		t.Fatal("writeFileAtomic() = false, want true")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "snapshot.json" {
		t.Fatalf("dir entries = %v, want exactly [snapshot.json]", entries)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file mode = %o, want 0600", perm)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"version":1}` {
		t.Fatalf("file content = %q, want %q", got, `{"version":1}`)
	}
}

func TestWriteFileAtomicSyncsBeforeRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")

	var synced bool
	orig := syncFile
	syncFile = func(f *os.File) error {
		synced = true
		return f.Sync()
	}
	defer func() { syncFile = orig }()

	if ok := writeFileAtomic(dir, path, []byte(`{"version":1}`)); !ok {
		t.Fatal("writeFileAtomic() = false, want true")
	}
	if !synced {
		t.Fatal("writeFileAtomic did not sync the temp file before renaming")
	}
}

func TestWriteFileAtomicSkipsRenameWhenSyncFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")

	orig := syncFile
	syncFile = func(*os.File) error { return errors.New("sync failed") }
	defer func() { syncFile = orig }()

	if ok := writeFileAtomic(dir, path, []byte(`{"version":1}`)); ok {
		t.Fatal("writeFileAtomic() = true, want false when sync fails")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("target file should not exist when sync fails, stat err = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp file should be cleaned up after sync failure, dir entries = %v", entries)
	}
}

func TestCleanupStaleUsageTemp(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	stale := filepath.Join(dir, ".usage-stale.tmp")
	fresh := filepath.Join(dir, ".usage-fresh.tmp")
	other := filepath.Join(dir, "unrelated.tmp")
	for _, f := range []string{stale, fresh, other} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(stale, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	cleanupStaleUsageTemp(dir, now)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale temp file should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh temp file should survive: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("non-usage-temp file should survive: %v", err)
	}
}

func TestShouldSkipUsageWrite(t *testing.T) {
	tests := []struct {
		name     string
		prevBody string // "" means no previous file
		want     bool
	}{
		{
			name:     "no previous snapshot",
			prevBody: "",
			want:     false,
		},
		{
			name:     "unchanged and refreshed recently",
			prevBody: usageSnapshotJSON(t, 55, time.Now().Add(-usageRefreshInterval/2)),
			want:     true,
		},
		{
			name:     "unchanged but refresh is overdue",
			prevBody: usageSnapshotJSON(t, 55, time.Now().Add(-usageRefreshInterval-time.Second)),
			want:     false,
		},
		{
			name:     "changed value refreshed recently",
			prevBody: usageSnapshotJSON(t, 60, time.Now().Add(-usageRefreshInterval/2)),
			want:     false,
		},
		{
			name:     "corrupt previous file",
			prevBody: "{not json",
			want:     false,
		},
		{
			name:     "unchanged but checked_at is in the future within the interval",
			prevBody: usageSnapshotJSON(t, 55, time.Now().Add(usageRefreshInterval/2)),
			want:     true,
		},
		{
			name:     "unchanged but checked_at is far in the future (clock skew)",
			prevBody: usageSnapshotJSON(t, 55, time.Now().Add(10*usageRefreshInterval)),
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "snapshot.json")
			if tt.prevBody != "" {
				if err := os.WriteFile(path, []byte(tt.prevBody), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			snapshot := buildUsageSnapshot("/home/orli/.claude", &RateLimits{
				FiveHour: &RateLimit{UsedPercentage: 55},
			}, time.Now())

			if got := shouldSkipUsageWrite(path, snapshot); got != tt.want {
				t.Fatalf("shouldSkipUsageWrite() = %v, want %v", got, tt.want)
			}
		})
	}
}

// usageSnapshotJSON renders a minimal previous-snapshot file body with a
// single five_hour window, for feeding the throttle comparison directly.
func usageSnapshotJSON(t *testing.T, fiveHourUsed float64, checkedAt time.Time) string {
	t.Helper()
	snapshot := UsageSnapshot{
		Version:   1,
		ConfigDir: "/home/orli/.claude",
		CheckedAt: checkedAt.UTC().Format(time.RFC3339),
		FiveHour:  &UsageWindow{UsedPercentage: fiveHourUsed},
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWriteUsageSnapshot(t *testing.T) {
	setupUsageEnv := func(t *testing.T, usageDir string) {
		t.Helper()
		home := t.TempDir()
		t.Setenv("HOME", home)
		configDir := filepath.Join(home, ".claude-4")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CLAUDE_CONFIG_DIR", configDir)
		t.Setenv("STATUSLINE_USAGE_DIR", usageDir)
	}

	t.Run("disabled when env var unset", func(t *testing.T) {
		usageDir := t.TempDir()
		setupUsageEnv(t, usageDir)
		t.Setenv("STATUSLINE_USAGE_DIR", "")

		writeUsageSnapshot(Input{RateLimits: &RateLimits{FiveHour: &RateLimit{UsedPercentage: 55}}})

		assertDirEmpty(t, usageDir)
	})

	t.Run("skips when input carries no rate_limits", func(t *testing.T) {
		usageDir := t.TempDir()
		setupUsageEnv(t, usageDir)

		writeUsageSnapshot(Input{})

		assertDirEmpty(t, usageDir)
	})

	t.Run("writes both windows and omits unparseable resets_at", func(t *testing.T) {
		usageDir := t.TempDir()
		setupUsageEnv(t, usageDir)

		writeUsageSnapshot(Input{RateLimits: &RateLimits{
			FiveHour: &RateLimit{UsedPercentage: 55, ResetsAt: "2026-09-04T09:22:00Z"},
			SevenDay: &RateLimit{UsedPercentage: 92, ResetsAt: "garbage"},
		}})

		var got UsageSnapshot
		readSoleUsageFile(t, usageDir, &got)

		if got.Version != 1 {
			t.Fatalf("Version = %d, want 1", got.Version)
		}
		if _, err := time.Parse(time.RFC3339, got.CheckedAt); err != nil {
			t.Fatalf("CheckedAt = %q, not RFC3339: %v", got.CheckedAt, err)
		}
		if got.FiveHour == nil || got.FiveHour.ResetsAt != "2026-09-04T09:22:00Z" {
			t.Fatalf("FiveHour = %+v, want ResetsAt 2026-09-04T09:22:00Z", got.FiveHour)
		}
		if got.SevenDay == nil || got.SevenDay.ResetsAt != "" {
			t.Fatalf("SevenDay = %+v, want empty ResetsAt for unparseable input", got.SevenDay)
		}
	})

	t.Run("throttles unchanged writes, writes again once changed", func(t *testing.T) {
		usageDir := t.TempDir()
		setupUsageEnv(t, usageDir)

		writeUsageSnapshot(Input{RateLimits: &RateLimits{FiveHour: &RateLimit{UsedPercentage: 55}}})
		var first UsageSnapshot
		path := readSoleUsageFile(t, usageDir, &first)

		writeUsageSnapshot(Input{RateLimits: &RateLimits{FiveHour: &RateLimit{UsedPercentage: 55}}})
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var second UsageSnapshot
		if err := json.Unmarshal(data, &second); err != nil {
			t.Fatal(err)
		}
		if second.CheckedAt != first.CheckedAt {
			t.Fatalf("CheckedAt changed on unchanged/recent write: %q -> %q", first.CheckedAt, second.CheckedAt)
		}

		writeUsageSnapshot(Input{RateLimits: &RateLimits{FiveHour: &RateLimit{UsedPercentage: 60}}})
		var third UsageSnapshot
		readSoleUsageFile(t, usageDir, &third)
		if third.FiveHour == nil || third.FiveHour.UsedPercentage != 60 {
			t.Fatalf("FiveHour = %+v, want UsedPercentage 60 after a changed value", third.FiveHour)
		}
	})

	t.Run("creates the usage dir when it doesn't exist yet", func(t *testing.T) {
		usageDir := filepath.Join(t.TempDir(), "does", "not", "exist")
		setupUsageEnv(t, usageDir)

		writeUsageSnapshot(Input{RateLimits: &RateLimits{FiveHour: &RateLimit{UsedPercentage: 55}}})

		info, err := os.Stat(usageDir)
		if err != nil {
			t.Fatalf("usage dir should have been created: %v", err)
		}
		if !info.IsDir() {
			t.Fatalf("%s should be a directory", usageDir)
		}

		var got UsageSnapshot
		readSoleUsageFile(t, usageDir, &got)
		if got.FiveHour == nil || got.FiveHour.UsedPercentage != 55 {
			t.Fatalf("FiveHour = %+v, want UsedPercentage 55", got.FiveHour)
		}
	})

	t.Run("stays disabled and writes nothing when the env var is unset, regardless of the registry", func(t *testing.T) {
		usageDir := t.TempDir()
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("CLAUDE_CONFIG_DIR", "")
		t.Setenv("STATUSLINE_ACCOUNT_REGISTRY", filepath.Join(home, "does-not-exist.json"))
		t.Setenv("STATUSLINE_USAGE_DIR", "")

		writeUsageSnapshot(Input{RateLimits: &RateLimits{FiveHour: &RateLimit{UsedPercentage: 55}}})

		assertDirEmpty(t, usageDir)
		entries, err := os.ReadDir(home)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("HOME should be untouched, entries = %v", entries)
		}
	})
}

// The usage snapshot write must never gate the printed status line: it
// runs after render's output is already written, not as one of the
// goroutines the output waits on.
func TestRenderWritesUsageSnapshotAfterPrinting(t *testing.T) {
	origFn := usageSnapshotFn
	defer func() { usageSnapshotFn = origFn }()

	var buf bytes.Buffer
	var sawOutputBeforeWrite bool
	usageSnapshotFn = func(Input) {
		sawOutputBeforeWrite = buf.Len() > 0
	}

	input := Input{}
	input.Model.DisplayName = "Claude Test"
	render(input, &buf)

	if !sawOutputBeforeWrite {
		t.Fatal("usage snapshot write ran before the status line was printed")
	}
}

// No environment variable this program reads is required: everything must
// degrade to printing a plain status line, never a panic or a bad exit.
func TestRenderNeverPanicsWithNoConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("STATUSLINE_ACCOUNT", "")
	t.Setenv("STATUSLINE_ACCOUNT_REGISTRY", "")
	t.Setenv("STATUSLINE_USAGE_DIR", "")

	input := Input{}
	input.Model.DisplayName = "Claude Test"

	var buf bytes.Buffer
	render(input, &buf)

	if !strings.Contains(buf.String(), "Claude Test") {
		t.Fatalf("render output = %q, want it to contain the model name", buf.String())
	}
}

// STATUSLINE_USAGE_DIR must be purely additive: whether it is set must not
// change a single byte of what gets printed.
func TestRenderOutputUnaffectedByUsageDirSetting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("STATUSLINE_ACCOUNT", "0")

	input := Input{}
	input.Model.DisplayName = "Claude Test"

	t.Setenv("STATUSLINE_USAGE_DIR", "")
	var withoutDir bytes.Buffer
	render(input, &withoutDir)

	t.Setenv("STATUSLINE_USAGE_DIR", t.TempDir())
	var withDir bytes.Buffer
	render(input, &withDir)

	if withoutDir.String() != withDir.String() {
		t.Fatalf("render output changed based on STATUSLINE_USAGE_DIR:\nwithout = %q\nwith    = %q",
			withoutDir.String(), withDir.String())
	}
}

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("dir entries = %v, want none", entries)
	}
}

// readSoleUsageFile expects exactly one file in dir, decodes it into out,
// and returns its path.
func readSoleUsageFile(t *testing.T, dir string, out *UsageSnapshot) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir entries = %v, want exactly one usage file", entries)
	}
	path := filepath.Join(dir, entries[0].Name())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
	return path
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
