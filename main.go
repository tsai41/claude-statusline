package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ColorReset  = "\033[0m"
	ColorDim    = "\033[2m"
	ColorBold   = "\033[1m"
	ColorWhite  = "\033[37m"
	ColorGold   = "\033[38;2;195;158;83m"
	ColorCyan   = "\033[36m"
	ColorGreen  = "\033[32m"
	ColorRed    = "\033[31m"
	ColorYellow = "\033[33m"
	ColorPurple = "\033[1;35m"
	ColorGray   = "\033[38;2;152;195;121m" // message color

	SEP = "\033[2m │ \033[0m"
)

var modelColors = map[string]string{
	"Opus":   "\033[38;2;195;158;83m",
	"Sonnet": "\033[38;2;118;170;185m",
	"Haiku":  "\033[38;2;255;182;193m",
}

type ContextWindow struct {
	UsedPercentage      float64 `json:"used_percentage"`
	RemainingPercentage float64 `json:"remaining_percentage"`
	ContextWindowSize   int     `json:"context_window_size"`
}

type RateLimit struct {
	UsedPercentage float64     `json:"used_percentage"`
	ResetsAt       interface{} `json:"resets_at"`
}

type RateLimits struct {
	FiveHour *RateLimit `json:"five_hour"`
	SevenDay *RateLimit `json:"seven_day"`
}

type Input struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	SessionID string `json:"session_id"`
	Workspace struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
	CWD            string         `json:"cwd"`
	TranscriptPath string         `json:"transcript_path,omitempty"`
	ContextWindow  *ContextWindow `json:"context_window,omitempty"`
	RateLimits     *RateLimits    `json:"rate_limits,omitempty"`
}

type Session struct {
	ID            string     `json:"id"`
	Date          string     `json:"date"`
	Start         int64      `json:"start"`
	LastHeartbeat int64      `json:"last_heartbeat"`
	TotalSeconds  int64      `json:"total_seconds"`
	Intervals     []Interval `json:"intervals"`
}

type Interval struct {
	Start int64  `json:"start"`
	End   *int64 `json:"end"`
}

type Result struct {
	Type string
	Data interface{}
}

// UsageWindow is one rate-limit window in the published usage-snapshot
// contract; ResetsAt is omitted rather than a zero value when the input
// could not be normalized into RFC3339.
type UsageWindow struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       string  `json:"resets_at,omitempty"`
}

// UsageSnapshot is the STATUSLINE_USAGE_DIR file contract; other programs
// key their reads on ConfigDir, so its shape and field names are fixed.
type UsageSnapshot struct {
	Version   int          `json:"version"`
	ConfigDir string       `json:"config_dir"`
	CheckedAt string       `json:"checked_at"`
	FiveHour  *UsageWindow `json:"five_hour,omitempty"`
	SevenDay  *UsageWindow `json:"seven_day,omitempty"`
}

// usageSnapshotVersion is the UsageSnapshot format version. A reader
// accepts only an exact match and shows no data for anything else, so this
// only moves for a change that would make an existing reader wrong. Adding
// a new optional field that old readers already tolerate ignoring keeps
// this at 1; bump it only when that stops being true.
const usageSnapshotVersion = 1

var (
	gitBranchCache   string
	gitBranchExpires time.Time
	cacheMutex       sync.RWMutex
)

func main() {
	currentFlags = parseFlags(os.Args[1:])

	var input Input
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to decode input: %v\n", err)
		os.Exit(1)
	}
	render(input, os.Stdout)
}

// cliFlags holds parsed --flag values. Zero value (all empty/false) means
// none were passed, matching the behavior before these flags existed:
// callers fall through to the environment variable.
type cliFlags struct {
	usageDir        string
	accountRegistry string
	noAccount       bool
	noLastMessage   bool
}

// currentFlags is parsed once in main and consulted by usageDir,
// accountRegistryPath and accountHidden. Tests set it directly.
var currentFlags cliFlags

