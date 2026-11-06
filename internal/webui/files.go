package webui

import (
	"cmp"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// filesData is the page that shows what a snapshot holds: one directory of
// it, or the result of a search.
type filesData struct {
	// Snapshot is the snapshot as it was asked for (an ID or "latest").
	Snapshot string
	// SnapshotID and SnapshotTime name the snapshot that was listed.
	SnapshotID   string
	SnapshotTime time.Time
	Crumbs       []crumb
	Files        []fileRow
	Truncated    bool

	// Pattern is the search; Found is set once it ran.
	Pattern    string
	OnlyThis   bool
	IgnoreCase bool
	Found      bool
	Searched   int
	Versions   []versionRow

	// Error is why the directory or the search cannot be shown.
	Error string
}

// crumb is one step of the path to the listed directory.
type crumb struct{ Name, URL string }

type fileRow struct {
	service.File
	Name string
	// Browse opens a directory, Restore the restore form for this entry.
	Browse, Restore string
}

type versionRow struct {
	service.FileVersion
	// Folder shows the directory of the file in its newest snapshot.
	Folder, Restore string
}

func filesURL(snapshot, dir string) string {
	return "/files?" + url.Values{"snapshot": {snapshot}, "path": {dir}}.Encode()
}

// files shows a directory of a snapshot, or what a search found.
func (h *Handler) files(w http.ResponseWriter, r *http.Request) {
	r = h.ctx(r)
	q := r.URL.Query()
	d := filesData{
		Snapshot: cmp.Or(strings.TrimSpace(q.Get("snapshot")), "latest"),
		Pattern:  strings.TrimSpace(q.Get("q")), OnlyThis: q.Get("only") == "1", IgnoreCase: q.Get("i") == "1",
	}
	if d.Pattern != "" {
		h.search(r, &d)
	} else {
		h.list(r, &d, q.Get("path"))
	}
	h.render(w, r, "files", "snapshots", d)
}

func (h *Handler) list(r *http.Request, d *filesData, dir string) {
	list, err := h.svc.Files(r.Context(), service.FilesRequest{Snapshot: d.Snapshot, Path: dir})
	if err != nil {
		d.Error = describe(err)
		return
	}
	// stay on the snapshot that was listed, also when "latest" moves on
	id := list.Snapshot.ID
	d.Snapshot, d.SnapshotID, d.SnapshotTime, d.Truncated = list.Snapshot.ShortID, id, list.Snapshot.Time, list.Truncated
	d.Crumbs = []crumb{{Name: "top", URL: filesURL(id, "/")}}
	at := "/"
	for name := range strings.SplitSeq(strings.Trim(list.Path, "/"), "/") {
		if name != "" {
			at = path.Join(at, name)
			d.Crumbs = append(d.Crumbs, crumb{Name: name, URL: filesURL(id, at)})
		}
	}
	for _, f := range list.Files {
		row := fileRow{File: f, Name: path.Base(f.Path), Restore: restoreURL(service.RestoreRequest{Snapshot: id, Includes: []string{f.Path}})}
		if f.Type == "dir" {
			row.Browse = filesURL(id, f.Path)
		}
		d.Files = append(d.Files, row)
	}
}

func (h *Handler) search(r *http.Request, d *filesData) {
	req := service.FindRequest{Pattern: d.Pattern, IgnoreCase: d.IgnoreCase}
	if d.OnlyThis {
		req.Snapshot = d.Snapshot
	}
	res, err := h.svc.Find(r.Context(), req)
	if err != nil {
		d.Error = describe(err)
		return
	}
	d.Found, d.Searched, d.Truncated = true, res.Searched, res.Truncated
	for _, v := range res.Versions {
		d.Versions = append(d.Versions, versionRow{
			FileVersion: v,
			Folder:      filesURL(v.Newest.ID, path.Dir(v.Path)),
			Restore:     restoreURL(service.RestoreRequest{Snapshot: v.Newest.ID, Includes: []string{v.Path}}),
		})
	}
}
