package logs

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// scannerBufSize is the bufio.Scanner buffer size used for all log file reads.
const scannerBufSize = 1024 * 1024

// listCacheTTL is the duration for which ListAccessible() results are cached.
const listCacheTTL = 30 * time.Second

// JournaldPrefix is the virtual path prefix for the systemd journal source.
const JournaldPrefix = "journald://"

// IsJournaldPath reports whether path refers to the journal source.
func IsJournaldPath(path string) bool {
	return path == JournaldPrefix || strings.HasPrefix(path, JournaldPrefix)
}

// journaldUnit extracts the optional unit filter from a journald:// path.
// "journald://" → "" (all units), "journald://nginx.service" → "nginx.service".
func journaldUnit(path string) string {
	return strings.TrimPrefix(path, JournaldPrefix)
}

// FileInfo holds metadata about a log file.
type FileInfo struct {
	Path         string    `json:"path"`
	SizeBytes    int64     `json:"size_bytes"`
	LastModified time.Time `json:"last_modified"`
	LineCount    int       `json:"line_count"`
	Readable     bool      `json:"readable"`
}

// ReadOptions controls how log lines are read.
type ReadOptions struct {
	Lines  int
	Tail   bool
	Offset int
	Since  *time.Time
	Until  *time.Time
}

// SearchOptions controls how log files are searched.
type SearchOptions struct {
	Pattern      string
	Since        *time.Time
	Until        *time.Time
	MaxResults   int
	ContextLines int
}

// Match represents a search match with optional surrounding context.
type Match struct {
	LineNumber int      `json:"line_number"`
	Line       string   `json:"line"`
	Before     []string `json:"before,omitempty"`
	After      []string `json:"after,omitempty"`
}

// Manager controls access to log files using whitelist/blacklist glob patterns.
type Manager struct {
	mu          sync.RWMutex
	whitelist   []string
	blacklist   []string
	journald    bool
	cachedFiles []FileInfo
	cacheTime   time.Time
}

// NewManager creates a Manager with the given whitelist and blacklist patterns.
// Blacklist takes precedence over whitelist. journald enables the journald:// virtual source.
func NewManager(whitelist, blacklist []string, journald bool) *Manager {
	return &Manager{
		whitelist: whitelist,
		blacklist: blacklist,
		journald:  journald,
	}
}

// Update replaces the access-control settings. Safe for concurrent use.
// It also invalidates the ListAccessible() cache so the next call rescans the filesystem.
func (m *Manager) Update(whitelist, blacklist []string, journald bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.whitelist = whitelist
	m.blacklist = blacklist
	m.journald = journald
	m.cacheTime = time.Time{}
}

// cleanPath canonicalizes a path to prevent directory traversal.
// It cleans ".." components and resolves symlinks when possible.
func cleanPath(path string) string {
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return resolved
	}
	return clean
}

// IsAllowed returns true if path is matched by the whitelist and NOT matched
// by the blacklist. Blacklist always takes precedence.
// journald:// paths are allowed when the journald source is enabled.
// The path is canonicalized before matching to prevent traversal attacks.
func (m *Manager) IsAllowed(path string) bool {
	m.mu.RLock()
	whitelist := m.whitelist
	blacklist := m.blacklist
	journald := m.journald
	m.mu.RUnlock()

	if IsJournaldPath(path) {
		return journald
	}

	path = cleanPath(path)

	// Check blacklist first — any match denies access.
	for _, pattern := range blacklist {
		if matchGlob(pattern, path) {
			return false
		}
	}

	// Must match at least one whitelist entry.
	for _, pattern := range whitelist {
		if matchGlob(pattern, path) {
			return true
		}
	}

	return false
}

// matchGlob matches path against a glob pattern. Supports both
// path/filepath.Match patterns and double-star (**) patterns via doublestar.
func matchGlob(pattern, path string) bool {
	// Use doublestar for ** patterns (correct prefix+suffix matching).
	if strings.Contains(pattern, "**") {
		matched, err := doublestar.Match(pattern, path)
		if err != nil {
			return false
		}
		return matched
	}

	// Try glob expansion (handles * within directory names).
	matched, err := filepath.Match(pattern, path)
	if err != nil {
		return false
	}
	if matched {
		return true
	}

	// Also try matching the expanded glob against the path.
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return false
	}
	absPath, _ := filepath.Abs(path)
	for _, m := range matches {
		absM, _ := filepath.Abs(m)
		if absM == absPath {
			return true
		}
	}

	return false
}