// parseFlags never exits or panics on a bad argument: this program's
// stdout is a Claude Code status line, so a malformed flag must be ignored
// rather than abort the render. ContinueOnError plus a discarded error
// achieves that; flag's default ExitOnError would os.Exit instead.
func parseFlags(args []string) cliFlags {
	var f cliFlags
	fs := flag.NewFlagSet("claude-statusline", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&f.usageDir, "usage-dir", "", "override STATUSLINE_USAGE_DIR")
	fs.StringVar(&f.accountRegistry, "account-registry", "", "override STATUSLINE_ACCOUNT_REGISTRY")
	fs.BoolVar(&f.noAccount, "no-account", false, "hide the account tag, like STATUSLINE_ACCOUNT=0")
	fs.BoolVar(&f.noLastMessage, "no-last-message", false, "hide the last user message on line 3, like STATUSLINE_LAST_MESSAGE=0")
	_ = fs.Parse(args)
	return f
}

// usageSnapshotFn is writeUsageSnapshot, indirected so tests can observe
// when it runs relative to render's output instead of gating on it.
var usageSnapshotFn = writeUsageSnapshot

// render gathers the per-render data, writes the formatted status line to
// out, and only then records the usage snapshot. The snapshot write is not
// part of the goroutine set that gates out, so a slow or hung
// STATUSLINE_USAGE_DIR filesystem can never delay or suppress the printed
// status line — at worst it costs that one snapshot if the process is
// killed before the write finishes.
func render(input Input, out io.Writer) {
	// prefer cwd over workspace.current_dir
	dir := input.CWD
	if dir == "" {
		dir = input.Workspace.CurrentDir
	}

	homeDir, _ := os.UserHomeDir()
	hideLineThree := compactModeOn(homeDir)

	results := make(chan Result, 6)
	var wg sync.WaitGroup
	wg.Add(6)

	go func() { defer wg.Done(); results <- Result{"git", getGitBranch(dir)} }()
	go func() {
		defer wg.Done()
		message := ""
		if !hideLineThree && !lastMessageHidden() {
			message = extractUserMessage(input.TranscriptPath, input.SessionID)
		}
		results <- Result{"message", message}
	}()
	go func() {
		defer wg.Done()
		results <- Result{"tool", extractLastTool(input.TranscriptPath, input.SessionID)}
	}()
	go func() { defer wg.Done(); results <- Result{"effort", readEffortLevel()} }()
	go func() {
		defer wg.Done()
		results <- Result{"editwt", detectEditWorktree(input.TranscriptPath, input.SessionID)}
	}()
	go func() { defer wg.Done(); results <- Result{"account", readAccountTag()} }()

	go func() { wg.Wait(); close(results) }()

	var gitBranch, userMessage, lastTool, effort, editWorktree, account string
	for r := range results {
		switch r.Type {
		case "git":
			gitBranch = r.Data.(string)
		case "message":
			userMessage = r.Data.(string)
		case "tool":
			lastTool = r.Data.(string)
		case "effort":
			effort = r.Data.(string)
		case "editwt":
			editWorktree = r.Data.(string)
		case "account":
			account = r.Data.(string)
		}
	}

	updateSession(input.SessionID)

	shortDir := dir
	if homeDir != "" {
		shortDir = strings.Replace(dir, homeDir, "~", 1)
	}

	// line 1: dir │ git │ model │ effort
	var line1 []string
	line1 = append(line1, fmt.Sprintf("%s%s%s", ColorCyan, shortDir, ColorReset))
	if gitBranch != "" {
		line1 = append(line1, fmt.Sprintf("%s⚡ %s%s", ColorWhite, gitBranch, ColorReset))
	}
	if editWorktree != "" {
		line1 = append(line1, fmt.Sprintf("%s%s%s", ColorCyan, editWorktree, ColorReset))
	}
	line1 = append(line1, fmt.Sprintf("%s%s%s", ColorPurple, input.Model.DisplayName, ColorReset))
	if effort != "" {
		line1 = append(line1, fmt.Sprintf("%s%s%s", ColorDim, effort, ColorReset))
	}
	fmt.Fprintln(out, strings.Join(line1, SEP))

	// line 2: ctx │ rate limits │ account
	fmt.Fprintln(out, formatMetricsLine(input, account))

	// third line: special tools + last user message
	if !hideLineThree {
		if lastTool != "" {
			fmt.Fprintf(out, "%s│%s tool: %s\n", ColorDim, ColorReset, lastTool)
		}
		if userMessage != "" {
			fmt.Fprint(out, userMessage)
		}
	}

	usageSnapshotFn(input)
}

