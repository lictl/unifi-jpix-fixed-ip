//go:build linux

package main

import (
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"testing"
	"time"
)

// TestWatcherDirEvents drives a real inotify instance against a temp directory
// and asserts the events the network-watcher relies on, including the atomic
// write-temp + rename case (MOVED_TO) that a watch held on the file path itself
// would miss.
func TestWatcherDirEvents(t *testing.T) {
	dir := t.TempDir()

	w, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	mask := uint32(syscall.IN_MODIFY | syscall.IN_CLOSE_WRITE | syscall.IN_MOVED_TO | syscall.IN_CREATE)
	if _, err := w.Add(dir, mask); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Collect events on a background goroutine. The watch is already registered,
	// so the kernel queues events regardless of when Read runs. Closing the
	// watcher at test end unblocks the final Read and ends the goroutine.
	got := make(map[string]bool)
	done := make(chan struct{})
	collected := make(chan string, 128)
	go func() {
		defer close(done)
		for {
			events, err := w.Read()
			if err != nil {
				return
			}
			for _, e := range events {
				collected <- eventNames(e.Mask) + " " + e.Name
			}
		}
	}()

	// 1. create + write a file -> CREATE, MODIFY, CLOSE_WRITE
	statePath := filepath.Join(dir, "state")
	if err := os.WriteFile(statePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 2. atomic replace via rename -> MOVED_TO (the inode-swap case)
	tmpPath := filepath.Join(dir, "state.tmp")
	if err := os.WriteFile(tmpPath, []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmpPath, statePath); err != nil {
		t.Fatal(err)
	}
	// 3. mkdir -> CREATE,ISDIR
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"CREATE state",
		"MODIFY state",
		"CLOSE_WRITE,CLOSE state", // inotify-tools appends the CLOSE aggregate
		"MOVED_TO state",
		"CREATE,ISDIR sub",
	}

	deadline := time.After(5 * time.Second)
	for !hasAll(got, want) {
		select {
		case line := <-collected:
			got[line] = true
		case <-deadline:
			t.Fatalf("timed out; missing %v (got %v)", missing(got, want), keys(got))
		}
	}
}

func hasAll(got map[string]bool, want []string) bool {
	for _, w := range want {
		if !got[w] {
			return false
		}
	}
	return true
}

func missing(got map[string]bool, want []string) []string {
	var m []string
	for _, w := range want {
		if !got[w] {
			m = append(m, w)
		}
	}
	return m
}

func keys(m map[string]bool) []string {
	var k []string
	for s := range m {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}
