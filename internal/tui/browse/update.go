package browse

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/journal"
)

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		// Resize the filter input to fit the bottom bar.
		m.filter.Width = max(m.width-20, 20)
		m.clampOffset()
		return m, nil

	case tickMsg:
		m.spin++
		return m, tickCmd()

	case pkgsLoadedMsg:
		m.loadingList = false
		if msg.err != nil {
			m.loadErr = msg.err
			m.status = "failed: " + msg.err.Error()
			return m, nil
		}
		m.cache[msg.scope] = msg.pkgs
		// Only apply if it matches the current scope — older in-flight
		// fetches from an earlier scope are ignored.
		if msg.scope != m.scope {
			return m, nil
		}
		m.pkgs = msg.pkgs
		m.cursor = 0
		m.listOffset = 0
		m.applyFilter()
		m.status = ""
		// Kick off info load for the first selection.
		if sel := m.selectedPackage(); sel != nil {
			m.loadingInfo = true
			m.info = nil
			m.infoName = sel.Name
			return m, m.loadInfoCmd(sel.Name)
		}
		return m, nil

	case infoLoadedMsg:
		// Drop stale responses.
		if msg.name != m.infoName {
			return m, nil
		}
		m.loadingInfo = false
		if msg.err != nil {
			m.info = nil
			return m, nil
		}
		m.info = msg.pkg
		m.infoRevCount = msg.revCount
		return m, nil

	case formulaLoadedMsg:
		if msg.name != m.formulaName {
			return m, nil
		}
		m.formulaBusy = false
		m.formulaBody = msg.body
		m.formulaErr = msg.err
		return m, nil

	case mutationStartedMsg:
		m.mutating = true
		m.mutationName = msg.name
		m.mutationAction = msg.action
		m.muBuf.Reset()
		m.status = msg.action.verb() + "ing " + msg.name + "…"
		return m, m.drainProgressCmd()

	case mutationProgressMsg:
		m.muBuf.WriteString(msg.line)
		m.muBuf.WriteByte('\n')
		// Keep the bottom status compact — only the last line is surfaced.
		line := strings.TrimSpace(msg.line)
		if line != "" {
			// Clip overly long progress lines to fit.
			if len(line) > 60 {
				line = line[:57] + "…"
			}
			m.status = m.mutationAction.verb() + "ing " + m.mutationName + ": " + line
		}
		return m, m.drainProgressCmd()

	case mutationStepMsg:
		if msg.msg != "" {
			m.status = m.mutationAction.verb() + "ing " + m.mutationName + ": " + msg.msg
		}
		return m, m.drainProgressCmd()

	case mutationDoneMsg:
		m.mutating = false
		if msg.err != nil {
			m.status = "failed: " + msg.err.Error()
		} else {
			m.status = msg.action.verb() + " ok: " + msg.name
		}
		// Invalidate caches so the list reflects the mutation and reload.
		m.cache = map[Scope][]backend.Package{}
		m.loadingList = true
		// Also refresh the preview.
		if m.info != nil && m.info.Name == msg.name {
			m.loadingInfo = true
			m.info = nil
		}
		return m, tea.Batch(m.loadScopeCmd(m.scope), m.loadInfoCmd(msg.name))

	case clipboardMsg:
		if msg.err != nil {
			m.flash = "clipboard: " + msg.err.Error()
		} else {
			m.flash = "yanked " + msg.name
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// handleKey routes key presses based on current focus.
func (m *model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global shortcuts regardless of focus.
	switch k.String() {
	case "ctrl+c":
		return m, tea.Quit
	}

	switch m.focus {
	case focusFilter:
		return m.handleFilterKey(k)
	case focusConfirm:
		return m.handleConfirmKey(k)
	case focusHelp:
		switch k.String() {
		case "esc", "?", "q":
			m.focus = focusList
		}
		return m, nil
	case focusFormula:
		return m.handleFormulaKey(k)
	}
	return m.handleListKey(k)
}

func (m *model) handleListKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "?":
		m.focus = focusHelp
		return m, nil
	case "/":
		m.focus = focusFilter
		m.filter.Focus()
		return m, nil
	case "tab":
		m.scope = m.scope.next()
		m.loadingList = true
		m.pkgs = nil
		m.filtered = m.filtered[:0]
		m.cursor = 0
		m.listOffset = 0
		m.info = nil
		return m, m.loadScopeCmd(m.scope)
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		} else if len(m.filtered) > 0 {
			m.cursor = len(m.filtered) - 1
		}
		m.clampOffset()
		return m, m.maybeLoadInfoCmd()
	case "down", "j":
		if m.cursor+1 < len(m.filtered) {
			m.cursor++
		} else {
			m.cursor = 0
		}
		m.clampOffset()
		return m, m.maybeLoadInfoCmd()
	case "pgup", "ctrl+u":
		m.cursor -= m.listVisibleRows()
		if m.cursor < 0 {
			m.cursor = 0
		}
		m.clampOffset()
		return m, m.maybeLoadInfoCmd()
	case "pgdown", "ctrl+d":
		m.cursor += m.listVisibleRows()
		if m.cursor >= len(m.filtered) {
			m.cursor = len(m.filtered) - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
		m.clampOffset()
		return m, m.maybeLoadInfoCmd()
	case "g":
		m.cursor = 0
		m.clampOffset()
		return m, m.maybeLoadInfoCmd()
	case "G":
		if len(m.filtered) > 0 {
			m.cursor = len(m.filtered) - 1
		}
		m.clampOffset()
		return m, m.maybeLoadInfoCmd()
	case "enter":
		return m.startPrompt(decidePrimaryAction(m.selectedPackage()))
	case "i":
		return m.startPrompt(ActInstall)
	case "r":
		return m.startPrompt(ActRemove)
	case "u":
		return m.startPrompt(ActUpgrade)
	case "v":
		return m.startFormulaView()
	case "y":
		sel := m.selectedPackage()
		if sel == nil {
			return m, nil
		}
		name := sel.Name
		return m, func() tea.Msg {
			err := clipboard.WriteAll(name)
			return clipboardMsg{name: name, err: err}
		}
	case "esc":
		m.flash = ""
		return m, nil
	}
	return m, nil
}

