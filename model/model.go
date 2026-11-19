// Package model holds the entities shared by the analyzer, the chart renderer,
// the worker queue and the web layer.
package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/holacloud/store"
)

// Repo statuses
const (
	StatusPending    = "pending"    // known, never analyzed yet
	StatusProcessing = "processing" // a worker is on it right now
	StatusReady      = "ready"      // has a series
	StatusError      = "error"      // last attempt failed (series may still exist from a previous run)
	StatusTooBig     = "too_big"    // over the configured size limit, needs manual baseline
	StatusNotFound   = "not_found"  // does not exist or is private
)

// Stats are the counters for one language at one commit.
type Stats struct {
	Code     int64 `json:"code"`
	Comments int64 `json:"comments"`
	Blanks   int64 `json:"blanks"`
	Files    int64 `json:"files"`
}

func (s Stats) Lines() int64 { return s.Code + s.Comments + s.Blanks }

func (s *Stats) Add(o Stats) {
	s.Code += o.Code
	s.Comments += o.Comments
	s.Blanks += o.Blanks
	s.Files += o.Files
}

func (s *Stats) Sub(o Stats) {
	s.Code -= o.Code
	s.Comments -= o.Comments
	s.Blanks -= o.Blanks
	s.Files -= o.Files
}

func (s Stats) IsZero() bool { return s == Stats{} }

// Point is the snapshot of the repository at one commit of the first-parent
// history of the default branch.
type Point struct {
	Commit    string           `json:"commit"`
	Time      int64            `json:"time"` // committer time, unix seconds, forced monotonic
	Languages map[string]Stats `json:"languages"`

	Author  string `json:"author,omitempty"`
	Date    int64  `json:"date,omitempty"`    // author time, unix seconds (real, not monotonic)
	Message string `json:"message,omitempty"` // subject line, truncated
}

// CurrentSchema is bumped when Point gains data that requires re-walking
// already analyzed repos (2: commit author, date and message).
const CurrentSchema = 2

// Repo is the (small) metadata document. Kept in a cached store, so it must
// stay light: the heavy series lives in Series.
type Repo struct {
	*store.Id `json:",inline"`

	Host          string `json:"host"`
	Owner         string `json:"owner"` // display case, as reported by the host
	Name          string `json:"name"`  // display case, as reported by the host
	DefaultBranch string `json:"default_branch"`
	SizeKB        int64  `json:"size_kb"`

	Status string `json:"status"`
	Error  string `json:"error,omitempty"`

	HeadCommit string `json:"head_commit"` // last analyzed commit
	Schema     int    `json:"schema"`      // model.CurrentSchema at analysis time
	Commits    int    `json:"commits"`     // number of points in the series

	// Latest is a copy of the last point languages, handy for listings
	// without loading the whole series.
	Latest map[string]Stats `json:"latest,omitempty"`

	CreatedAt   time.Time `json:"created_at"`
	RequestedAt time.Time `json:"requested_at"` // last time somebody asked for it
	CheckedAt   time.Time `json:"checked_at"`   // last time a worker looked at the remote
	UpdatedAt   time.Time `json:"updated_at"`   // last time the series changed
}

// Series is the heavy document with one point per commit.
type Series struct {
	*store.Id `json:",inline"`
	Points    []Point `json:"points"`
}

// Slug is the canonical "host/owner/name" used in URLs.
func (r *Repo) Slug() string {
	return r.Host + "/" + r.Owner + "/" + r.Name
}

func (r *Repo) CloneURL() string {
	return "https://" + r.Host + "/" + r.Owner + "/" + r.Name + ".git"
}

var (
	reOwner = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	reName  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// SupportedHosts is the whitelist of git hosts we accept.
var SupportedHosts = map[string]bool{
	"github.com": true,
}

// RepoID builds the store id. Hosts are case-insensitive about owner/name, so
// the id is lowercased. Slashes are not allowed in disk store ids (they are
// file names), so "~" is used as separator.
func RepoID(host, owner, name string) (string, error) {
	host = strings.ToLower(host)
	if !SupportedHosts[host] {
		return "", fmt.Errorf("host '%s' is not supported", host)
	}
	if !reOwner.MatchString(owner) {
		return "", fmt.Errorf("invalid owner '%s'", owner)
	}
	if !reName.MatchString(name) || name == "." || name == ".." || strings.HasSuffix(strings.ToLower(name), ".git") {
		return "", fmt.Errorf("invalid repository name '%s'", name)
	}
	return strings.ToLower(host + "~" + owner + "~" + name), nil
}
