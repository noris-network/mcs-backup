package service

import (
	"slices"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
)

// Snapshot is a snapshot as shown to users.
type Snapshot struct {
	ID         string    `json:"id"`
	ShortID    string    `json:"short_id"`
	Time       time.Time `json:"time"`
	Host       string    `json:"host"`
	Paths      []string  `json:"paths"`
	Origin     Origin    `json:"origin,omitempty"`
	Note       string    `json:"note,omitempty"`
	Keep       bool      `json:"keep,omitzero"`
	Suspicious bool      `json:"suspicious,omitzero"`
	PreRestore bool      `json:"pre_restore,omitzero"`
	Tags       []string  `json:"tags,omitempty"`
	HasSummary bool      `json:"has_summary"`
	Files      int64     `json:"files,omitzero"`
	Bytes      int64     `json:"bytes,omitzero"`
	AddedBytes int64     `json:"added_bytes,omitzero"`
	CopiedFrom string    `json:"copied_from,omitempty"`
}

// FromRestic converts a restic snapshot.
func FromRestic(s restic.Snapshot) Snapshot {
	out := Snapshot{
		ID:         s.ID,
		ShortID:    s.ShortID,
		Time:       s.Time,
		Host:       s.Hostname,
		Paths:      s.Paths,
		Origin:     OriginOf(s.Tags),
		Note:       NoteOf(s.Tags),
		Keep:       s.HasTag(TagKeep),
		Suspicious: s.HasTag(TagSuspicious),
		PreRestore: s.HasTag(TagPreRestore),
		Tags:       s.Tags,
		CopiedFrom: s.Original,
	}
	if out.ShortID == "" && len(s.ID) >= 8 {
		out.ShortID = s.ID[:8]
	}
	if s.Summary != nil {
		out.HasSummary = true
		out.Files = s.Summary.TotalFilesProcessed
		out.Bytes = s.Summary.TotalBytesProcessed
		out.AddedBytes = s.Summary.DataAdded
	}
	return out
}

// OwnHosts returns the restic host names whose snapshots belong to this
// instance: its own MCS_BACKUP_HOST and, as long as such snapshots exist, the
// host name of mcs-backup 1.x.
func OwnHosts(host string, snaps []restic.Snapshot) []string {
	hosts := []string{host}
	if host != LegacyHost && slices.ContainsFunc(snaps, func(s restic.Snapshot) bool { return s.Hostname == LegacyHost }) {
		hosts = append(hosts, LegacyHost)
	}
	return hosts
}

// Own returns the snapshots taken by one of hosts, oldest first.
func Own(snaps []restic.Snapshot, hosts []string) []restic.Snapshot {
	var out []restic.Snapshot
	for _, s := range snaps {
		if slices.Contains(hosts, s.Hostname) {
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(a, b restic.Snapshot) int { return a.Time.Compare(b.Time) })
	return out
}

// Regular filters out suspicious and pre-restore snapshots.
func Regular(snaps []restic.Snapshot) []restic.Snapshot {
	return slices.DeleteFunc(slices.Clone(snaps), func(s restic.Snapshot) bool { return !IsRegular(s.Tags) })
}

// Latest returns the newest of snaps.
func Latest(snaps []restic.Snapshot) (restic.Snapshot, bool) {
	if len(snaps) == 0 {
		return restic.Snapshot{}, false
	}
	return slices.MaxFunc(snaps, func(a, b restic.Snapshot) int { return a.Time.Compare(b.Time) }), true
}