func (m *model) handleFilterKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.filter.Blur()
		m.filter.SetValue("")
		m.focus = focusList
		m.applyFilter()
		return m, m.maybeLoadInfoCmd()
	case "enter":
		m.filter.Blur()
		m.focus = focusList
		m.applyFilter()
		return m, m.maybeLoadInfoCmd()
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(k)
	m.applyFilter()
	return m, tea.Batch(cmd, m.maybeLoadInfoCmd())
}

func (m *model) handleConfirmKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "Y", "enter":
		action := m.pending.action
		name := m.pending.name
		m.pending = pendingMutation{}
		m.focus = focusList
		return m, m.startMutationCmd(action, name)
	case "n", "N", "esc", "q":
		m.pending = pendingMutation{}
		m.focus = focusList
		m.status = ""
		return m, nil
	}
	return m, nil
}

func (m *model) handleFormulaKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q", "v":
		m.focus = focusList
		m.formulaBody = ""
		m.formulaName = ""
		m.formulaScrl = 0
		return m, nil
	case "up", "k":
		if m.formulaScrl > 0 {
			m.formulaScrl--
		}
	case "down", "j":
		m.formulaScrl++
	case "pgup", "ctrl+u":
		m.formulaScrl -= 10
		if m.formulaScrl < 0 {
			m.formulaScrl = 0
		}
	case "pgdown", "ctrl+d":
		m.formulaScrl += 10
	case "g":
		m.formulaScrl = 0
	}
	return m, nil
}

// maybeLoadInfoCmd returns an Info+Deps fetch for the current selection if
// we don't already have fresh info for it.
func (m *model) maybeLoadInfoCmd() tea.Cmd {
	sel := m.selectedPackage()
	if sel == nil {
		m.info = nil
		return nil
	}
	if m.info != nil && m.info.Name == sel.Name {
		return nil
	}
	m.infoName = sel.Name
	m.loadingInfo = true
	m.info = nil
	return m.loadInfoCmd(sel.Name)
}

// startPrompt arms a confirm prompt for the current selection.
func (m *model) startPrompt(act Action) (tea.Model, tea.Cmd) {
	sel := m.selectedPackage()
	if sel == nil {
		return m, nil
	}
	// Upgrade only makes sense when there's a newer version available.
	if act == ActUpgrade && sel.Latest != "" && sel.Version != "" && sel.Latest == sel.Version {
		m.status = sel.Name + " is up to date"
		return m, nil
	}
	m.pending = pendingMutation{action: act, name: sel.Name}
	m.focus = focusConfirm
	return m, nil
}

// decidePrimaryAction picks install vs. remove based on installation state.
func decidePrimaryAction(p *backend.Package) Action {
	if p == nil {
		return ActInstall
	}
	if p.Version != "" {
		return ActRemove
	}
	return ActInstall
}