// compactModeOn reports whether the toggle file exists, which hides line 3.
// Its name is a shared contract with the shell alias that creates and removes
// it.
func compactModeOn(homeDir string) bool {
	if homeDir == "" && os.Getenv("CLAUDE_CONFIG_DIR") == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(claudeConfigDir(homeDir), "statusline-compact"))
	return err == nil
}

func formatMetricsLine(input Input, account string) string {
	var line2 []string
	if input.ContextWindow != nil {
		used := int(input.ContextWindow.UsedPercentage)
		remaining := 100 - used
		color := colorByRemaining(remaining)
		bar := miniBar(used)
		line2 = append(line2, fmt.Sprintf("%sctx %s %d%%%s", color, bar, used, ColorReset))
	}
	if input.RateLimits != nil {
		if rl := input.RateLimits.FiveHour; rl != nil {
			remaining := 100 - int(rl.UsedPercentage)
			color := colorByRemaining(remaining)
			reset := formatResetTime(rl.ResetsAt)
			line2 = append(line2, fmt.Sprintf("%s5h: %d%%%s%s%s%s", color, remaining, ColorReset, ColorDim, reset, ColorReset))
		}
		if rl := input.RateLimits.SevenDay; rl != nil {
			remaining := 100 - int(rl.UsedPercentage)
			color := colorByRemaining(remaining)
			reset := formatResetTime(rl.ResetsAt)
			line2 = append(line2, fmt.Sprintf("%s7d: %d%%%s%s%s%s", color, remaining, ColorReset, ColorDim, reset, ColorReset))
		}
	}
	// Last, in the branch's color: leftmost on this line sits right under
	// Claude Code's own permission-mode hint, and a warm-colored tag there
	// read as part of it. Branch and account are the same kind of fact --
	// "where am I" -- so they share a color.
	if account != "" {
		line2 = append(line2, fmt.Sprintf("%s%s%s", ColorWhite, account, ColorReset))
	}
	return strings.Join(line2, SEP)
}

// nowFn is time.Now, indirected so tests can pin the reset countdown.
var nowFn = time.Now

// formatResetTime returns the time until reset, or "" if unavailable/past.
func formatResetTime(resetsAt interface{}) string {
	var t time.Time
	switch v := resetsAt.(type) {
	case float64:
		t = time.Unix(int64(v), 0)
	case string:
		if parsed, err := time.Parse(time.RFC3339, v); err == nil {
			t = parsed
		}
	}
	if t.IsZero() {
		return ""
	}
	d := t.Sub(nowFn())
	if d <= 0 {
		return ""
	}
	return " (" + formatResetDuration(d) + ")"
}

