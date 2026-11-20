package webui

import (
	"cmp"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/envexport"
	"github.com/noris-network/mcs-backup/v2/internal/present"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// liveData is the part of the overview that refreshes by itself.
type liveData struct {
	// Cards are the headline figures, Rows the details below them.
	Cards []present.Card
	Rows  []detailRow
	// Notes need attention; NotesLevel is the color of the worst one, info
	// when they are only for information.
	Notes      []note
	NotesLevel string
	// Runs are the latest results of the kinds of operations.
	Runs []runRow
	// Op is the running operation: what it does in words (OpTitle), what
	// it is (OpLine) and the latest lines of its log in plain words.
	Op              *service.Operation
	OpTitle, OpLine string
	Log             []string
	Result          *service.RunResult
	ResultLines     []string
	ResultOK        bool
	ResultClass     string
	Retry           *service.RetryInfo
	// CanRepeat: the last restore failed and can be repeated without the safety snapshot.
	CanRepeat   bool
	RepeatWhat  string
	Web         service.WebUIStatus
	Schedule    scheduleState
	ForgetHeld  bool
	AppendOnly  bool
	VerifyReady bool
	// CanPrune: a repository is not append-only, so prune has something to do.
	CanPrune bool
	// Fragment is set when htmx refreshes the part; it then brings the
	// indicator of the header along (Health, HealthLevel).
	Fragment            bool
	Health, HealthLevel string
}

// scheduleState is what the schedule panel says and offers: to pause or
// disable the scheduled backups when they run (On), to enable them otherwise,
// and nothing when there is no backup schedule (Manual).
type scheduleState struct {
	Text       string
	On, Manual bool
}

// detailRow is a row of the details; Sub rows belong to the row above them.
type detailRow struct {
	Label, Text string
	Sub         bool
}

// note is one thing that needs attention.
type note struct {
	Text     string
	Severity service.Severity
}

// runRow is the latest result of one kind of operation.
type runRow struct {
	Kind              service.Kind
	Result            service.Result
	Class             string
	Started, Duration string
	Snapshot          string
}

func resultClass(r service.Result) string {
	switch r {
	case service.ResultSuccess:
		return "ok"
	case service.ResultWarning:
		return "warn"
	case service.ResultSkipped, service.ResultCancelled: // a cancel is no failure
		return "skip"
	}
	return "bad"
}

func (h *Handler) liveData(r *http.Request) (liveData, error) {
	st, err := h.svc.Status(r.Context())
	if err != nil {
		return liveData{}, err
	}
	d := liveData{Op: st.Operation, Retry: st.Retry, Web: st.WebUI, ForgetHeld: st.ForgetHeld, AppendOnly: st.AppendOnly, VerifyReady: st.VerifyConfigured,
		CanPrune: !st.AppendOnly || st.Copy != nil}
	d.Cards = present.Cards(st, h.o.Now())
	label, level := present.Health(st)
	d.Health, d.HealthLevel = label, string(level)
	d.Schedule.Manual = st.Schedule.Backup == ""
	d.Schedule.On = st.Schedule.Enabled && !st.Schedule.MaintenanceUntil.After(h.o.Now())
	// the cards, the notes, the runs and the operation are shown on their own
	inRuns := false
	for _, row := range present.StatusRows(st, h.o.Now()) {
		switch {
		case row.Label == "Last runs":
			inRuns = true
		case inRuns && row.Label == "" && row.Severity == "":
		case row.Severity != "" || row.Label == "Notes":
			d.Notes = append(d.Notes, note{strings.TrimPrefix(row.Text, present.SeverityMark(row.Severity)+" "), row.Severity})
			switch {
			case row.Severity == service.SeverityError:
				d.NotesLevel = "bad"
			case row.Severity == service.SeverityWarning && d.NotesLevel != "bad":
				d.NotesLevel = "warn"
			case d.NotesLevel == "":
				d.NotesLevel = "info" // only something to know, like a pause for maintenance
			}
		case row.Label == "Repository" || row.Label == "Last backup" || row.Label == "Secondary" || row.Label == "  next backup" || row.Label == "Operation":
		default:
			inRuns = false
			if row.Label == "Schedule" {
				d.Schedule.Text = row.Text
			}
			label := strings.TrimLeft(row.Label, " ")
			d.Rows = append(d.Rows, detailRow{Label: label, Text: row.Text, Sub: label != row.Label})
		}
	}
	runs := slices.SortedFunc(maps.Values(st.LastRuns), newestFirst)
	for _, res := range runs {
		d.Runs = append(d.Runs, runRow{
			Kind: res.Kind, Result: res.Result, Class: resultClass(res.Result), Started: present.Stamp(res.Started),
			Duration: present.Duration(res.Duration.Std()), Snapshot: present.Short(res.Snapshot),
		})
	}
	switch {
	case st.Operation != nil:
		op := st.Operation
		d.OpTitle, d.OpLine = present.OperationTitle(*op), present.OperationMeta(h.o.Now(), *op)
		if l, err := h.svc.Log(r.Context(), op.Kind); err == nil {
			for _, line := range l.Lines[max(0, len(l.Lines)-30):] {
				if text, ok := present.LogLine(line); ok {
					d.Log = append(d.Log, text)
				}
			}
			d.Log = d.Log[max(0, len(d.Log)-6):]
		}
	case len(runs) > 0:
		// the most recent result of any kind
		last := &runs[0]
		d.Result, d.ResultLines, d.ResultOK, d.ResultClass = last, present.ResultLines(*last), last.Result.OK(), resultClass(last.Result)
		h.mu.Lock()
		rr := h.lastRestore
		h.mu.Unlock()
		if last.Kind == service.KindRestore && !last.Result.OK() && rr != nil && rr.Target == "" && !rr.NoSafetySnapshot {
			d.CanRepeat, d.RepeatWhat = true, rr.Snapshot
		}
	}
	return d, nil
}

// newestFirst orders results by their start, the newest first. The copy of a
// backup has the start of the backup and comes after it, so that the backup
// is the last result; the kind keeps the order stable between refreshes.
func newestFirst(a, b service.RunResult) int {
	copyLast := func(r service.RunResult) int {
		if r.Kind == service.KindCopy {
			return 1
		}
		return 0
	}
	return cmp.Or(b.Started.Compare(a.Started), cmp.Compare(copyLast(a), copyLast(b)), strings.Compare(string(a.Kind), string(b.Kind)))
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	d, err := h.liveData(h.ctx(r))
	if err != nil {
		h.fail(w, r, "overview", err)
		return
	}
	h.render(w, r, "overview", "overview", d)
}

// live is the fragment that htmx reloads every few seconds.
func (h *Handler) live(w http.ResponseWriter, r *http.Request) {
	d, err := h.liveData(h.ctx(r))
	if err != nil {
		http.Error(w, describe(err), http.StatusBadGateway)
		return
	}
	d.Fragment = true
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.pages["overview"].ExecuteTemplate(w, "live", d); err != nil {
		h.o.Log.ErrorContext(r.Context(), "web UI fragment failed", "error", err)
	}
}

// fail shows an error where a page would have been.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, name string, err error) {
	h.setFlash(w, r, false, describe(err))
	if r.URL.Path != "/" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	// the overview itself failed: show the message on an otherwise empty page
	h.render(w, r, name, "overview", liveData{})
}

