//go:build linux

// inotify-go is a tiny, dependency-free stand-in for `inotifywait` covering
// the subset of behaviour the network-watcher needs: watch a path, print one
// line per matching event using a small `--format` vocabulary.
//
// It speaks the raw Linux inotify syscalls via the standard library, so the
// resulting static binary has no runtime dependencies — unlike inotify-tools,
// which ships as an apt package that UniFi OS wipes on firmware updates.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// defaultTimeFmt is used for %T when --timefmt is not given. inotifywait has no
// default, but network-watcher always passes one; this is a sensible fallback.
const defaultTimeFmt = "%Y-%m-%dT%H:%M:%S"

// headerSize is the on-the-wire size of an inotify_event header (up to and
// including Len), after which the variable-length name follows. We compute it
// from the field offset rather than unsafe.Sizeof(InotifyEvent{}), because that
// struct carries a trailing Name field plus padding (20 bytes) that is not part
// of the header on the wire (16 bytes).
const headerSize = uint32(unsafe.Offsetof(syscall.InotifyEvent{}.Len) + unsafe.Sizeof(syscall.InotifyEvent{}.Len))

// eventBits maps a lowercase event name (as accepted by `-e`) to its mask bit.
var eventBits = map[string]uint32{
	"access":        syscall.IN_ACCESS,
	"modify":        syscall.IN_MODIFY,
	"attrib":        syscall.IN_ATTRIB,
	"close_write":   syscall.IN_CLOSE_WRITE,
	"close_nowrite": syscall.IN_CLOSE_NOWRITE,
	"close":         syscall.IN_CLOSE_WRITE | syscall.IN_CLOSE_NOWRITE,
	"open":          syscall.IN_OPEN,
	"moved_from":    syscall.IN_MOVED_FROM,
	"moved_to":      syscall.IN_MOVED_TO,
	"move":          syscall.IN_MOVED_FROM | syscall.IN_MOVED_TO,
	"create":        syscall.IN_CREATE,
	"delete":        syscall.IN_DELETE,
	"delete_self":   syscall.IN_DELETE_SELF,
	"move_self":     syscall.IN_MOVE_SELF,
}

// bitNames maps a single mask bit to the uppercase name printed for %e,
// matching inotifywait's output so existing log parsing keeps working.
var bitNames = []struct {
	bit  uint32
	name string
}{
	{syscall.IN_ACCESS, "ACCESS"},
	{syscall.IN_MODIFY, "MODIFY"},
	{syscall.IN_ATTRIB, "ATTRIB"},
	{syscall.IN_CLOSE_WRITE, "CLOSE_WRITE"},
	{syscall.IN_CLOSE_NOWRITE, "CLOSE_NOWRITE"},
	{syscall.IN_OPEN, "OPEN"},
	{syscall.IN_MOVED_FROM, "MOVED_FROM"},
	{syscall.IN_MOVED_TO, "MOVED_TO"},
	{syscall.IN_CREATE, "CREATE"},
	{syscall.IN_DELETE, "DELETE"},
	{syscall.IN_DELETE_SELF, "DELETE_SELF"},
	{syscall.IN_MOVE_SELF, "MOVE_SELF"},
	// inotify-tools appends the aggregate CLOSE name whenever either close bit
	// is set (e.g. "CLOSE_WRITE,CLOSE"). It has no equivalent MOVE aggregate,
	// so MOVED_TO/MOVED_FROM are printed without a trailing "MOVE" — this
	// asymmetry is intentional, to stay byte-for-byte compatible with %e.
	{syscall.IN_CLOSE_WRITE | syscall.IN_CLOSE_NOWRITE, "CLOSE"},
}

// eventList collects `-e` values, supporting both repetition and CSV lists.
type eventList []string

func (e *eventList) String() string { return strings.Join(*e, ",") }

func (e *eventList) Set(v string) error {
	for p := range strings.SplitSeq(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*e = append(*e, p)
		}
	}
	return nil
}

