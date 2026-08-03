//go:build linux

package main

import (
	"syscall"
	"testing"
	"time"
)

func TestEventListSet(t *testing.T) {
	var e eventList
	// CSV in one call, plus a repeated flag, plus surrounding whitespace and
	// empty fields that should be ignored.
	if err := e.Set("modify, close_write"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := e.Set("create,,"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got := []string(e)
	want := []string{"modify", "close_write", "create"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestBuildMask(t *testing.T) {
	t.Run("combines bits", func(t *testing.T) {
		m, err := buildMask(eventList{"modify", "close_write", "moved_to", "create"})
		if err != nil {
			t.Fatal(err)
		}
		want := uint32(syscall.IN_MODIFY | syscall.IN_CLOSE_WRITE | syscall.IN_MOVED_TO | syscall.IN_CREATE)
		if m != want {
			t.Fatalf("mask = %#x, want %#x", m, want)
		}
	})

	t.Run("case insensitive and aggregate names", func(t *testing.T) {
		m, err := buildMask(eventList{"CLOSE", "Move"})
		if err != nil {
			t.Fatal(err)
		}
		want := uint32(syscall.IN_CLOSE_WRITE | syscall.IN_CLOSE_NOWRITE | syscall.IN_MOVED_FROM | syscall.IN_MOVED_TO)
		if m != want {
			t.Fatalf("mask = %#x, want %#x", m, want)
		}
	})

	t.Run("unknown event errors", func(t *testing.T) {
		if _, err := buildMask(eventList{"modify", "bogus"}); err == nil {
			t.Fatal("expected error for unknown event")
		}
	})

	t.Run("empty errors", func(t *testing.T) {
		if _, err := buildMask(nil); err == nil {
			t.Fatal("expected error for empty event list")
		}
	})
}

func TestEventNames(t *testing.T) {
	tests := []struct {
		name string
		mask uint32
		want string
	}{
		{"single", syscall.IN_MODIFY, "MODIFY"},
		{"moved_to has no MOVE aggregate", syscall.IN_MOVED_TO, "MOVED_TO"},
		{"isdir suffix", syscall.IN_CREATE | syscall.IN_ISDIR, "CREATE,ISDIR"},
		{"close_write gets CLOSE aggregate", syscall.IN_CLOSE_WRITE, "CLOSE_WRITE,CLOSE"},
		{"close_nowrite gets CLOSE aggregate", syscall.IN_CLOSE_NOWRITE, "CLOSE_NOWRITE,CLOSE"},
		{"multiple ordered", syscall.IN_CLOSE_WRITE | syscall.IN_MODIFY, "MODIFY,CLOSE_WRITE,CLOSE"},
		{"none", 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := eventNames(tc.mask); got != tc.want {
				t.Fatalf("eventNames(%#x) = %q, want %q", tc.mask, got, tc.want)
			}
		})
	}
}

func TestRender(t *testing.T) {
	got := render("%e %f", "", syscall.IN_CLOSE_WRITE, "state", "/data")
	if want := "CLOSE_WRITE,CLOSE state"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	got = render("%w/%f -> %e", "", syscall.IN_MOVED_TO, "state", "/data/dir")
	if want := "/data/dir/state -> MOVED_TO"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// %T is substituted from the precomputed timestamp; the watcher relies on
	// this being the first whitespace-delimited field.
	got = render("%T %e %f", "2026-06-10T12:34:56", syscall.IN_MODIFY, "state", "/data")
	if want := "2026-06-10T12:34:56 MODIFY state"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFormatTime(t *testing.T) {
	ref := time.Date(2026, 6, 10, 9, 5, 3, 0, time.UTC) // a Wednesday
	tests := []struct {
		timefmt string
		want    string
	}{
		{"%Y-%m-%dT%H:%M:%S", "2026-06-10T09:05:03"}, // network-watcher's timefmt
		{"", "2026-06-10T09:05:03"},                  // empty falls back to default
		{"%F %T", "2026-06-10 09:05:03"},
		{"%y/%m/%d", "26/06/10"},
		{"%a %b %j", "Wed Jun 161"},
		{"100%% done", "100% done"},   // literal percent
		{"%q", "%q"},                  // unknown specifier passed through
		{"trailing %", "trailing %"},  // dangling percent
	}
	for _, tc := range tests {
		if got := formatTime(tc.timefmt, ref); got != tc.want {
			t.Fatalf("formatTime(%q) = %q, want %q", tc.timefmt, got, tc.want)
		}
	}
}

func TestCString(t *testing.T) {
	tests := []struct {
		in   []byte
		want string
	}{
		{[]byte("state\x00\x00\x00"), "state"}, // NUL-padded, as inotify delivers
		{[]byte("state"), "state"},             // no terminator
		{[]byte("\x00"), ""},                   // empty
		{[]byte{}, ""},                         // zero length
	}
	for _, tc := range tests {
		if got := cString(tc.in); got != tc.want {
			t.Fatalf("cString(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