// checkACL checks whitelist/blacklist access against the given slices.
// Caller must hold m.mu to ensure the slices are stable.
func checkACL(path string, whitelist, blacklist []string) bool {
	for _, pattern := range blacklist {
		if matchGlob(pattern, path) {
			return false
		}
	}
	for _, pattern := range whitelist {
		if matchGlob(pattern, path) {
			return true
		}
	}
	return false
}

// ListAccessible returns FileInfo for all accessible files matching the whitelist.
// Directories matched by glob patterns are silently skipped.
// If journald is enabled, a virtual journald:// entry is prepended.
// Results are cached for listCacheTTL; the cache is invalidated by Update().
func (m *Manager) ListAccessible() ([]FileInfo, error) {
	// Fast path: return cached results while they are still valid.
	m.mu.RLock()
	if !m.cacheTime.IsZero() && time.Since(m.cacheTime) < listCacheTTL {
		cached := make([]FileInfo, len(m.cachedFiles))
		copy(cached, m.cachedFiles)
		m.mu.RUnlock()
		return cached, nil
	}
	m.mu.RUnlock()

	// Slow path: perform the filesystem scan.
	m.mu.Lock()
	defer m.mu.Unlock()

	// Re-check under write lock — another goroutine may have populated the cache.
	if !m.cacheTime.IsZero() && time.Since(m.cacheTime) < listCacheTTL {
		cached := make([]FileInfo, len(m.cachedFiles))
		copy(cached, m.cachedFiles)
		return cached, nil
	}

	var results []FileInfo

	if m.journald {
		results = append(results, m.JournaldInfo())
	}

	seen := map[string]bool{}

	for _, pattern := range m.whitelist {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, path := range matches {
			if seen[path] {
				continue
			}
			seen[path] = true

			// Skip directories — globs like /var/log/* match them too.
			if st, err := os.Stat(path); err != nil || st.IsDir() {
				continue
			}

			// IsAllowed acquires its own read lock; check inline to avoid deadlock.
			path = cleanPath(path)
			if !checkACL(path, m.whitelist, m.blacklist) {
				continue
			}

			info, err := m.FileInfo(path)
			if err != nil {
				results = append(results, FileInfo{Path: path, Readable: false})
				continue
			}
			results = append(results, info)
		}
	}

	// Store results in cache.
	m.cachedFiles = make([]FileInfo, len(results))
	copy(m.cachedFiles, results)
	m.cacheTime = time.Now()

	return results, nil
}

// JournaldInfo returns a virtual FileInfo representing the systemd journal.
func (m *Manager) JournaldInfo() FileInfo {
	return FileInfo{
		Path:     JournaldPrefix,
		Readable: true,
	}
}

// FileInfo returns metadata for a single file.
func (m *Manager) FileInfo(path string) (FileInfo, error) {
	path = cleanPath(path)
	stat, err := os.Stat(path)
	if err != nil {
		return FileInfo{}, fmt.Errorf("stat %q: %w", path, err)
	}
	if stat.IsDir() {
		return FileInfo{}, fmt.Errorf("%q is a directory", path)
	}

	fi := FileInfo{
		Path:         path,
		SizeBytes:    stat.Size(),
		LastModified: stat.ModTime(),
		Readable:     false,
	}

	f, err := os.Open(path)
	if err != nil {
		return fi, nil
	}
	defer f.Close()
	fi.Readable = true

	// Count lines.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, scannerBufSize), scannerBufSize)
	count := 0
	for scanner.Scan() {
		count++
	}
	fi.LineCount = count

	return fi, nil
}

// ReadFile reads lines from the given file according to opts.
func (m *Manager) ReadFile(ctx context.Context, path string, opts ReadOptions) ([]string, error) {
	path = cleanPath(path)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %q: %w", path, err)
	}
	defer f.Close()

	n := opts.Lines
	if n <= 0 {
		n = 100
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, scannerBufSize), scannerBufSize)

	if opts.Tail {
		// Ring buffer of size n: keep only the last n time-filtered lines.
		ring := make([]string, n)
		pos := 0
		count := 0
		for scanner.Scan() {
			line := scanner.Text()
			if opts.Since != nil || opts.Until != nil {
				ts, ok := parseLineTimestamp(line)
				if ok {
					if opts.Since != nil && ts.Before(*opts.Since) {
						continue
					}
					if opts.Until != nil && ts.After(*opts.Until) {
						continue
					}
				}
			}
			ring[pos%n] = line
			pos++
			count++
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("reading %q: %w", path, err)
		}
		if count <= n {
			return ring[:count], nil
		}
		out := make([]string, n)
		start := pos % n
		copy(out, ring[start:])
		copy(out[n-start:], ring[:start])
		return out, nil
	}

	// Head mode: stop after offset + n lines.
	fetch := n + opts.Offset
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		if opts.Since != nil || opts.Until != nil {
			ts, ok := parseLineTimestamp(line)
			if ok {
				if opts.Since != nil && ts.Before(*opts.Since) {
					continue
				}
				if opts.Until != nil && ts.After(*opts.Until) {
					continue
				}
			}
		}
		lines = append(lines, line)
		if len(lines) >= fetch {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %q: %w", path, err)
	}

	if opts.Offset > 0 {
		if opts.Offset >= len(lines) {
			return []string{}, nil
		}
		lines = lines[opts.Offset:]
	}
	if len(lines) > n {
		lines = lines[:n]
	}
	return lines, nil
}

