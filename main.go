package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ColorReset = "\033[0m"
	ColorDim   = "\033[2m"
	ColorBold  = "\033[1m"
	ColorGold  = "\033[38;2;195;158;83m"
	ColorCyan  = "\033[36m"
	ColorGreen = "\033[32m"
	ColorRed   = "\033[31m"
	ColorYellow = "\033[33m"
	ColorPurple = "\033[1;35m"
	ColorGray  = "\033[38;2;152;195;121m" // message color

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
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       interface{} `json:"resets_at"`
}

type RateLimits struct {
	FiveHour  *RateLimit `json:"five_hour"`
	SevenDay  *RateLimit `json:"seven_day"`
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

var (
	gitBranchCache   string
	gitBranchExpires time.Time
	cacheMutex       sync.RWMutex
)

func main() {
	var input Input
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to decode input: %v\n", err)
		os.Exit(1)
	}

	// prefer cwd over workspace.current_dir
	dir := input.CWD
	if dir == "" {
		dir = input.Workspace.CurrentDir
	}

	results := make(chan Result, 5)
	var wg sync.WaitGroup
	wg.Add(5)

	go func() { defer wg.Done(); results <- Result{"git", getGitBranch(dir)} }()
	go func() { defer wg.Done(); results <- Result{"hours", calculateTotalHours()} }()
	go func() { defer wg.Done(); results <- Result{"message", extractUserMessage(input.TranscriptPath, input.SessionID)} }()
	go func() { defer wg.Done(); results <- Result{"tool", extractLastTool(input.TranscriptPath, input.SessionID)} }()
	go func() { defer wg.Done(); results <- Result{"effort", readEffortLevel()} }()

	go func() { wg.Wait(); close(results) }()

	var gitBranch, totalHours, userMessage, lastTool, effort string
	for r := range results {
		switch r.Type {
		case "git":
			gitBranch = r.Data.(string)
		case "hours":
			totalHours = r.Data.(string)
		case "message":
			userMessage = r.Data.(string)
		case "tool":
			lastTool = r.Data.(string)
		case "effort":
			effort = r.Data.(string)
		}
	}

	updateSession(input.SessionID)

	homeDir, _ := os.UserHomeDir()
	shortDir := dir
	if homeDir != "" {
		shortDir = strings.Replace(dir, homeDir, "~", 1)
	}

	// line 1: dir │ git │ model │ effort
	var line1 []string
	line1 = append(line1, fmt.Sprintf("%s%s%s", ColorCyan, shortDir, ColorReset))
	if gitBranch != "" {
		line1 = append(line1, fmt.Sprintf("\033[37m⚡ %s%s", gitBranch, ColorReset))
	}
	line1 = append(line1, fmt.Sprintf("%s%s%s", ColorPurple, input.Model.DisplayName, ColorReset))
	if effort != "" {
		line1 = append(line1, fmt.Sprintf("%s%s%s", ColorDim, effort, ColorReset))
	}
	fmt.Println(strings.Join(line1, SEP))

	// line 2: ctx │ rate limits │ time
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
			line2 = append(line2, fmt.Sprintf("%s5h: %d%%%s%s", color, remaining, reset, ColorReset))
		}
		if rl := input.RateLimits.SevenDay; rl != nil {
			remaining := 100 - int(rl.UsedPercentage)
			color := colorByRemaining(remaining)
			reset := formatResetTime(rl.ResetsAt)
			line2 = append(line2, fmt.Sprintf("%s7d: %d%%%s%s", color, remaining, reset, ColorReset))
		}
	}
	line2 = append(line2, fmt.Sprintf("%s%s%s", ColorDim, totalHours, ColorReset))
	fmt.Println(strings.Join(line2, SEP))

	// third line: special tools + last user message
	if lastTool != "" {
		fmt.Printf("%s│%s tool: %s\n", ColorDim, ColorReset, lastTool)
	}
	if userMessage != "" {
		fmt.Print(userMessage)
	}
}

// formatResetTime returns " (Xh Ym)" string until the reset time, or "" if unavailable/past.
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
	d := time.Until(t)
	if d <= 0 {
		return ""
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf(" (%dh%dm)", h, m)
	}
	return fmt.Sprintf(" (%dm)", m)
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

func calculateTotalHours() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "0m"
	}

	sessionsDir := filepath.Join(homeDir, ".claude", "session-tracker", "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return "0m"
	}

	var totalSeconds int64
	activeSessions := 0
	today := time.Now().Format("2006-01-02")
	currentTime := time.Now().Unix()

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sessionsDir, entry.Name()))
		if err != nil {
			continue
		}
		var session Session
		if err := json.Unmarshal(data, &session); err != nil {
			continue
		}
		if session.Date == today {
			totalSeconds += session.TotalSeconds
			if currentTime-session.LastHeartbeat < 600 {
				activeSessions++
			}
		}
	}

	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60

	var timeStr string
	if hours > 0 {
		timeStr = fmt.Sprintf("%dh", hours)
		if minutes > 0 {
			timeStr += fmt.Sprintf("%dm", minutes)
		}
	} else {
		timeStr = fmt.Sprintf("%dm", minutes)
	}

	if activeSessions > 1 {
		return fmt.Sprintf("%s [%d sessions]", timeStr, activeSessions)
	}
	return timeStr
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
	for _, tag := range []string{"<local-command-stdout>", "<command-name>", "<command-message>", "<command-args>"} {
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
	maxLines := 3
	lineWidth := 80
	lines := strings.Split(message, "\n")
	var result []string
	for i, line := range lines {
		if i >= maxLines {
			break
		}
		line = strings.TrimSpace(line)
		if len([]rune(line)) > lineWidth {
			runes := []rune(line)
			line = string(runes[:lineWidth-3]) + "..."
		}
		result = append(result, fmt.Sprintf("%s│%s %s", ColorDim, ColorReset, line))
	}
	if len(lines) > maxLines {
		result = append(result, fmt.Sprintf("%s│ +%d lines%s", ColorDim, len(lines)-maxLines, ColorReset))
	}
	if len(result) > 0 {
		return strings.Join(result, "\n") + "\n"
	}
	return ""
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
