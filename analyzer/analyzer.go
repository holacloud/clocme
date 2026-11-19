// Package analyzer clones git repositories and computes, for every commit of
// the first-parent history of a branch, the lines of code per language.
//
// The heavy lifting is incremental:
//   - git is driven through long running processes (diff-tree --stdin and
//     cat-file --batch-command), never one process per commit;
//   - every blob is counted once per detection key (blobs are content
//     addressed) and the result is cached on disk across runs;
//   - totals are updated with the per-commit diff instead of walking the tree.
//
// Concurrency: an Analyzer can be used from several goroutines. Operations on
// the same repoID are serialized. scc's global configuration is set once and
// only read afterwards, so different repos can be analyzed in parallel.
package analyzer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/holacloud/clocme/model"
)

type Config struct {
	ReposDir     string // bare clones live in ReposDir/<repoID>.git
	CacheDir     string // blob count cache per repo: CacheDir/<repoID>.gob
	MaxBlobBytes int64  // blobs bigger than this are not counted (default 1MB if 0)
}

type Analyzer struct {
	config Config

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func New(c Config) *Analyzer {
	if c.MaxBlobBytes <= 0 {
		c.MaxBlobBytes = 1 << 20
	}
	if c.ReposDir == "" {
		c.ReposDir = "repos"
	}
	if c.CacheDir == "" {
		c.CacheDir = "cache"
	}
	return &Analyzer{config: c, locks: map[string]*sync.Mutex{}}
}

// lock serializes operations on the same repo.
func (a *Analyzer) lock(repoID string) func() {
	a.mu.Lock()
	l, ok := a.locks[repoID]
	if !ok {
		l = &sync.Mutex{}
		a.locks[repoID] = l
	}
	a.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func validID(repoID string) error {
	if repoID == "" || repoID == "." || repoID == ".." || strings.ContainsAny(repoID, `/\`) || strings.HasPrefix(repoID, "-") {
		return fmt.Errorf("invalid repo id '%s'", repoID)
	}
	return nil
}

func (a *Analyzer) repoDir(repoID string) string {
	return filepath.Join(a.config.ReposDir, repoID+".git")
}

func (a *Analyzer) cacheFile(repoID string) string {
	return filepath.Join(a.config.CacheDir, repoID+".gob")
}

// Sync clones (first time) or fetches (subsequent times) the branch of the
// repo into ReposDir/<repoID>.git and returns the commit sha of the branch.
func (a *Analyzer) Sync(ctx context.Context, repoID, cloneURL, branch string) (string, error) {
	if err := validID(repoID); err != nil {
		return "", err
	}
	if branch == "" || strings.HasPrefix(branch, "-") {
		return "", fmt.Errorf("invalid branch '%s'", branch)
	}
	if _, err := runGit(ctx, "", "check-ref-format", "--branch", branch); err != nil {
		return "", fmt.Errorf("invalid branch '%s'", branch)
	}
	if strings.HasPrefix(cloneURL, "-") {
		return "", fmt.Errorf("invalid clone url '%s'", cloneURL)
	}

	defer a.lock(repoID)()

	dir := a.repoDir(repoID)
	ref := "refs/heads/" + branch

	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(a.config.ReposDir, 0o755); err != nil {
			return "", err
		}
		// Clone into a temp dir and rename, so an interrupted clone never
		// leaves a half baked repo behind.
		tmp, err := os.MkdirTemp(a.config.ReposDir, repoID+".tmp-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(tmp)
		if _, err := runGit(ctx, "", "clone", "--bare", "--single-branch", "--branch", branch, "--no-tags", "--", cloneURL, tmp); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, dir); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else {
		// Fetch from the given URL (not "origin") so renamed repos keep
		// working; force update because history may have been rewritten.
		if _, err := runGit(ctx, dir, "fetch", "--no-tags", "--force", "--update-head-ok", "--", cloneURL, "+"+ref+":"+ref); err != nil {
			return "", err
		}
	}

	out, err := runGit(ctx, dir, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Remove deletes the clone and the cache of a repo.
func (a *Analyzer) Remove(repoID string) error {
	if err := validID(repoID); err != nil {
		return err
	}
	defer a.lock(repoID)()
	return errors.Join(
		os.RemoveAll(a.repoDir(repoID)),
		os.RemoveAll(a.cacheFile(repoID)),
	)
}

// Analyze returns one point per commit of the first-parent history of head,
// oldest first. If prev is a prefix of that history, only new commits are
// processed; otherwise (e.g. after a force push) everything is recomputed.
func (a *Analyzer) Analyze(ctx context.Context, repoID, head string, prev []model.Point, progress func(done, total int)) ([]model.Point, error) {
	if err := validID(repoID); err != nil {
		return nil, err
	}
	sccInit()
	defer a.lock(repoID)()

	dir := a.repoDir(repoID)
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("repo not synced: %w", err)
	}

	commits, err := firstParentLog(ctx, dir, head)
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		return nil, nil
	}

	cache, err := loadCache(a.cacheFile(repoID))
	if err != nil {
		return nil, err
	}

	cat, err := startCatFile(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer cat.close()

	w := &walker{
		cat:     cat,
		cache:   cache,
		maxBlob: a.config.MaxBlobBytes,
		files:   map[string]*fileInfo{},
		totals:  map[string]model.Stats{},
	}

	start := 0
	var points []model.Point
	if isPrefix(prev, commits) {
		start = len(prev)
		points = make([]model.Point, len(prev), len(commits))
		copy(points, prev)
		if err := w.loadTree(ctx, dir, prev[len(prev)-1].Commit); err != nil {
			return nil, err
		}
	} else {
		points = make([]model.Point, 0, len(commits))
	}

	if start < len(commits) {
		if err := w.walk(ctx, dir, commits[start:], &points, len(commits), progress); err != nil {
			return nil, err
		}
	}

	// Commit metadata is (re)filled for every point: this also backfills
	// series computed before these fields existed.
	for i := range points {
		c := commits[i]
		points[i].Author = c.author
		points[i].Date = c.authorTime
		points[i].Message = c.subject
	}

	if err := cache.save(a.cacheFile(repoID)); err != nil {
		return nil, fmt.Errorf("saving cache: %w", err)
	}
	if progress != nil {
		progress(len(commits), len(commits))
	}
	return points, nil
}

// isPrefix reports whether prev is a non-empty prefix of commits.
func isPrefix(prev []model.Point, commits []commit) bool {
	if len(prev) == 0 || len(prev) > len(commits) {
		return false
	}
	for i := range prev {
		if prev[i].Commit != commits[i].sha {
			return false
		}
	}
	return true
}
