package ui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"

	"github.com/hunchom/bodega/internal/ui/theme"
)

// Live renders install/upgrade progress as an in-place multi-line block —
// one line per in-flight package (spinner, phase, byte bar) — with
// persistent log lines (✓/✗, restyled brew output) printed above it.
// Safe for concurrent use; meant for a TTY. Non-TTY callers should keep
// using plain line output.
type Live struct {
	mu       sync.Mutex
	out      io.Writer
	st       map[string]*livePkg
	order    []string
	drawn    int // lines currently occupied by the block
	frame    int
	width    int          // terminal columns; block lines are clamped to it
	buf      bytes.Buffer // partial brew passthrough line
	stop     chan struct{}
	stopOnce sync.Once
}

type livePkg struct {
	name, version, phase string
	current, total       int64
}

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func NewLive(out io.Writer) *Live {
	l := &Live{out: out, st: map[string]*livePkg{}, stop: make(chan struct{}), width: 80}
	l.refreshWidth()
	go l.tick()
	return l
}

// refreshWidth re-reads the terminal width. The clear() escape moves the
// cursor by PHYSICAL rows, so block lines must never wrap — every rendered
// line is clamped to this width.
func (l *Live) refreshWidth() {
	f, ok := l.out.(*os.File)
	if !ok {
		return
	}
	if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 10 {
		l.width = w
	}
}

// tick keeps spinners moving during long silent phases (relocate on llvm
// takes tens of seconds with zero events).
func (l *Live) tick() {
	t := time.NewTicker(120 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			l.mu.Lock()
			if len(l.order) > 0 {
				l.frame++
				l.refreshWidth()
				l.redraw()
			}
			l.mu.Unlock()
		}
	}
}

// Event consumes one structured install event.
func (l *Live) Event(phase, pkg, version string, current, total int64, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch phase {
	case "download", "extract", "relocate", "link":
		s := l.st[pkg]
		if s == nil {
			s = &livePkg{name: pkg, version: version}
			l.st[pkg] = s
			l.order = append(l.order, pkg)
		}
		s.phase = phase
		if version != "" {
			s.version = version
		}
		if phase == "download" {
			if current > 0 {
				s.current = current
			}
			s.total = total
		}
		l.redraw()
	case "installed":
		l.finish(pkg, theme.OK.Render("✓")+" "+theme.Bold.Render(pkg)+" "+theme.Muted.Render(version))
	case "failed":
		l.finish(pkg, theme.Err.Render("✗")+" "+msg)
	case "resolve", "done":
		if msg != "" {
			l.println(theme.Muted.Render(msg))
		}
	default:
		if msg != "" {
			l.println(msg)
		}
	}
}

// finish prints a persistent terminal line for pkg and drops it from the
// in-flight block.
func (l *Live) finish(pkg, line string) {
	if _, ok := l.st[pkg]; ok {
		delete(l.st, pkg)
		for i, n := range l.order {
			if n == pkg {
				l.order = append(l.order[:i], l.order[i+1:]...)
				break
			}
		}
	}
	l.println(line)
}

// Println prints a persistent line above the in-flight block.
func (l *Live) Println(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.println(line)
}

func (l *Live) println(line string) {
	l.clear()
	fmt.Fprintln(l.out, line)
	l.redraw()
}

// Write accepts raw brew subprocess output, restyling complete lines into
// persistent log lines. Implements io.Writer so a StreamPW can point here.
func (l *Live) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.Write(b)
	for {
		line, err := l.buf.ReadString('\n')
		if err != nil {
			// Partial line — keep for the next Write.
			l.buf.Reset()
			l.buf.WriteString(line)
			break
		}
		l.clear()
		fmt.Fprintln(l.out, StyleBrewLine(strings.TrimRight(line, "\r\n")))
	}
	l.redraw()
	return len(b), nil
}

// Close flushes any partial passthrough line and erases the block.
func (l *Live) Close() {
	l.stopOnce.Do(func() { close(l.stop) })
	l.mu.Lock()
	defer l.mu.Unlock()
	if rest := strings.TrimRight(l.buf.String(), "\r\n"); rest != "" {
		l.clear()
		fmt.Fprintln(l.out, StyleBrewLine(rest))
	}
	l.buf.Reset()
	l.clear()
	l.drawn = 0
}