// ---- mutation plumbing ----

// browseProgress is a backend.ProgressWriter that pushes lines into a channel
// so the tea loop can render them in the status bar. It's safe to call Write
// from any goroutine.
type browseProgress struct {
	ch chan<- string
}

func (b *browseProgress) Write(p []byte) (int, error) {
	// Split on newlines so each status chunk is its own message.
	for line := range strings.SplitSeq(string(p), "\n") {
		if line == "" {
			continue
		}
		select {
		case b.ch <- line:
		default:
			// Drop if the consumer is slow; rendering never blocks the
			// mutation.
		}
	}
	return len(p), nil
}

func (b *browseProgress) Step(msg string) {
	select {
	case b.ch <- "→ " + msg:
	default:
	}
}

// startMutationCmd spins up a goroutine to run the selected action and
// returns the initial "started" message. Progress lines arrive later via
// drainProgressCmd.
func (m *model) startMutationCmd(act Action, name string) tea.Cmd {
	// Ensure channels are fresh.
	m.muCh = make(chan string, 64)
	m.muDoneCh = make(chan error, 1)
	reg := m.reg
	ctx := m.ctxOr()
	jrnl := m.jrnl

	pw := &browseProgress{ch: m.muCh}

	go func() {
		var err error
		var action string
		switch act {
		case ActInstall:
			err = reg.Primary().Install(ctx, []string{name}, pw)
			action = "installed"
		case ActRemove:
			err = reg.Primary().Remove(ctx, []string{name}, pw)
			action = "removed"
		case ActReinstall:
			err = reg.Primary().Reinstall(ctx, []string{name}, pw)
			action = "reinstalled"
		case ActUpgrade:
			err = reg.Primary().Upgrade(ctx, []string{name}, pw)
			action = "upgraded"
		}
		// Journal the transaction if we have one. Best-effort — journal
		// failures should not mask a successful brew call.
		if jrnl != nil {
			exit := 0
			if err != nil {
				exit = 1
			}
			txID, jerr := jrnl.Begin(ctx, act.verb(),
				journal.Cmdline([]string{"yum", "browse", act.verb(), name}),
				"", "")
			if jerr == nil {
				_ = jrnl.End(ctx, txID, exit, []journal.TxPackage{{
					Name:   name,
					Source: "formula",
					Action: action,
				}})
			}
		}
		m.muDoneCh <- err
	}()

	return tea.Batch(
		func() tea.Msg { return mutationStartedMsg{action: act, name: name} },
	)
}

// drainProgressCmd waits for either the next progress line or a completion
// message and surfaces it as the corresponding tea.Msg. Runs in a loop via
// self-resubscription so progress keeps streaming until done.
func (m *model) drainProgressCmd() tea.Cmd {
	if m.muCh == nil || m.muDoneCh == nil {
		return nil
	}
	ch := m.muCh
	done := m.muDoneCh
	act := m.mutationAction
	name := m.mutationName
	return func() tea.Msg {
		select {
		case line, ok := <-ch:
			if !ok {
				return nil
			}
			return mutationProgressMsg{line: line}
		case err := <-done:
			// Drain any remaining buffered lines into the final state.
			return mutationDoneMsg{action: act, name: name, err: err}
		}
	}
}

// ---- formula overlay ----

func (m *model) startFormulaView() (tea.Model, tea.Cmd) {
	sel := m.selectedPackage()
	if sel == nil {
		return m, nil
	}
	name := sel.Name
	m.focus = focusFormula
	m.formulaName = name
	m.formulaBody = ""
	m.formulaErr = nil
	m.formulaScrl = 0
	m.formulaBusy = true

	ctx := m.ctxOr()
	return m, func() tea.Msg {
		cctx, cancel := context.WithCancel(ctx)
		defer cancel()
		cmd := exec.CommandContext(cctx, "brew", "cat", name)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		return formulaLoadedMsg{
			name: name,
			body: out.String(),
			err:  err,
		}
	}
}

// ---- helpers ----

// truncStr trims a string to n runes with an ellipsis if it was cut.
func truncStr(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// padRight pads a string with spaces to width n (rune-aware).
func padRight(s string, n int) string {
	r := []rune(s)
	if len(r) >= n {
		return string(r[:n])
	}
	return s + strings.Repeat(" ", n-len(r))
}

// formatSize turns bytes into a short human-friendly string.
func formatSize(b int64) string {
	if b <= 0 {
		return "-"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	pre := "KMGTPE"[exp]
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), pre)
}