type snapshotsData struct {
	Snapshots []service.Snapshot
	Retention *service.RetentionInfo
}

func (h *Handler) snapshots(w http.ResponseWriter, r *http.Request) {
	r = h.ctx(r)
	snaps, err := h.svc.Snapshots(r.Context())
	if err != nil {
		h.fail(w, r, "snapshots", err)
		return
	}
	slices.SortStableFunc(snaps, func(a, b service.Snapshot) int { return b.Time.Compare(a.Time) })
	d := snapshotsData{Snapshots: snaps}
	if ret, err := h.svc.Retention(r.Context()); err == nil {
		d.Retention = &ret
	}
	h.render(w, r, "snapshots", "snapshots", d)
}

func (h *Handler) locks(w http.ResponseWriter, r *http.Request) {
	r = h.ctx(r)
	locks, err := h.svc.Locks(r.Context())
	if err != nil {
		h.fail(w, r, "locks", err)
		return
	}
	h.render(w, r, "locks", "locks", locks)
}

type accessData struct {
	Info     service.EnvInfo
	Revealed bool
	// the same variables for a shell, an env file and docker
	Export, Dotenv, Docker string
}

// render writes info in a style of the env command.
func render(info service.EnvInfo, style envexport.Style) string {
	var b strings.Builder
	_ = envexport.Write(&b, info, style) // writing to a Builder does not fail
	return b.String()
}

func (h *Handler) access(w http.ResponseWriter, r *http.Request) {
	r = h.ctx(r)
	reveal := r.URL.Query().Get("reveal") == "1"
	info, err := h.svc.Env(r.Context(), reveal)
	if err != nil {
		h.fail(w, r, "access", err)
		return
	}
	if reveal {
		h.o.Log.InfoContext(r.Context(), "restic environment revealed", "source", service.SourceFrom(r.Context()))
	}
	h.render(w, r, "access", "access", accessData{
		Info: info, Revealed: reveal,
		Export: render(info, envexport.StyleExport), Dotenv: render(info, envexport.StyleDotenv), Docker: render(info, envexport.StyleDocker),
	})
}

