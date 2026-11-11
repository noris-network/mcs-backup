package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/noris-network/mcs-backup/v2/internal/present"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

type tab int

const (
	tabOverview tab = iota
	tabSnapshots
	tabLocks
	tabAccess
	numTabs
)

var tabNames = [numTabs]string{"Overview", "Snapshots", "Locks", "Access"}

const maxLog = 200

// Model is the Bubble Tea model of the TUI.
type Model struct {
	ctx context.Context //nolint:containedctx // a Bubble Tea model has no other way to reach the commands
	svc service.Service
	o   Options

	width, height int
	tab           tab
	cursor        [numTabs]int
	showForget    bool // snapshots tab: the forget preview instead of the list
	quitting      bool

	status    *service.Status
	snapshots []service.Snapshot // newest first
	locks     []service.Lock
	retention *service.RetentionInfo
	env       *service.EnvInfo
	revealed  bool
	webLink   string

	// connErr is why the service cannot be reached, empty while it can.
	connErr string
	flash   string
	flashAt time.Time

	op          *service.Operation
	log         []string
	lastResult  *service.RunResult
	lastRestore *service.RestoreRequest

	dlg dialog
	// frame counts the ticks, for the spinner
	frame int
}

// New creates the model. ctx should carry service.SourceTUI.
func New(ctx context.Context, svc service.Service, o Options) *Model {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Model{ctx: ctx, svc: svc, o: o, width: 100, height: 30}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return tea.Batch(m.refresh(), tick()) }

func (m *Model) refresh() tea.Cmd {
	return tea.Batch(fetchStatus(m.ctx, m.svc), fetchSnapshots(m.ctx, m.svc), fetchLocks(m.ctx, m.svc), fetchRetention(m.ctx, m.svc))
}

func (m *Model) appendOnly() bool { return m.status != nil && m.status.AppendOnly }

func (m *Model) say(format string, args ...any) {
	m.flash, m.flashAt = fmt.Sprintf(format, args...), m.o.Now()
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		resized := msg.Width != m.width || msg.Height != m.height
		m.width, m.height = msg.Width, msg.Height
		if resized { // repaint everything: what an earlier, wider frame left behind must not stay
			return m, tea.ClearScreen
		}
	case tea.KeyPressMsg:
		return m, m.key(msg)
	case tickMsg:
		m.frame++
		if m.flash != "" && m.o.Now().Sub(m.flashAt) > 10*time.Second {
			m.flash = ""
		}
		return m, tick()
	case statusMsg:
		m.gotStatus(msg)
	case snapshotsMsg:
		if !m.fail("snapshots", msg.err) {
			m.setSnapshots(msg.list)
		}
	case locksMsg:
		if !m.fail("locks", msg.err) {
			m.locks = msg.list
			m.clamp(tabLocks, len(m.locks))
		}
	case retentionMsg:
		if !m.fail("retention", msg.err) {
			m.retention = &msg.info
		}
	case envMsg:
		if !m.fail("access", msg.err) {
			m.env, m.revealed = &msg.info, msg.revealed
		}
	case logMsg:
		if !m.fail("log", msg.err) {
			m.dlg = &textDialog{title: "Log of the last " + string(msg.log.Kind) + " run", lines: msg.log.Lines, follow: true}
		}
	case preflightMsg:
		return m, m.gotPreflight(msg)
	case restoreStartMsg:
		return m, m.restoreStarted(msg)
	case filesMsg:
		m.gotFiles(msg)
	case diffMsg:
		if !m.fail("diff", msg.err) {
			m.dlg = &textDialog{title: "What changed", lines: present.DiffLines(msg.diff)}
		}
	case findMsg:
		m.gotFind(msg)
	case pasteMsg:
		m.dlg = &pasteDialog{title: "restic environment", intro: "These lines set up restic for this repository and contain the secrets. Select them with the mouse, copy them and paste them into a shell.", text: msg.text}
	case linkMsg:
		if !m.fail("web UI", msg.err) {
			m.webLink = msg.url
			d := &linkDialog{url: msg.url}
			if m.o.Hint != nil {
				d.hint = m.o.Hint(msg.url)
			}
			m.dlg = d
		}
	case doneMsg:
		return m, m.done(msg)
	case eventMsg:
		return m, m.event(msg.ev)
	case streamMsg:
		if msg.err != nil {
			m.connErr = msg.err.Error()
			return m, nil
		}
		m.connErr, m.op = "", nil // the stream starts with the running operation, if any
		return m, m.refresh()
	}
	return m, nil
}