func main() {
	var (
		monitor bool
		quiet   bool
		events  eventList
		format  string
		timefmt string
	)
	flag.BoolVar(&monitor, "m", false, "keep watching after the first event")
	flag.BoolVar(&monitor, "monitor", false, "keep watching after the first event")
	flag.BoolVar(&quiet, "q", false, "suppress the startup message on stderr")
	flag.BoolVar(&quiet, "quiet", false, "suppress the startup message on stderr")
	flag.Var(&events, "e", "event to watch; comma-separated and/or repeated")
	flag.Var(&events, "event", "event to watch; comma-separated and/or repeated")
	flag.StringVar(&format, "format", "%e %f", "output format (%T %e %f %w)")
	flag.StringVar(&timefmt, "timefmt", defaultTimeFmt, "strftime format used for %T")
	flag.Parse()

	paths := flag.Args()
	if len(paths) == 0 {
		fatal("no path to watch given")
	}

	mask, err := buildMask(events)
	if err != nil {
		fatal(err.Error())
	}

	w, err := NewWatcher()
	if err != nil {
		fatal("inotify_init: " + err.Error())
	}
	defer w.Close()

	// Track each watch descriptor back to the path it was added for, so %w
	// reports the right path when multiple paths are watched at once.
	wdToPath := make(map[int32]string, len(paths))
	for _, p := range paths {
		wd, err := w.Add(p, mask)
		if err != nil {
			fatal(fmt.Sprintf("add_watch %q: %v", p, err))
		}
		wdToPath[wd] = p
	}

	if !quiet {
		fmt.Fprintf(os.Stderr, "inotify-go: watching %s\n", strings.Join(paths, ", "))
	}

	out := bufio.NewWriter(os.Stdout)
	for {
		events, err := w.Read()
		if err != nil {
			fatal("read: " + err.Error())
		}
		for _, ev := range events {
			ts := formatTime(timefmt, time.Now())
			out.WriteString(render(format, ts, ev.Mask, ev.Name, wdToPath[ev.Wd]))
			out.WriteByte('\n')
			if !monitor {
				out.Flush()
				return
			}
		}
		out.Flush()
	}
}

// buildMask turns the requested event names into a single inotify mask.
func buildMask(events eventList) (uint32, error) {
	if len(events) == 0 {
		return 0, fmt.Errorf("no events given; use -e (e.g. -e modify,close_write)")
	}
	var mask uint32
	for _, name := range events {
		bit, ok := eventBits[strings.ToLower(name)]
		if !ok {
			return 0, fmt.Errorf("unknown event %q", name)
		}
		mask |= bit
	}
	return mask, nil
}

// render expands the format string for a single event. Replacement is a single
// left-to-right pass, so substituted values are never re-scanned for specifiers.
func render(format, timeStr string, mask uint32, name, watched string) string {
	r := strings.NewReplacer(
		"%T", timeStr,
		"%e", eventNames(mask),
		"%f", name,
		"%w", watched,
	)
	return r.Replace(format)
}

// formatTime renders t according to a strftime-style timefmt, matching the
// subset of conversions inotifywait's --timefmt accepts. Unknown specifiers are
// emitted verbatim (e.g. "%q" -> "%q"), as glibc does for many of them.
func formatTime(timefmt string, t time.Time) string {
	if timefmt == "" {
		timefmt = defaultTimeFmt
	}
	var b strings.Builder
	for i := 0; i < len(timefmt); i++ {
		if timefmt[i] != '%' || i+1 >= len(timefmt) {
			b.WriteByte(timefmt[i])
			continue
		}
		i++
		switch timefmt[i] {
		case 'Y':
			b.WriteString(t.Format("2006"))
		case 'y':
			b.WriteString(t.Format("06"))
		case 'm':
			b.WriteString(t.Format("01"))
		case 'd':
			b.WriteString(t.Format("02"))
		case 'e':
			fmt.Fprintf(&b, "%2d", t.Day())
		case 'H':
			b.WriteString(t.Format("15"))
		case 'I':
			b.WriteString(t.Format("03"))
		case 'M':
			b.WriteString(t.Format("04"))
		case 'S':
			b.WriteString(t.Format("05"))
		case 'p':
			b.WriteString(t.Format("PM"))
		case 'a':
			b.WriteString(t.Format("Mon"))
		case 'A':
			b.WriteString(t.Format("Monday"))
		case 'b':
			b.WriteString(t.Format("Jan"))
		case 'B':
			b.WriteString(t.Format("January"))
		case 'j':
			fmt.Fprintf(&b, "%03d", t.YearDay())
		case 'F':
			b.WriteString(t.Format("2006-01-02"))
		case 'T':
			b.WriteString(t.Format("15:04:05"))
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(timefmt[i])
		}
	}
	return b.String()
}

// eventNames renders a mask as a comma-separated list of uppercase names,
// appending ISDIR when the event concerns a directory (matching inotifywait).
func eventNames(mask uint32) string {
	var parts []string
	for _, b := range bitNames {
		if mask&b.bit != 0 {
			parts = append(parts, b.name)
		}
	}
	if mask&syscall.IN_ISDIR != 0 {
		parts = append(parts, "ISDIR")
	}
	return strings.Join(parts, ",")
}

// cString trims an inotify name field at its first NUL (names are NUL-padded).
func cString(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "inotify-go: "+msg)
	os.Exit(1)
}