type logData struct {
	Kind  service.Kind
	Lines []string
	Start time.Time
}

func (h *Handler) logPage(w http.ResponseWriter, r *http.Request) {
	r = h.ctx(r)
	kind := service.Kind(r.PathValue("kind"))
	l, err := h.svc.Log(r.Context(), kind)
	if err != nil {
		h.fail(w, r, "log", err)
		return
	}
	h.render(w, r, "log", "overview", logData{Kind: l.Kind, Lines: l.Lines, Start: l.Start})
}

// ---- restore ----

type restoreForm struct {
	Snapshot string
	Target   string
	Include  string
	Merge    bool
	// Pipe: the snapshot goes to the restore script as a whole, so the form
	// has only the snapshot
	Pipe bool
}

// restoreForm shows the form filled from the query, see restoreURL.
func (h *Handler) restoreForm(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	form := restoreForm{
		Snapshot: cmp.Or(q.Get("snapshot"), "latest"), Target: q.Get("target"), Include: strings.Join(includeLines(q["include"]), "\n"),
		Merge: q.Get("mode") == string(service.ModeMerge),
	}
	if st, err := h.svc.Status(r.Context()); err == nil && st.PipeMode {
		form = restoreForm{Snapshot: form.Snapshot, Pipe: true}
	}
	h.render(w, r, "restore", "snapshots", form)
}

// restoreURL is the restore form filled with req, so that the way back from
// the check keeps what was entered.
func restoreURL(req service.RestoreRequest) string {
	q := url.Values{"snapshot": {req.Snapshot}}
	if req.Target != "" {
		q.Set("target", req.Target)
	}
	if len(req.Includes) > 0 {
		q["include"] = req.Includes
	}
	if req.Mode == service.ModeMerge {
		q.Set("mode", string(req.Mode))
	}
	return "/restore?" + q.Encode()
}

// includeLines reads include paths, one per line, so that a path may contain
// spaces. The form sends them in one field, links and hidden fields one by one.
func includeLines(values []string) []string {
	var paths []string
	for _, v := range values {
		for line := range strings.Lines(v) {
			if p := strings.TrimSpace(line); p != "" {
				paths = append(paths, p)
			}
		}
	}
	return paths
}

// restoreRequest reads the restore fields of a form.
func restoreRequest(r *http.Request) service.RestoreRequest {
	req := service.RestoreRequest{
		Snapshot: strings.TrimSpace(r.PostFormValue("snapshot")),
		Target:   strings.TrimSpace(r.PostFormValue("target")),
		Includes: includeLines(r.PostForm["include"]),
		Mode:     service.ModeExact,
	}
	if r.PostFormValue("mode") == string(service.ModeMerge) {
		req.Mode = service.ModeMerge
	}
	req.IgnoreSpaceWarning = r.PostFormValue("ignore_space") == "1"
	req.Preview = r.PostFormValue("preview") == "1"
	req.NoSafetySnapshot = r.PostFormValue("no_safety_snapshot") == "1"
	req.Confirm = strings.TrimSpace(r.PostFormValue("confirm"))
	return req
}

type restoreCheckData struct {
	Request service.RestoreRequest
	Rows    []present.Row
	Pre     service.RestorePreflight
	Back    string
}