func (m *Model) gotStatus(msg statusMsg) {
	if msg.err != nil {
		m.connErr = msg.err.Error()
		return
	}
	m.connErr = ""
	m.status = &msg.st
	if msg.st.Operation != nil { // a status that was fetched before an operation began must not hide it
		m.op = msg.st.Operation
	}
}

// fail reports an error of a call and tells whether there was one.
func (m *Model) fail(what string, err error) bool {
	if err == nil {
		return false
	}
	m.say("%s: %s", what, describe(err))
	return true
}

// describe turns an error of the service into a sentence for the screen.
func describe(err error) string {
	if b, ok := errors.AsType[*service.BusyError](err); ok {
		return fmt.Sprintf("another operation is running (%s); wait for it or cancel it", b.Current.Kind)
	}
	switch {
	case errors.Is(err, service.ErrUnavailable):
		return "not available in this configuration"
	case errors.Is(err, service.ErrAppendOnly):
		return "not possible, the repository is append-only"
	}
	return err.Error()
}

// setSnapshots replaces the list. The cursor stays on the snapshot it was on,
// also when new snapshots come in above it: found by its ID, or by its time
// and host when a change of its tags gave it a new ID. A snapshot that is
// gone leaves the cursor on the same row.
func (m *Model) setSnapshots(list []service.Snapshot) {
	var prev *service.Snapshot
	if c := m.cursor[tabSnapshots]; c < len(m.snapshots) {
		prev = &m.snapshots[c]
	}
	sorted := slices.Clone(list)
	slices.SortStableFunc(sorted, func(a, b service.Snapshot) int { return b.Time.Compare(a.Time) })
	if prev != nil {
		i := slices.IndexFunc(sorted, func(s service.Snapshot) bool { return s.ID == prev.ID })
		if i < 0 {
			i = slices.IndexFunc(sorted, func(s service.Snapshot) bool { return s.Time.Equal(prev.Time) && s.Host == prev.Host })
		}
		if i >= 0 {
			m.cursor[tabSnapshots] = i
		}
	}
	m.snapshots = sorted
	m.clamp(tabSnapshots, len(m.snapshots))
}

func (m *Model) clamp(t tab, n int) {
	m.cursor[t] = max(0, min(m.cursor[t], n-1))
}

func (m *Model) done(msg doneMsg) tea.Cmd {
	if m.fail(msg.what, msg.err) {
		return nil
	}
	if msg.note != "" {
		m.say("%s", msg.note)
	} else {
		m.say("%s: done", msg.what)
	}
	return m.refresh()
}

func (m *Model) event(ev service.Event) tea.Cmd {
	switch ev.Type {
	case service.EventStarted:
		m.op, m.lastResult, m.log = ev.Operation, nil, nil
		return fetchStatus(m.ctx, m.svc)
	case service.EventPhase, service.EventProgress:
		if ev.Operation != nil {
			m.op = ev.Operation
		}
	case service.EventLog:
		if text, ok := present.LogLine(ev.Line); ok { // the panel shows the readable form, the log dialog the original
			m.log = append(m.log, text)
		}
		if len(m.log) > maxLog {
			m.log = m.log[len(m.log)-maxLog:]
		}
		if t, ok := m.dlg.(*textDialog); ok && t.follow {
			t.lines = append(t.lines, ev.Line)
		}
	case service.EventFinished:
		m.op = nil
		if ev.Result != nil {
			m.lastResult = ev.Result
		}
		return m.refresh()
	case service.EventStatus:
		return fetchStatus(m.ctx, m.svc)
	}
	return nil
}

// selectedSnapshot returns the snapshot under the cursor.
func (m *Model) selectedSnapshot() (service.Snapshot, bool) {
	if m.tab != tabSnapshots || m.cursor[tabSnapshots] >= len(m.snapshots) {
		return service.Snapshot{}, false
	}
	return m.snapshots[m.cursor[tabSnapshots]], true
}

// key handles a key press.
func (m *Model) key(k tea.KeyPressMsg) tea.Cmd {
	if cur := m.dlg; cur != nil {
		cmd, closed := cur.key(m, k)
		if closed && m.dlg == cur { // a dialog may have opened its successor
			m.dlg = nil
		}
		return cmd
	}
	s := k.String()
	switch s {
	case "ctrl+c", "q":
		m.quitting = true
		return tea.Quit
	case "?":
		m.dlg = &helpDialog{}
	case "tab", "right":
		return m.switchTab((m.tab + 1) % numTabs)
	case "shift+tab", "left":
		return m.switchTab((m.tab + numTabs - 1) % numTabs)
	case "1", "2", "3", "4":
		return m.switchTab(tab(s[0] - '1'))
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "r":
		return m.refresh()
	case "b":
		m.dlg = newBackupDialog(m)
	case "c":
		return m.cancel()
	case "v":
		return m.start("restore test", func() (service.Operation, error) {
			return m.svc.StartVerify(m.ctx, service.VerifyRequest{})
		})
	case "l":
		return fetchLog(m.ctx, m.svc, m.logKind())
	case "s":
		m.dlg = newScheduleDialog(m.status, m.o.Now())
	case "n":
		return act("test notification", func() error { return m.svc.NotifyTest(m.ctx) })
	case "w":
		return m.openWeb()
	case "x":
		return m.retryWithoutSafety()
	default:
		return m.tabKey(s)
	}
	return nil
}