// clear erases the in-flight block (cursor to block start, wipe down).
func (l *Live) clear() {
	if l.drawn == 0 {
		return
	}
	fmt.Fprintf(l.out, "\x1b[%dA\x1b[J", l.drawn)
	l.drawn = 0
}

// redraw repaints the in-flight block in place. Lines are clamped to the
// terminal width: a wrapped line occupies 2+ physical rows while drawn
// counts 1, which would desync clear() and smear stale rows down the screen.
func (l *Live) redraw() {
	l.clear()
	for _, name := range l.order {
		s := l.st[name]
		fmt.Fprintln(l.out, ansi.Truncate(l.renderLine(s), l.width-1, "…"))
	}
	l.drawn = len(l.order)
}

func (l *Live) renderLine(s *livePkg) string {
	spin := theme.Accent.Render(spinFrames[l.frame%len(spinFrames)])
	head := fmt.Sprintf("%s %s %s", spin, theme.Bold.Render(s.name), theme.Muted.Render(s.version))
	switch s.phase {
	case "download":
		if s.total > 0 {
			return fmt.Sprintf("%s  %s %s %3.0f%%  %s", head,
				theme.Muted.Render("download"),
				renderBar(s.current, s.total, 20),
				100*float64(s.current)/float64(s.total),
				theme.Muted.Render(HumanBytes(s.current)+" / "+HumanBytes(s.total)))
		}
		return fmt.Sprintf("%s  %s %s", head, theme.Muted.Render("download"), theme.Muted.Render(HumanBytes(s.current)))
	default:
		return fmt.Sprintf("%s  %s", head, theme.Muted.Render(s.phase))
	}
}

// renderBar draws a width-cell progress bar.
func renderBar(cur, total int64, width int) string {
	if total <= 0 || cur < 0 {
		return ""
	}
	filled := min(int(float64(width)*float64(cur)/float64(total)), width)
	return theme.Accent.Render(strings.Repeat("█", filled)) + theme.Muted.Render(strings.Repeat("░", width-filled))
}

// brewNoise are line prefixes worth keeping but de-emphasizing — brew's
// blow-by-blow file accounting drowns the lines that matter.
var brewNoise = []string{
	"Purging", "Unlinking", "Linking", "Removing", "Backing up", "Moving",
	"Trashing", "Cleaning", "Already downloaded", "Outdated", "Running",
	"Changing ownership", "==> Pouring", "Pouring",
}

// StyleBrewLine restyles one line of raw brew output into yum's visual
// language. Unrecognized lines pass through untouched.
func StyleBrewLine(l string) string {
	switch {
	case l == "":
		return l
	case strings.HasPrefix(l, "==> Upgrading "), strings.HasPrefix(l, "==> Installing "),
		strings.HasPrefix(l, "==> Fetching "), strings.HasPrefix(l, "==> Uninstalling "):
		return theme.Accent.Render("•") + " " + theme.Bold.Render(strings.TrimPrefix(l, "==> "))
	case strings.HasPrefix(l, "==> Caveats"):
		return theme.Muted.Render("· caveats")
	case strings.HasPrefix(l, "==> "):
		return theme.Muted.Render("· " + strings.TrimPrefix(l, "==> "))
	case strings.HasPrefix(l, "Error: "):
		return theme.Err.Render("✗") + " " + strings.TrimPrefix(l, "Error: ")
	case strings.HasPrefix(l, "Warning: Not upgrading "):
		// Common after an auto-fix verify re-run — it means success.
		name := strings.TrimPrefix(l, "Warning: Not upgrading ")
		if i := strings.IndexByte(name, ','); i > 0 {
			name = name[:i]
		}
		return theme.Muted.Render("· " + name + " already up to date")
	case strings.HasPrefix(l, "Warning: "):
		return theme.Warn.Render("⚠") + " " + strings.TrimPrefix(l, "Warning: ")
	case strings.HasSuffix(l, "was successfully upgraded!"), strings.HasSuffix(l, "was successfully installed!"):
		return theme.OK.Render("✓") + " " + strings.TrimSuffix(strings.TrimSuffix(l, " was successfully upgraded!"), " was successfully installed!")
	case strings.HasPrefix(l, "auto-fix"), strings.HasPrefix(l, "auto-fixed"):
		return theme.Warn.Render("⟳") + " " + l
	}
	for _, p := range brewNoise {
		if strings.HasPrefix(l, p) {
			return theme.Muted.Render(l)
		}
	}
	return l
}