// SearchFile searches the file for lines matching opts.Pattern.
func (m *Manager) SearchFile(ctx context.Context, path string, opts SearchOptions) ([]Match, error) {
	path = cleanPath(path)
	if opts.Pattern == "" {
		return nil, fmt.Errorf("search pattern must not be empty")
	}

	re, err := regexp.Compile(opts.Pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern %q: %w", opts.Pattern, err)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %q: %w", path, err)
	}
	defer f.Close()

	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults = 200
	}

	k := opts.ContextLines
	// Rolling deque for before-context: fixed capacity k.
	var beforeDeque []string

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, scannerBufSize), scannerBufSize)

	var results []Match
	lineNum := 0
	afterPending := 0

	for scanner.Scan() {
		line := scanner.Text()
		lineNum++

		// Time filter.
		if opts.Since != nil || opts.Until != nil {
			ts, ok := parseLineTimestamp(line)
			if ok {
				if opts.Since != nil && ts.Before(*opts.Since) {
					if k > 0 {
						if len(beforeDeque) >= k {
							beforeDeque = beforeDeque[1:]
						}
						beforeDeque = append(beforeDeque, line)
					}
					continue
				}
				if opts.Until != nil && ts.After(*opts.Until) {
					if k > 0 {
						if len(beforeDeque) >= k {
							beforeDeque = beforeDeque[1:]
						}
						beforeDeque = append(beforeDeque, line)
					}
					continue
				}
			}
		}

		// Feed after-context of the last open match.
		if afterPending > 0 {
			results[len(results)-1].After = append(results[len(results)-1].After, line)
			afterPending--
		}

		if re.MatchString(line) {
			match := Match{LineNumber: lineNum, Line: line}
			if k > 0 && len(beforeDeque) > 0 {
				match.Before = make([]string, len(beforeDeque))
				copy(match.Before, beforeDeque)
			}
			results = append(results, match)

			// Extend after-window; overlapping windows are merged.
			if k > 0 {
				if afterPending < k {
					afterPending = k
				}
			}

			if len(results) >= maxResults && afterPending == 0 {
				break
			}
		}

		// Advance rolling before-deque.
		if k > 0 {
			if len(beforeDeque) >= k {
				beforeDeque = beforeDeque[1:]
			}
			beforeDeque = append(beforeDeque, line)
		}

		// Stop once last result's after-context is complete.
		if len(results) >= maxResults && afterPending == 0 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %q: %w", path, err)
	}

	return results, nil
}

// filterByTime returns only lines whose timestamp prefix falls within [since, until].
// Lines that cannot be parsed are included (conservative approach).
func filterByTime(lines []string, since, until *time.Time) []string {
	var out []string
	for _, line := range lines {
		ts, ok := parseLineTimestamp(line)
		if !ok {
			// Cannot parse timestamp — include the line.
			out = append(out, line)
			continue
		}
		if since != nil && ts.Before(*since) {
			continue
		}
		if until != nil && ts.After(*until) {
			continue
		}
		out = append(out, line)
	}
	return out
}

// timestampFormats lists timestamp prefixes to try when parsing log lines.
var timestampFormats = []string{
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02T15:04:05",
	"Jan 02 15:04:05",
	"Jan  2 15:04:05",
}

// parseLineTimestamp tries to parse a timestamp from the beginning of a log line.
func parseLineTimestamp(line string) (time.Time, bool) {
	if len(line) < 10 {
		return time.Time{}, false
	}

	// Try each format against progressively longer prefixes.
	for _, format := range timestampFormats {
		flen := len(format)
		if flen > len(line) {
			continue
		}
		prefix := line[:flen]
		t, err := time.ParseInLocation(format, prefix, time.Local)
		if err == nil {
			// Restore year for formats that omit it (e.g. syslog).
			if t.Year() == 0 {
				t = t.AddDate(time.Now().Year(), 0, 0)
			}
			return t, true
		}
	}

	return time.Time{}, false
}

