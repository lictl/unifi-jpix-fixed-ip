//go:build linux

package main

import (
	"syscall"
	"unsafe"
)

// Event is a single decoded inotify event.
type Event struct {
	Wd   int32  // watch descriptor the event came from
	Mask uint32 // IN_* bits describing what happened
	Name string // file name within a watched directory ("" for file watches)
}

// Watcher wraps an inotify instance and its read buffer.
type Watcher struct {
	fd  int
	buf []byte
}

// NewWatcher creates an inotify instance.
func NewWatcher() (*Watcher, error) {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC)
	if err != nil {
		return nil, err
	}
	return &Watcher{fd: fd, buf: make([]byte, 64*1024)}, nil
}

// Add registers a watch on path for the given event mask and returns its
// watch descriptor.
func (w *Watcher) Add(path string, mask uint32) (int32, error) {
	wd, err := syscall.InotifyAddWatch(w.fd, path, mask)
	if err != nil {
		return 0, err
	}
	return int32(wd), nil
}

// Read blocks until at least one event is available, then returns all events
// from that read. IN_IGNORED events (a watch being auto-removed) are dropped.
// The returned slice may be empty if a read contained only dropped events.
func (w *Watcher) Read() ([]Event, error) {
	for {
		n, err := syscall.Read(w.fd, w.buf)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return nil, err
		}
		return parseEvents(w.buf[:n]), nil
	}
}

// Close releases the inotify instance. A blocked Read returns an error.
func (w *Watcher) Close() error {
	return syscall.Close(w.fd)
}

// parseEvents decodes a raw inotify read buffer into events. It is the inverse
// of how the kernel packs back-to-back inotify_event records and is kept
// separate from the syscalls so it can be exercised directly in tests.
func parseEvents(buf []byte) []Event {
	var events []Event
	var off uint32
	for off+headerSize <= uint32(len(buf)) {
		raw := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[off]))
		nameStart := off + headerSize
		nameEnd := nameStart + raw.Len
		if nameEnd > uint32(len(buf)) { // truncated record; stop
			break
		}
		name := cString(buf[nameStart:nameEnd])
		off = nameEnd

		// IN_IGNORED fires when a watch is auto-removed (e.g. the watched file
		// was deleted); it carries no useful event for callers.
		if raw.Mask&syscall.IN_IGNORED != 0 {
			continue
		}
		events = append(events, Event{Wd: raw.Wd, Mask: raw.Mask, Name: name})
	}
	return events
}
