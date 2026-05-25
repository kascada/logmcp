package logs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- matchGlob ---

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// filepath.Match cases
		{"/var/log/*", "/var/log/syslog", true},
		{"/var/log/*", "/var/log/nginx/access.log", false}, // * does not cross /
		{"/var/log/*.log", "/var/log/app.log", true},
		{"/var/log/*.log", "/var/log/app.txt", false},
		{"/etc/app.conf", "/etc/app.conf", true},
		// ** cases
		{"/var/log/**", "/var/log/syslog", true},
		{"/var/log/**", "/var/log/nginx/access.log", true},
		{"/var/log/**", "/etc/passwd", false},
		{"**", "/anything/goes/here", true},
	}
	for _, tc := range tests {
		got := matchGlob(tc.pattern, tc.path)
		if got != tc.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

// --- IsAllowed ---

func TestIsAllowed_BlacklistPrecedence(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.log")
	os.WriteFile(file, nil, 0644)

	m := NewManager([]string{dir + "/*"}, []string{dir + "/*.log"}, false)
	if m.IsAllowed(file) {
		t.Errorf("blacklisted file should not be allowed")
	}
}

func TestIsAllowed_Traversal(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.log")
	os.WriteFile(file, nil, 0644)

	m := NewManager([]string{dir + "/*.log"}, nil, false)

	if !m.IsAllowed(file) {
		t.Errorf("direct path should be allowed")
	}

	// Traversal: resolve lands outside dir
	traversal := filepath.Join(dir, "..", filepath.Base(dir)+"_evil.log")
	if m.IsAllowed(traversal) {
		t.Errorf("traversal path should not be allowed: %s", traversal)
	}
}

func TestIsAllowed_Journald(t *testing.T) {
	m := NewManager(nil, nil, true)
	if !m.IsAllowed(JournaldPrefix) {
		t.Error("journald:// should be allowed when journald=true")
	}

	m2 := NewManager(nil, nil, false)
	if m2.IsAllowed(JournaldPrefix) {
		t.Error("journald:// should not be allowed when journald=false")
	}
}

// --- ParseTimeOrDuration ---

func TestParseTimeOrDuration(t *testing.T) {
	t.Run("RFC3339", func(t *testing.T) {
		got, err := ParseTimeOrDuration("2024-03-15T10:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
		if got.Year() != 2024 || got.Month() != 3 || got.Day() != 15 {
			t.Errorf("unexpected time: %v", got)
		}
	})

	t.Run("duration", func(t *testing.T) {
		before := time.Now()
		got, err := ParseTimeOrDuration("1h")
		after := time.Now()
		if err != nil {
			t.Fatal(err)
		}
		want := before.Add(-time.Hour)
		if got.Before(want.Add(-2*time.Second)) || got.After(after) {
			t.Errorf("duration result %v not within expected range", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		if _, err := ParseTimeOrDuration(""); err == nil {
			t.Error("expected error for empty string")
		}
	})

	t.Run("garbage", func(t *testing.T) {
		if _, err := ParseTimeOrDuration("not-a-time"); err == nil {
			t.Error("expected error for invalid input")
		}
	})
}

// --- parseLineTimestamp ---

func TestParseLineTimestamp(t *testing.T) {
	t.Run("ISO datetime", func(t *testing.T) {
		ts, ok := parseLineTimestamp("2024-03-15 14:22:30 some log message")
		if !ok {
			t.Fatal("expected ok=true")
		}
		if ts.Year() != 2024 || ts.Month() != 3 || ts.Day() != 15 {
			t.Errorf("unexpected: %v", ts)
		}
	})

	t.Run("ISO T separator", func(t *testing.T) {
		ts, ok := parseLineTimestamp("2024-03-15T14:22:30 some log message")
		if !ok {
			t.Fatal("expected ok=true")
		}
		if ts.Year() != 2024 {
			t.Errorf("unexpected year: %v", ts)
		}
	})

	t.Run("syslog format restores current year", func(t *testing.T) {
		ts, ok := parseLineTimestamp("Jan 02 15:04:05 hostname app: message")
		if !ok {
			t.Fatal("expected ok=true for syslog format")
		}
		if ts.Year() != time.Now().Year() {
			t.Errorf("year = %d, want current year %d", ts.Year(), time.Now().Year())
		}
	})

	t.Run("too short", func(t *testing.T) {
		if _, ok := parseLineTimestamp("short"); ok {
			t.Error("expected ok=false for short line")
		}
	})

	t.Run("no recognizable timestamp", func(t *testing.T) {
		if _, ok := parseLineTimestamp("hello world no timestamp here"); ok {
			t.Error("expected ok=false for line with no timestamp")
		}
	})
}

// --- filterByTime ---

func TestFilterByTime(t *testing.T) {
	since := time.Date(2024, 1, 15, 10, 0, 0, 0, time.Local)
	until := time.Date(2024, 1, 15, 11, 0, 0, 0, time.Local)

	lines := []string{
		"2024-01-15 09:59:59 too early",
		"2024-01-15 10:30:00 in range",
		"2024-01-15 11:00:01 too late",
		"no timestamp here — must be included",
	}

	got := filterByTime(lines, &since, &until)

	want := []string{
		"2024-01-15 10:30:00 in range",
		"no timestamp here — must be included",
	}

	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