// ReadJournald reads lines from the systemd journal.
// path may be "journald://" (all units) or "journald://unit.service".
func (m *Manager) ReadJournald(ctx context.Context, path string, opts ReadOptions) ([]string, error) {
	unit := journaldUnit(path)
	n := opts.Lines
	if n <= 0 {
		n = 100
	}

	args := buildJournalArgs(unit, opts.Since, opts.Until)

	if opts.Tail {
		// journalctl -n N returns the last N entries directly.
		fetch := n + opts.Offset
		args = append(args, "-n", strconv.Itoa(fetch))
		out, err := exec.CommandContext(ctx, "journalctl", args...).Output()
		if err != nil {
			return nil, fmt.Errorf("journalctl: %w", err)
		}
		lines := splitOutputLines(out)
		if opts.Offset > 0 {
			if opts.Offset >= len(lines) {
				return []string{}, nil
			}
			lines = lines[opts.Offset:]
		}
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
		return lines, nil
	}

	// For head-style reading, stream and stop early.
	fetch := n + opts.Offset
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("journalctl pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("journalctl start: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	var lines []string
	for scanner.Scan() && len(lines) < fetch {
		lines = append(lines, scanner.Text())
	}
	// Drain and wait; ignore kill errors.
	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	if opts.Offset > 0 {
		if opts.Offset >= len(lines) {
			return []string{}, nil
		}
		lines = lines[opts.Offset:]
	}
	if len(lines) > n {
		lines = lines[:n]
	}
	return lines, nil
}

// SearchJournald searches the systemd journal for lines matching opts.Pattern.
func (m *Manager) SearchJournald(ctx context.Context, path string, opts SearchOptions) ([]Match, error) {
	unit := journaldUnit(path)

	re, err := regexp.Compile(opts.Pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern %q: %w", opts.Pattern, err)
	}

	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults = 200
	}

	args := buildJournalArgs(unit, opts.Since, opts.Until)
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("journalctl pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("journalctl start: %w", err)
	}

	k := opts.ContextLines
	var beforeDeque []string
	var results []Match
	lineNum := 0
	afterPending := 0

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		lineNum++

		if afterPending > 0 {
			results[len(results)-1].After = append(results[len(results)-1].After, line)
			afterPending--
		}

		if re.MatchString(line) {
			match := Match{LineNumber: lineNum, Line: line}
			if k > 0 && len(beforeDeque) > 0 {
				match.Before = make([]string, len(beforeDeque))
				copy(match.Before, beforeDeque)
			}
			results = append(results, match)

			if k > 0 {
				if afterPending < k {
					afterPending = k
				}
			}

			if len(results) >= maxResults && afterPending == 0 {
				break
			}
		}

		if k > 0 {
			if len(beforeDeque) >= k {
				beforeDeque = beforeDeque[1:]
			}
			beforeDeque = append(beforeDeque, line)
		}

		if len(results) >= maxResults && afterPending == 0 {
			break
		}
	}

	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	return results, nil
}

// buildJournalArgs returns base journalctl args for the given unit and time window.
func buildJournalArgs(unit string, since, until *time.Time) []string {
	args := []string{"--no-pager", "--output=short-iso"}
	if unit != "" {
		args = append(args, "--unit="+unit)
	}
	if since != nil {
		args = append(args, "--since="+since.Format("2006-01-02 15:04:05"))
	}
	if until != nil {
		args = append(args, "--until="+until.Format("2006-01-02 15:04:05"))
	}
	return args
}

// splitOutputLines splits journalctl byte output into lines, trimming the trailing newline.
func splitOutputLines(out []byte) []string {
	s := strings.TrimRight(string(out), "\n")
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\n")
}

// ParseTimeOrDuration parses a time specification that is either an RFC3339
// timestamp or a relative duration string (e.g. "1h", "30m").
func ParseTimeOrDuration(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time string")
	}

	// Try RFC3339 first.
	t, err := time.Parse(time.RFC3339, s)
	if err == nil {
		return t, nil
	}

	// Try relative duration.
	d, err2 := time.ParseDuration(s)
	if err2 == nil {
		return time.Now().Add(-d), nil
	}

	return time.Time{}, fmt.Errorf("cannot parse time %q: not an RFC3339 timestamp nor a duration", s)
}