func formatResetDuration(d time.Duration) string {
	days := int(d / (24 * time.Hour))
	if days > 0 {
		hours := int(d/time.Hour) % 24
		if hours == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// colorByRemaining returns color based on remaining percentage (green≥50, yellow 21-49, red≤20)
func colorByRemaining(remaining int) string {
	if remaining <= 20 {
		return ColorRed
	} else if remaining <= 49 {
		return ColorYellow
	}
	return ColorGreen
}

// miniBar generates a 10-char progress bar showing remaining (━=filled, ┄=empty)
func miniBar(remaining int) string {
	width := 10
	filled := remaining * width / 100
	if filled > width {
		filled = width
	}
	empty := width - filled
	return strings.Repeat("━", filled) + strings.Repeat("┄", empty)
}

func formatModel(model string) string {
	for key, color := range modelColors {
		if strings.Contains(model, key) {
			return fmt.Sprintf("%s%s%s", color, model, ColorReset)
		}
	}
	return model
}

func getGitBranch(dir string) string {
	cacheMutex.RLock()
	if time.Now().Before(gitBranchExpires) && gitBranchCache != "" {
		result := gitBranchCache
		cacheMutex.RUnlock()
		return result
	}
	cacheMutex.RUnlock()

	if err := exec.Command("git", "-C", dir, "rev-parse", "--git-dir").Run(); err != nil {
		return ""
	}

	output, err := exec.Command("git", "-C", dir, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(output))

	// dirty status: count staged and unstaged changes
	dirty := ""
	if out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output(); err == nil {
		staged, unstaged := 0, 0
		for _, line := range strings.Split(string(out), "\n") {
			if len(line) < 2 {
				continue
			}
			if line[0] != ' ' && line[0] != '?' {
				staged++
			}
			if line[1] != ' ' {
				unstaged++
			}
		}
		if staged > 0 || unstaged > 0 {
			dirty = fmt.Sprintf(" %s+%d~%d%s", ColorYellow, staged, unstaged, ColorGreen)
		}
	}

	result := branch + dirty
	cacheMutex.Lock()
	gitBranchCache = result
	gitBranchExpires = time.Now().Add(5 * time.Second)
	cacheMutex.Unlock()

	return result
}

// readAccountTag names the account this session runs as: the ccs registry
// entry whose home is this session's config dir ("#4 orli"). Empty when the
// registry doesn't list this config dir, the registry is missing or
// unreadable, or the tag is hidden (see accountHidden).
func readAccountTag() string {
	if accountHidden() {
		return ""
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	configDir := claudeConfigDir(homeDir)
	return registryAccountTag(homeDir, configDir)
}

// registryAccountTag asks the ccs registry what it calls the account
// living in configDir. The home is matched with os.SameFile rather than by
// spelling: CLAUDE_CONFIG_DIR and the registry can name one directory
// differently (a symlink, a case-insensitive volume, a trailing slash) and
// a missed match would silently label the session as somebody else.
func registryAccountTag(homeDir, configDir string) string {
	raw, err := os.ReadFile(accountRegistryPath(homeDir))
	if err != nil {
		return ""
	}
	var registry struct {
		Accounts []struct {
			Provider string `json:"provider"`
			Number   int    `json:"number"`
			Home     string `json:"home"`
			Alias    string `json:"alias"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &registry); err != nil {
		return ""
	}
	current, err := os.Stat(configDir)
	if err != nil {
		return ""
	}
	for _, account := range registry.Accounts {
		if account.Provider != "claude" {
			continue
		}
		home, err := os.Stat(account.Home)
		if err != nil || !os.SameFile(current, home) {
			continue
		}
		if account.Alias == "" {
			return fmt.Sprintf("#%d", account.Number)
		}
		return fmt.Sprintf("#%d %s", account.Number, account.Alias)
	}
	return ""
}

// accountHidden reports whether the account tag should be suppressed:
// --no-account, or else STATUSLINE_ACCOUNT set to a falsy value.
func accountHidden() bool {
	return currentFlags.noAccount || envOff("STATUSLINE_ACCOUNT")
}

// envOff reports whether the named env var is set to a falsy value.
func envOff(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

// lastMessageHidden reports whether line 3's user message should be
// suppressed: --no-last-message, or else STATUSLINE_LAST_MESSAGE set to a
// falsy value.
func lastMessageHidden() bool {
	return currentFlags.noLastMessage || envOff("STATUSLINE_LAST_MESSAGE")
}

// accountRegistryPath is the ccs registry file naming known accounts,
// overridable via --account-registry or STATUSLINE_ACCOUNT_REGISTRY so this
// program reads a roster of accounts rather than presuming whose roster it
// is.
func accountRegistryPath(homeDir string) string {
	if currentFlags.accountRegistry != "" {
		return currentFlags.accountRegistry
	}
	if path := os.Getenv("STATUSLINE_ACCOUNT_REGISTRY"); path != "" {
		return path
	}
	return filepath.Join(homeDir, ".config", "agent-conversation-handoff", "accounts.json")
}

// claudeConfigDir honors CLAUDE_CONFIG_DIR, defaulting to homeDir/.claude.
func claudeConfigDir(homeDir string) string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(homeDir, ".claude")
}

func readEffortLevel() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	data, err := os.ReadFile(filepath.Join(homeDir, ".claude", "settings.json"))
	if err != nil {
		return ""
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return ""
	}

	effort, _ := settings["effortLevel"].(string)
	short := map[string]string{"low": "effort:L", "medium": "effort:M", "high": "effort:H"}
	if s, ok := short[effort]; ok {
		return s
	}
	if effort != "" {
		return "effort:" + effort
	}
	return ""
}

// usageDir resolves where to write the usage snapshot: --usage-dir if
// passed, else STATUSLINE_USAGE_DIR, else "" (write nothing).
func usageDir() string {
	if currentFlags.usageDir != "" {
		return currentFlags.usageDir
	}
	return os.Getenv("STATUSLINE_USAGE_DIR")
}

// writeUsageSnapshot records the account's rate-limit usage to usageDir()
// for another program to read, since a live status line render is the only
// place this data ever appears. It never requires that other program to
// exist or be running — it only ever writes a file. Every failure path (no
// configured dir, no rate_limits, a dir that can't be created, marshal
// error) is a silent no-op — this must never affect what the status line
// prints.
func writeUsageSnapshot(input Input) {
	dir := usageDir()
	if dir == "" || input.RateLimits == nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}
	configDir := claudeConfigDir(homeDir)

	snapshot := buildUsageSnapshot(configDir, input.RateLimits, time.Now())
	path := usageSnapshotPath(dir, configDir)
	if shouldSkipUsageWrite(path, snapshot) {
		return
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	if writeFileAtomic(dir, path, data) {
		cleanupStaleUsageTemp(dir, time.Now())
	}
}

func buildUsageSnapshot(configDir string, rateLimits *RateLimits, now time.Time) UsageSnapshot {
	snapshot := UsageSnapshot{
		Version:   usageSnapshotVersion,
		ConfigDir: configDir,
		CheckedAt: now.UTC().Format(time.RFC3339),
	}
	if rateLimits.FiveHour != nil {
		snapshot.FiveHour = usageWindowFrom(rateLimits.FiveHour)
	}
	if rateLimits.SevenDay != nil {
		snapshot.SevenDay = usageWindowFrom(rateLimits.SevenDay)
	}
	return snapshot
}

func usageWindowFrom(rl *RateLimit) *UsageWindow {
	window := &UsageWindow{UsedPercentage: rl.UsedPercentage}
	if resetsAt, ok := normalizeResetsAt(rl.ResetsAt); ok {
		window.ResetsAt = resetsAt
	}
	return window
}

// plausibleResetTime bounds a rate-limit reset value. A unix timestamp
// misread from milliseconds lands decades outside this range, and gets
// rejected the same way an unparseable value does rather than written out.
var (
	minPlausibleResetTime = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	maxPlausibleResetTime = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)

// normalizeResetsAt converts a rate-limit reset value — a unix timestamp or
// an RFC3339 string, since Claude Code sends this field inconsistently —
// into an RFC3339 UTC string. ok is false when the value is absent, in a
// shape neither branch recognizes, or outside plausibleResetTime, so the
// caller can omit it instead of inventing or trusting a bogus time.
func normalizeResetsAt(resetsAt interface{}) (string, bool) {
	var t time.Time
	switch v := resetsAt.(type) {
	case float64:
		t = time.Unix(int64(v), 0).UTC()
	case string:
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return "", false
		}
		t = parsed.UTC()
	default:
		return "", false
	}
	if t.Before(minPlausibleResetTime) || t.After(maxPlausibleResetTime) {
		return "", false
	}
	return t.Format(time.RFC3339), true
}

// usageSnapshotPath derives a stable, filesystem-safe filename from
// configDir. Hashing (not just sanitizing separators) is what makes two
// different config dirs collision-free: replacing "/" with another
// character can map distinct paths to the same string, a hash cannot.
func usageSnapshotPath(dir, configDir string) string {
	sum := sha256.Sum256([]byte(configDir))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".json")
}

// usageRefreshInterval bounds how long an unchanged snapshot's checked_at
// goes without a refresh. The goal is avoiding redundant writes, not
// bounding staleness tightly, so this is minutes rather than seconds: an
// idle session's file would otherwise never get touched again, and a
// reader would report it as increasingly old for a session that is open
// right now.
const usageRefreshInterval = 5 * time.Minute

// shouldSkipUsageWrite implements the throttle: skip when the previous
// snapshot at path already reflects the same windows and checked_at is
// within usageRefreshInterval of now. The comparison is absolute rather
// than time.Since, so a checked_at in the future (clock skew, or a file
// copied from another machine) can't pin the file until real time catches
// up to it. Any read/parse failure means there is nothing to compare
// against, so the write proceeds.
func shouldSkipUsageWrite(path string, snapshot UsageSnapshot) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var prev UsageSnapshot
	if err := json.Unmarshal(data, &prev); err != nil {
		return false
	}
	if !usageWindowEqual(prev.FiveHour, snapshot.FiveHour) || !usageWindowEqual(prev.SevenDay, snapshot.SevenDay) {
		return false
	}
	checkedAt, err := time.Parse(time.RFC3339, prev.CheckedAt)
	if err != nil {
		return false
	}
	age := time.Since(checkedAt)
	if age < 0 {
		age = -age
	}
	return age < usageRefreshInterval
}

func usageWindowEqual(a, b *UsageWindow) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// syncFile is (*os.File).Sync, indirected so tests can simulate a sync
// failure without needing a real filesystem fault.
var syncFile = func(f *os.File) error { return f.Sync() }

// writeFileAtomic writes data to path via a temp file in dir, fsynced then
// renamed, so a concurrent reader never observes a partial write and a
// crash right after can't leave a zero-byte path. Mode 0600 comes from
// os.CreateTemp's default, not an explicit chmod. Reports whether it wrote.
func writeFileAtomic(dir, path string, data []byte) bool {
	tmp, err := os.CreateTemp(dir, ".usage-*.tmp")
	if err != nil {
		return false
	}
	tmpPath := tmp.Name()

	_, writeErr := tmp.Write(data)
	syncErr := syncFile(tmp)
	closeErr := tmp.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		os.Remove(tmpPath)
		return false
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return false
	}
	return true
}

// cleanupStaleUsageTemp removes .usage-*.tmp leftovers from a process that
// was killed between CreateTemp and Rename (a status line timeout is a
// real case) — the reader only looks at *.json, so nothing else would ever
// notice or clear them. Any error here is ignored: cleanup must never turn
// into a write failure.
func cleanupStaleUsageTemp(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, ".usage-") || !strings.HasSuffix(name, ".tmp") {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) <= time.Hour {
			continue
		}
		os.Remove(filepath.Join(dir, name))
	}
}

// worktreeName returns the linked-worktree name for dir, or "" if dir is not
// inside a linked worktree (main tree or non-repo). A linked worktree's
// git dir is .../worktrees/<name>.
func worktreeName(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return ""
	}
	absGitDir := strings.TrimSpace(string(out))
	if strings.Contains(absGitDir, "/worktrees/") {
		return filepath.Base(absGitDir)
	}
	return ""
}

// detectEditWorktree reports the linked worktree that the most recent file
// edit landed in, as "→wt:<name>", or "" if the latest edit was not in a
// worktree. Source is the transcript, not cwd: it surfaces where work is
// going when the session itself stays in the main tree.
func detectEditWorktree(transcriptPath, sessionID string) string {
	if transcriptPath == "" {
		return ""
	}
	editTools := map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

	lines := readLastLines(transcriptPath, 100)
	for i := len(lines) - 1; i >= 0; i-- {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(lines[i]), &data); err != nil {
			continue
		}
		if sidechain, _ := data["isSidechain"].(bool); sidechain {
			continue
		}
		if sid, _ := data["sessionId"].(string); sid != sessionID {
			continue
		}
		if data["type"] != "assistant" {
			continue
		}
		message, ok := data["message"].(map[string]interface{})
		if !ok {
			continue
		}
		content, ok := message["content"].([]interface{})
		if !ok {
			continue
		}
		// newest tool_use within the message first
		for j := len(content) - 1; j >= 0; j-- {
			c, ok := content[j].(map[string]interface{})
			if !ok || c["type"] != "tool_use" {
				continue
			}
			if name, _ := c["name"].(string); !editTools[name] {
				continue
			}
			args, _ := c["input"].(map[string]interface{})
			fp, _ := args["file_path"].(string)
			if fp == "" {
				continue
			}
			// most recent edit found — decide here and stop
			if name := worktreeName(filepath.Dir(fp)); name != "" {
				return "→wt:" + name
			}
			return ""
		}
	}
	return ""
}

func extractLastTool(transcriptPath, sessionID string) string {
	if transcriptPath == "" {
		return ""
	}

	lines := readLastLines(transcriptPath, 100)
	for i := len(lines) - 1; i >= 0; i-- {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(lines[i]), &data); err != nil {
			continue
		}
		if sidechain, _ := data["isSidechain"].(bool); sidechain {
			continue
		}
		if sid, _ := data["sessionId"].(string); sid != sessionID {
			continue
		}
		if data["type"] != "assistant" {
			continue
		}
		message, ok := data["message"].(map[string]interface{})
		if !ok {
			continue
		}
		content, ok := message["content"].([]interface{})
		if !ok {
			continue
		}

		// collect special tool labels only
		counts := map[string]int{}
		var order []string
		for _, item := range content {
			c, ok := item.(map[string]interface{})
			if !ok || c["type"] != "tool_use" {
				continue
			}
			name, _ := c["name"].(string)
			if name == "" {
				continue
			}
			label := friendlyToolName(name, c["input"])
			if label == "" {
				continue // skip common tools
			}
			if counts[label] == 0 {
				order = append(order, label)
			}
			counts[label]++
		}

		if len(order) == 0 {
			continue
		}

		var parts []string
		for _, label := range order {
			if counts[label] > 1 {
				parts = append(parts, fmt.Sprintf("%s×%d", label, counts[label]))
			} else {
				parts = append(parts, label)
			}
		}
		return strings.Join(parts, "  ")
	}
	return ""
}

// commonTools are routine tools not worth surfacing in the status line.
var commonTools = map[string]bool{
	"Bash": true, "Edit": true, "Write": true, "Read": true,
	"Glob": true, "Grep": true, "WebFetch": true, "WebSearch": true,
	"TodoWrite": true, "TodoRead": true, "LS": true,
}

// friendlyToolName converts a tool name + input into a human-readable label.
// Returns "" for common tools that should be hidden.
func friendlyToolName(name string, input interface{}) string {
	if commonTools[name] {
		return ""
	}

	args, _ := input.(map[string]interface{})

	switch name {
	case "Skill":
		if skill, _ := args["skill"].(string); skill != "" {
			if idx := strings.LastIndex(skill, ":"); idx >= 0 {
				skill = skill[idx+1:]
			}
			return fmt.Sprintf("Skill(%s)", skill)
		}
		return "Skill"

	case "Agent":
		if t, _ := args["subagent_type"].(string); t != "" {
			if idx := strings.LastIndex(t, ":"); idx >= 0 {
				t = t[idx+1:]
			}
			return fmt.Sprintf("Agent(%s)", t)
		}
		return "Agent"

	default:
		// mcp__plugin_name__tool_name → plugin:tool
		if strings.HasPrefix(name, "mcp__") {
			parts := strings.SplitN(strings.TrimPrefix(name, "mcp__"), "__", 2)
			if len(parts) == 2 {
				plugin := shortenMCPPlugin(parts[0])
				tool := parts[1]
				return fmt.Sprintf("%s:%s", plugin, tool)
			}
		}
		return name
	}
}

func shortenMCPPlugin(plugin string) string {
	shortcuts := map[string]string{
		"plugin_oh-my-claudecode_t": "omc",
		"context7":                  "ctx7",
		"playwright":                "pw",
	}
	if s, ok := shortcuts[plugin]; ok {
		return s
	}
	// strip common prefixes
	plugin = strings.TrimPrefix(plugin, "plugin_")
	if len(plugin) > 8 {
		return plugin[:8]
	}
	return plugin
}

func updateSession(sessionID string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}

	sessionsDir := filepath.Join(homeDir, ".claude", "session-tracker", "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return
	}

	sessionFile := filepath.Join(sessionsDir, sessionID+".json")
	currentTime := time.Now().Unix()
	today := time.Now().Format("2006-01-02")

	var session Session
	if data, err := os.ReadFile(sessionFile); err == nil {
		json.Unmarshal(data, &session)
	} else {
		session = Session{
			ID:            sessionID,
			Date:          today,
			Start:         currentTime,
			LastHeartbeat: currentTime,
			Intervals:     []Interval{{Start: currentTime}},
		}
	}

	gap := currentTime - session.LastHeartbeat
	session.LastHeartbeat = currentTime

	if gap < 600 {
		if len(session.Intervals) > 0 {
			session.Intervals[len(session.Intervals)-1].End = &currentTime
		}
	} else {
		session.Intervals = append(session.Intervals, Interval{Start: currentTime, End: &currentTime})
	}

	var total int64
	for _, interval := range session.Intervals {
		if interval.End != nil {
			total += *interval.End - interval.Start
		}
	}
	session.TotalSeconds = total

	if data, err := json.Marshal(session); err == nil {
		os.WriteFile(sessionFile, data, 0644)
	}
}

func readLastLines(path string, n int) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	const maxBuf = 1024 * 1024
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, maxBuf)
	scanner.Buffer(buf, maxBuf)

	var all []string
	for scanner.Scan() {
		t := scanner.Text()
		if strings.TrimSpace(t) != "" {
			all = append(all, t)
		}
	}

	if len(all) <= n {
		return all
	}
	return all[len(all)-n:]
}

func extractUserMessage(transcriptPath, sessionID string) string {
	if transcriptPath == "" {
		return ""
	}

	lines := readLastLines(transcriptPath, 200)
	for i := len(lines) - 1; i >= 0; i-- {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(lines[i]), &data); err != nil {
			continue
		}
		if sidechain, _ := data["isSidechain"].(bool); sidechain {
			continue
		}
		if sid, _ := data["sessionId"].(string); sid != sessionID {
			continue
		}
		message, ok := data["message"].(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := message["role"].(string)
		msgType, _ := data["type"].(string)
		if role != "user" || msgType != "user" {
			continue
		}
		content, ok := message["content"].(string)
		if !ok || isSystemMessage(content) {
			continue
		}
		return formatUserMessage(content)
	}
	return ""
}

func isSystemMessage(content string) bool {
	if (strings.HasPrefix(content, "[") && strings.HasSuffix(content, "]")) ||
		(strings.HasPrefix(content, "{") && strings.HasSuffix(content, "}")) {
		return true
	}
	for _, tag := range []string{"<local-command-stdout>", "<command-name>", "<command-message>", "<command-args>", "<task-notification>", "<task-id>", "<tool-use-id>"} {
		if strings.Contains(content, tag) {
			return true
		}
	}
	return strings.HasPrefix(content, "Caveat:")
}

func formatUserMessage(message string) string {
	if message == "" {
		return ""
	}
	lineWidth := 80
	// collapse to first non-empty line only, to keep the status line short
	line := ""
	for _, l := range strings.Split(message, "\n") {
		if strings.TrimSpace(l) != "" {
			line = strings.TrimSpace(l)
			break
		}
	}
	if line == "" {
		return ""
	}
	if len([]rune(line)) > lineWidth {
		runes := []rune(line)
		line = string(runes[:lineWidth-3]) + "..."
	}
	return fmt.Sprintf("%s│%s %s\n", ColorDim, ColorReset, line)
}

func formatNumber(num int) string {
	if num == 0 {
		return "--"
	}
	if num >= 1000000 {
		return fmt.Sprintf("%dM", num/1000000)
	} else if num >= 1000 {
		return fmt.Sprintf("%dk", num/1000)
	}
	return strconv.Itoa(num)
}