func (h *Handler) restoreCheck(w http.ResponseWriter, r *http.Request) {
	r = h.ctx(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	req := restoreRequest(r)
	if req.Snapshot == "" {
		h.setFlash(w, r, false, "Name a snapshot, or latest.")
		http.Redirect(w, r, "/restore", http.StatusSeeOther)
		return
	}
	pre, err := h.svc.RestorePreflight(r.Context(), req)
	if err != nil {
		h.setFlash(w, r, false, describe(err))
		http.Redirect(w, r, restoreURL(req), http.StatusSeeOther)
		return
	}
	back := restoreURL(req)
	if id := pre.Snapshot.ShortID; id != "" {
		// the restore takes the snapshot that was checked, also if "latest"
		// is another one by then because a backup came in
		req.Snapshot = id
	}
	h.render(w, r, "restore_check", "snapshots", restoreCheckData{
		Request: req, Pre: pre, Rows: present.PreflightRows(pre, h.o.Now()),
		Back: back,
	})
}

func (h *Handler) restore(r *http.Request) (string, error) {
	if err := r.ParseForm(); err != nil {
		return "", errors.New("bad form")
	}
	req := restoreRequest(r)
	if req.NoSafetySnapshot && r.PostFormValue("confirm_no_safety") != "no-safety-snapshot" {
		return "", errors.New("to restore without the safety snapshot, type no-safety-snapshot")
	}
	op, err := h.svc.StartRestore(r.Context(), req)
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	h.lastRestore = &req
	h.mu.Unlock()
	return "Restore started (" + string(op.Kind) + ").", nil
}

// ---- actions ----

func (h *Handler) backup(r *http.Request) (string, error) {
	if err := r.ParseForm(); err != nil {
		return "", errors.New("bad form")
	}
	_, err := h.svc.StartBackup(r.Context(), service.BackupRequest{Note: strings.TrimSpace(r.PostFormValue("note")), Keep: r.PostFormValue("keep") == "1"})
	return "Backup started.", err
}

func (h *Handler) verify(r *http.Request) (string, error) {
	_, err := h.svc.StartVerify(r.Context(), service.VerifyRequest{})
	return "Restore test started.", err
}

func (h *Handler) prune(r *http.Request) (string, error) {
	_, err := h.svc.StartPrune(r.Context())
	return "Prune started.", err
}

func (h *Handler) check(r *http.Request) (string, error) {
	_, err := h.svc.StartCheck(r.Context())
	return "Check started.", err
}

func (h *Handler) cancel(r *http.Request) (string, error) {
	return "Cancel requested.", h.svc.Cancel(r.Context())
}

func (h *Handler) notifyTest(r *http.Request) (string, error) {
	return "Test notification sent.", h.svc.NotifyTest(r.Context())
}

func (h *Handler) schedule(r *http.Request) (string, error) {
	if err := r.ParseForm(); err != nil {
		return "", errors.New("bad form")
	}
	req := service.ScheduleRequest{Action: service.ScheduleAction(r.PostFormValue("action"))}
	switch req.Action {
	case service.ScheduleEnable, service.ScheduleDisable:
	case service.ScheduleMaintenance:
		d, err := time.ParseDuration(strings.TrimSpace(r.PostFormValue("for")))
		if err != nil || d <= 0 {
			return "", errors.New("say how long, for example 90m or 2h")
		}
		req.For = service.Duration(d)
	default:
		return "", errors.New("unknown schedule action")
	}
	_, err := h.svc.SetSchedule(r.Context(), req)
	return "Schedule changed.", err
}

func (h *Handler) approveForget(r *http.Request) (string, error) {
	if err := r.ParseForm(); err != nil {
		return "", errors.New("bad form")
	}
	_, err := h.svc.ApproveForget(r.Context(), strings.TrimSpace(r.PostFormValue("confirm")))
	return "Forget started.", err
}

func (h *Handler) keep(on bool) func(*http.Request) (string, error) {
	return func(r *http.Request) (string, error) {
		id := r.PathValue("id")
		if on {
			res, err := h.svc.Keep(r.Context(), id)
			return retagged(id, "is kept", res), err
		}
		res, err := h.svc.Unkeep(r.Context(), id)
		return retagged(id, "is no longer kept", res), err
	}
}

func (h *Handler) acknowledge(r *http.Request) (string, error) {
	if err := r.ParseForm(); err != nil {
		return "", errors.New("bad form")
	}
	id := r.PathValue("id")
	res, err := h.svc.Acknowledge(r.Context(), id, strings.TrimSpace(r.PostFormValue("confirm")))
	return retagged(id, "is a regular snapshot now", res), err
}

// retagged says what a tag change did. restic gives the snapshot a new ID
// for it, which the message names: the old one is gone from the list.
func retagged(id, state string, res service.TagResult) string {
	msg := "Snapshot " + present.Short(id) + " " + state
	if res.ID != "" && res.ID != id {
		msg += "; restic gave it the new ID " + present.Short(res.ID)
	}
	return msg + "."
}

func (h *Handler) unlock(r *http.Request) (string, error) {
	if err := r.ParseForm(); err != nil {
		return "", errors.New("bad form")
	}
	all := r.PostFormValue("all") == "1"
	if err := h.svc.Unlock(r.Context(), all, strings.TrimSpace(r.PostFormValue("confirm"))); err != nil {
		return "", err
	}
	if all {
		return "All locks removed.", nil
	}
	return "Stale locks removed.", nil
}

// webOff switches the web UI off. The answer is a page of its own, because
// there is nothing to go back to once the server is gone.
func (h *Handler) webOff(w http.ResponseWriter, r *http.Request) {
	if h.o.Off == nil {
		h.setFlash(w, r, false, describe(service.ErrUnavailable))
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.o.Log.InfoContext(h.ctx(r).Context(), "web UI switched off from the web UI", "remote", r.RemoteAddr)
	h.render(w, r, "off", "", nil)
	// Switching off waits for open requests, this one included, so it must not block here.
	go func() { _ = h.o.Off() }()
}