func (m *Model) switchTab(t tab) tea.Cmd {
	m.tab = t
	if t == tabAccess && m.env == nil {
		return fetchEnv(m.ctx, m.svc, false)
	}
	return nil
}

func (m *Model) move(delta int) {
	n := 0
	switch m.tab {
	case tabSnapshots:
		if m.showForget { // the list is not in view
			return
		}
		n = len(m.snapshots)
	case tabLocks:
		n = len(m.locks)
	}
	m.cursor[m.tab] = max(0, min(m.cursor[m.tab]+delta, n-1))
}

// logKind picks the log to show: the running or last operation, else the backup.
func (m *Model) logKind() service.Kind {
	switch {
	case m.op != nil:
		return m.op.Kind
	case m.lastResult != nil:
		return m.lastResult.Kind
	}
	return service.KindBackup
}

func (m *Model) cancel() tea.Cmd {
	if m.op == nil && (m.status == nil || m.status.Retry == nil) {
		m.say("nothing to cancel")
		return nil
	}
	return act("cancel", func() error { return m.svc.Cancel(m.ctx) })
}

// start begins an operation and reports it as an action.
func (m *Model) start(what string, fn func() (service.Operation, error)) tea.Cmd {
	return func() tea.Msg {
		_, err := fn()
		return doneMsg{what: what, note: what + " started", err: err}
	}
}

// tabKey handles the keys of the active tab.
func (m *Model) tabKey(s string) tea.Cmd {
	switch m.tab {
	case tabSnapshots:
		return m.snapshotKey(s)
	case tabLocks:
		return m.lockKey(s)
	case tabAccess:
		return m.accessKey(s)
	case tabOverview:
		return m.overviewKey(s)
	}
	return nil
}

func (m *Model) overviewKey(s string) tea.Cmd {
	if s == "F" && m.status != nil && m.status.ForgetHeld && !m.appendOnly() {
		m.dlg = newConfirmDialog("Approve the held forget", "The retention guard held back a forget that would remove many snapshots. Review them in Snapshots, then type "+service.ConfirmForget+" to run it now.", service.ConfirmForget,
			func(word string) tea.Cmd {
				return m.start("forget", func() (service.Operation, error) { return m.svc.ApproveForget(m.ctx, word) })
			})
	}
	return nil
}

func (m *Model) snapshotKey(s string) tea.Cmd {
	if m.showForget {
		// the preview hides the list: its keys must not act on a snapshot
		// that cannot be seen
		if s == "f" || s == "esc" {
			m.showForget = false
		}
		return nil
	}
	snap, ok := m.selectedSnapshot()
	switch s {
	case "f":
		m.showForget = true
		return nil
	case "enter":
		if ok {
			m.dlg = newRestoreDialog(snap.ShortID)
		}
		return nil
	case "t":
		m.dlg = newRestoreDialog("latest")
		return nil
	case "d":
		req := service.DiffRequest{}
		if ok {
			req.To = snap.ID
		}
		return func() tea.Msg {
			d, err := m.svc.Diff(m.ctx, req)
			return diffMsg{d, err}
		}
	case "o":
		if ok {
			return m.openFiles(snap.ShortID)
		}
		return m.openFiles("latest")
	}
	if !ok || m.appendOnly() {
		if ok && (s == "k" || s == "a") {
			m.say("not possible, the repository is append-only")
		}
		return nil
	}
	switch s {
	case "K":
		if snap.Keep {
			return act("unkeep", func() error { _, err := m.svc.Unkeep(m.ctx, snap.ID); return err })
		}
		return act("keep", func() error { _, err := m.svc.Keep(m.ctx, snap.ID); return err })
	case "a":
		if !snap.Suspicious {
			m.say("snapshot %s is not marked suspicious", snap.ShortID)
			return nil
		}
		m.dlg = newConfirmDialog("Acknowledge the size change", "Snapshot "+snap.ShortID+" is much smaller than the one before. If that is expected, type "+service.ConfirmAcknowledge+": it becomes a regular snapshot and the retention applies again.", service.ConfirmAcknowledge,
			func(word string) tea.Cmd {
				return act("acknowledge", func() error { _, err := m.svc.Acknowledge(m.ctx, snap.ID, word); return err })
			})
	}
	return nil
}

func (m *Model) lockKey(s string) tea.Cmd {
	switch s {
	case "u":
		return act("unlock", func() error { return m.svc.Unlock(m.ctx, false, "") })
	case "U":
		if m.appendOnly() {
			m.say("not possible, the repository is append-only")
			return nil
		}
		m.dlg = newConfirmDialog("Remove all locks", "This also removes locks of running processes, which can corrupt a running backup. Type "+service.ConfirmRemoveAll+" to continue.", service.ConfirmRemoveAll,
			func(word string) tea.Cmd {
				return act("unlock all", func() error { return m.svc.Unlock(m.ctx, true, word) })
			})
	}
	return nil
}

func (m *Model) accessKey(s string) tea.Cmd {
	switch s {
	case "p":
		return fetchEnv(m.ctx, m.svc, !m.revealed)
	case "y":
		return m.showEnv()
	}
	return nil
}

// showEnv shows the environment in export format, with the secrets, as plain
// text that can be selected and pasted into a shell.
func (m *Model) showEnv() tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		info, err := m.svc.Env(ctx, true)
		if err != nil {
			return doneMsg{what: "access", err: err}
		}
		return pasteMsg{text: exportText(info)}
	}
}

// openWeb switches the web UI on and shows a one-time login link.
func (m *Model) openWeb() tea.Cmd {
	return func() tea.Msg {
		st, err := m.svc.WebUI(m.ctx)
		if err != nil {
			return linkMsg{err: err}
		}
		if !st.Enabled {
			if _, err := m.svc.SetWebUI(m.ctx, service.WebUIRequest{Enable: true}); err != nil {
				return linkMsg{err: err}
			}
		}
		url, err := m.svc.LoginLink(m.ctx)
		return linkMsg{url: url, err: err}
	}
}

// retryWithoutSafety offers to repeat a failed in-place restore without the
// safety snapshot, for example when the application is dead and its dump
// cannot run any more.
func (m *Model) retryWithoutSafety() tea.Cmd {
	r, req := m.lastResult, m.lastRestore
	if r == nil || r.Kind != service.KindRestore || r.Result.OK() || req == nil || req.NoSafetySnapshot || req.Target != "" {
		m.say("there is no failed restore to repeat")
		return nil
	}
	again := *req
	again.NoSafetySnapshot = true
	m.dlg = newConfirmDialog("Restore without safety snapshot", "The safety snapshot of the current data failed. Without it, the current data is lost for good. Type no-safety-snapshot to go on.", "no-safety-snapshot",
		func(string) tea.Cmd {
			m.dlg = newConfirmDialog("Confirm the restore", "Type "+again.Snapshot+" to replace the current data.", again.Snapshot,
				func(word string) tea.Cmd {
					again.Confirm = word
					return m.startRestore(again)
				})
			return nil
		})
	return nil
}

func (m *Model) startRestore(req service.RestoreRequest) tea.Cmd {
	m.lastRestore = &req
	return func() tea.Msg {
		_, err := m.svc.StartRestore(m.ctx, req)
		return restoreStartMsg{err}
	}
}

// restoreStarted closes the restore dialog once the restore runs. When it
// could not start, because a scheduled backup runs at that moment for
// example, the dialog stays with all that was entered and says why.
func (m *Model) restoreStarted(msg restoreStartMsg) tea.Cmd {
	if d, ok := m.dlg.(*restoreDialog); ok {
		d.starting = false
		if msg.err != nil {
			d.err = describe(msg.err)
			if _, busy := errors.AsType[*service.BusyError](msg.err); busy {
				d.err += ". Press enter to try again."
			}
			return nil
		}
		m.dlg = nil
	}
	return m.done(doneMsg{what: "restore", note: "restore started", err: msg.err})
}

// gotPreflight moves the restore dialog on to the preview.
func (m *Model) gotPreflight(msg preflightMsg) tea.Cmd {
	d, ok := m.dlg.(*restoreDialog)
	if !ok {
		return nil
	}
	if msg.err != nil {
		d.err = describe(msg.err)
		return nil
	}
	d.req, d.pre, d.stage, d.err, d.now, d.top = msg.req, &msg.pre, stagePreview, "", m.o.Now(), 0
	return nil
}
