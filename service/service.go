// Package service owns the repository lifecycle: registering repos when they
// are requested, the refresh queue and the workers that sync + analyze them.
//
// Queue policy:
//   - Requested repos that are new or stale go to the priority queue (FIFO,
//     deduplicated).
//   - Repos discovered through an owner page go to the bulk queue, served
//     only when the priority queue is empty, so one big organization does not
//     delay somebody asking for a single repo.
//   - When both are empty, workers cycle over all known repos, picking the one
//     checked longest ago, if it was checked more than RefreshInterval ago.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/holacloud/store"

	"github.com/holacloud/clocme/githost"
	"github.com/holacloud/clocme/model"
	"github.com/holacloud/clocme/telegram"
)

// Analyzer is implemented by *analyzer.Analyzer.
type Analyzer interface {
	Sync(ctx context.Context, repoID, cloneURL, branch string) (head string, err error)
	Analyze(ctx context.Context, repoID, head string, prev []model.Point, progress func(done, total int)) ([]model.Point, error)
}

// Host is implemented by *githost.Github.
type Host interface {
	Info(ctx context.Context, owner, name string) (*githost.RepoInfo, error)
	Owner(ctx context.Context, login string, maxRepos int) (*githost.OwnerListing, error)
}

type Options struct {
	Workers         int
	RefreshInterval time.Duration
	AnalyzeTimeout  time.Duration
	MaxRepoSizeKB   int64
	MaxOwnerRepos   int           // repos listed (and enqueued) per owner, 0 = no limit
	OwnerRefresh    time.Duration // how old an owner listing can be before it is refreshed
	BaseURL         string
}

// errorRetryAfter is how long a failed repo waits before a new request
// retries it.
const errorRetryAfter = 5 * time.Minute

type Progress struct {
	Phase string `json:"phase"` // "sync" | "analyze"
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

type Service struct {
	repos    store.Storer[model.Repo]
	series   store.Storer[model.Series]
	owners   store.Storer[model.Owner]
	analyzer Analyzer
	host     Host
	tg       *telegram.Telegram
	opts     Options

	updateMu sync.Mutex // serializes read-modify-write of repo documents
	ownerMu  sync.Mutex // serializes owner listings

	mu           sync.Mutex
	queue        []string
	queued       map[string]bool
	bulk         []string
	bulkQueued   map[string]bool
	ownerListing map[string]bool // owner ids being refreshed in background
	working      map[string]bool
	progress     map[string]Progress
	wake         chan struct{}
}

func New(repos store.Storer[model.Repo], series store.Storer[model.Series], owners store.Storer[model.Owner], a Analyzer, h Host, tg *telegram.Telegram, opts Options) *Service {
	if opts.Workers < 1 {
		opts.Workers = 1
	}
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = time.Hour
	}
	if opts.AnalyzeTimeout <= 0 {
		opts.AnalyzeTimeout = 30 * time.Minute
	}
	if opts.OwnerRefresh <= 0 {
		opts.OwnerRefresh = 24 * time.Hour
	}
	return &Service{
		repos:        repos,
		series:       series,
		owners:       owners,
		analyzer:     a,
		host:         h,
		tg:           tg,
		opts:         opts,
		queued:       map[string]bool{},
		bulkQueued:   map[string]bool{},
		ownerListing: map[string]bool{},
		working:      map[string]bool{},
		progress:     map[string]Progress{},
		wake:         make(chan struct{}, 1),
	}
}

// Get returns the repo document or nil.
func (s *Service) Get(ctx context.Context, id string) (*model.Repo, error) {
	return s.repos.Get(ctx, id)
}

// Series returns the points of a repo (nil if none yet).
func (s *Service) Series(ctx context.Context, id string) ([]model.Point, error) {
	ser, err := s.series.Get(ctx, id)
	if err != nil || ser == nil {
		return nil, err
	}
	return ser.Points, nil
}

func (s *Service) Progress(id string) (Progress, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.progress[id]
	return p, ok
}

// Ready returns every repo with a chart (status ready, or a failed refresh of
// a repo that still has a series).
func (s *Service) Ready(ctx context.Context) ([]*model.Repo, error) {
	all, err := s.repos.List(ctx)
	if err != nil {
		return nil, err
	}
	ready := all[:0]
	for _, r := range all {
		if r.HeadCommit != "" && r.Status != model.StatusNotFound {
			ready = append(ready, r)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].Id.Id < ready[j].Id.Id })
	return ready, nil
}

// Recent returns the most recently updated ready repos.
func (s *Service) Recent(ctx context.Context, n int) ([]*model.Repo, error) {
	all, err := s.repos.List(ctx)
	if err != nil {
		return nil, err
	}
	ready := all[:0]
	for _, r := range all {
		if r.Status == model.StatusReady {
			ready = append(ready, r)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].RequestedAt.After(ready[j].RequestedAt) })
	if len(ready) > n {
		ready = ready[:n]
	}
	return ready, nil
}

// Request registers a repo if unknown and enqueues it (priority) if new or
// stale. It never blocks on git operations.
func (s *Service) Request(ctx context.Context, host, owner, name string) (*model.Repo, error) {
	return s.register(ctx, host, owner, name, false)
}

// register is Request with a choice of queue: bulk registrations (owner
// pages) only enqueue new repos, in the low priority queue, and do not count
// as a user request.
func (s *Service) register(ctx context.Context, host, owner, name string, bulk bool) (*model.Repo, error) {
	id, err := model.RepoID(host, owner, name)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	repo, err := s.update(ctx, id, func(r *model.Repo) error {
		if r.Host == "" { // new
			r.Host = strings.ToLower(host)
			r.Owner = owner
			r.Name = name
			r.Status = model.StatusPending
			r.CreatedAt = now
		}
		// Avoid rewriting the file on every image hit: only bump every minute.
		if !bulk && now.Sub(r.RequestedAt) > time.Minute {
			r.RequestedAt = now
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if bulk {
		if repo.Status == model.StatusPending {
			s.EnqueueBulk(id)
		}
		return repo, nil
	}

	switch repo.Status {
	case model.StatusTooBig:
		// waits for a manual baseline, see notifyTooBig
	case model.StatusPending:
		s.Enqueue(id)
	case model.StatusError:
		// failures are often transient (API limits, network): retry soon
		// when somebody asks again, not only after the refresh interval
		if now.Sub(repo.CheckedAt) > errorRetryAfter {
			s.Enqueue(id)
		}
	default:
		if now.Sub(repo.CheckedAt) > s.opts.RefreshInterval || outdated(repo) {
			s.Enqueue(id)
		}
	}
	return repo, nil
}

// Enqueue puts a repo in the priority queue (deduplicated). If it was
// waiting in the bulk queue it is promoted.
func (s *Service) Enqueue(id string) {
	s.mu.Lock()
	delete(s.bulkQueued, id) // lazily skipped when popped from s.bulk
	if !s.queued[id] && !s.working[id] {
		s.queued[id] = true
		s.queue = append(s.queue, id)
	}
	s.mu.Unlock()
	s.signal()
}

// EnqueueBulk puts a repo in the low priority queue (deduplicated).
func (s *Service) EnqueueBulk(id string) {
	s.mu.Lock()
	if !s.queued[id] && !s.bulkQueued[id] && !s.working[id] {
		s.bulkQueued[id] = true
		s.bulk = append(s.bulk, id)
	}
	s.mu.Unlock()
	s.signal()
}

func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run starts the workers and blocks until ctx is done.
func (s *Service) Run(ctx context.Context) {
	s.recoverInterrupted(ctx)

	var wg sync.WaitGroup
	for i := 0; i < s.opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.worker(ctx)
		}()
	}
	wg.Wait()
}

// recoverInterrupted re-enqueues repos that were being processed when the
// process died, and those analyzed with an older schema.
func (s *Service) recoverInterrupted(ctx context.Context) {
	all, err := s.repos.List(ctx)
	if err != nil {
		slog.Error("recover queue", "err", err)
		return
	}
	for _, r := range all {
		switch {
		case r.Status == model.StatusProcessing:
			s.Enqueue(r.Id.Id)
		case r.Status == model.StatusPending || outdated(r):
			s.EnqueueBulk(r.Id.Id)
		}
	}
}

func (s *Service) worker(ctx context.Context) {
	for {
		id := s.next(ctx)
		if id == "" {
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
			case <-time.After(30 * time.Second):
			}
			continue
		}

		s.process(ctx, id)

		s.mu.Lock()
		delete(s.working, id)
		delete(s.progress, id)
		s.mu.Unlock()
	}
}

// next pops the priority queue, then the bulk queue or, if both are empty,
// the stalest known repo.
func (s *Service) next(ctx context.Context) string {
	s.mu.Lock()
	for len(s.queue) > 0 {
		id := s.queue[0]
		s.queue = s.queue[1:]
		delete(s.queued, id)
		if s.working[id] {
			continue
		}
		s.working[id] = true
		s.mu.Unlock()
		return id
	}
	for len(s.bulk) > 0 {
		id := s.bulk[0]
		s.bulk = s.bulk[1:]
		if !s.bulkQueued[id] || s.working[id] {
			continue // promoted to the priority queue, or already running
		}
		delete(s.bulkQueued, id)
		s.working[id] = true
		s.mu.Unlock()
		return id
	}
	s.mu.Unlock()

	all, err := s.repos.List(ctx)
	if err != nil {
		slog.Error("list repos", "err", err)
		return ""
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CheckedAt.Before(all[j].CheckedAt) })

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range all {
		if r.Status == model.StatusTooBig || r.Status == model.StatusNotFound {
			continue
		}
		if time.Since(r.CheckedAt) < s.opts.RefreshInterval {
			break // sorted: the rest are fresher
		}
		if s.working[r.Id.Id] {
			continue
		}
		s.working[r.Id.Id] = true
		return r.Id.Id
	}
	return ""
}

func (s *Service) setProgress(id string, p Progress) {
	s.mu.Lock()
	s.progress[id] = p
	s.mu.Unlock()
}

func (s *Service) process(parent context.Context, id string) {
	ctx, cancel := context.WithTimeout(parent, s.opts.AnalyzeTimeout)
	defer cancel()

	started := time.Now()
	err := s.processRepo(ctx, id)
	if err != nil {
		slog.Warn("repo analysis failed", "repo", id, "err", err, "dur_ms", time.Since(started).Milliseconds())
		_, uerr := s.update(parent, id, func(r *model.Repo) error {
			r.CheckedAt = time.Now()
			if errors.Is(err, githost.ErrNotFound) {
				r.Status = model.StatusNotFound
				r.Error = ""
				return nil
			}
			r.Status = model.StatusError
			r.Error = err.Error()
			return nil
		})
		if uerr != nil {
			slog.Error("save repo error", "repo", id, "err", uerr)
		}
		return
	}
	slog.Info("repo analyzed", "repo", id, "dur_ms", time.Since(started).Milliseconds())
}

func (s *Service) processRepo(ctx context.Context, id string) error {
	repo, err := s.repos.Get(ctx, id)
	if err != nil {
		return err
	}
	if repo == nil {
		return fmt.Errorf("repo disappeared")
	}

	info, err := s.host.Info(ctx, repo.Owner, repo.Name)
	if err != nil {
		return err
	}

	repo, err = s.update(ctx, id, func(r *model.Repo) error {
		r.Owner = info.Owner
		r.Name = info.Name
		r.DefaultBranch = info.DefaultBranch
		r.SizeKB = info.SizeKB
		if s.opts.MaxRepoSizeKB > 0 && info.SizeKB > s.opts.MaxRepoSizeKB && r.HeadCommit == "" {
			r.Status = model.StatusTooBig
		} else if r.Status != model.StatusReady {
			r.Status = model.StatusProcessing
		}
		r.Error = ""
		return nil
	})
	if err != nil {
		return err
	}
	if repo.Status == model.StatusTooBig {
		s.notifyTooBig(repo)
		_, err = s.update(ctx, id, func(r *model.Repo) error { r.CheckedAt = time.Now(); return nil })
		return err
	}

	s.setProgress(id, Progress{Phase: "sync"})
	head, err := s.analyzer.Sync(ctx, id, repo.CloneURL(), repo.DefaultBranch)
	if err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	if head == repo.HeadCommit && repo.Status == model.StatusReady && !outdated(repo) {
		_, err = s.update(ctx, id, func(r *model.Repo) error { r.CheckedAt = time.Now(); return nil })
		return err
	}

	prev, err := s.Series(ctx, id)
	if err != nil {
		return err
	}

	s.setProgress(id, Progress{Phase: "analyze"})
	points, err := s.analyzer.Analyze(ctx, id, head, prev, func(done, total int) {
		s.setProgress(id, Progress{Phase: "analyze", Done: done, Total: total})
	})
	if err != nil {
		return fmt.Errorf("analyze: %w", err)
	}

	if err := s.putSeries(ctx, id, points); err != nil {
		return err
	}

	now := time.Now()
	_, err = s.update(ctx, id, func(r *model.Repo) error {
		r.Status = model.StatusReady
		r.Error = ""
		r.HeadCommit = head
		r.Schema = model.CurrentSchema
		r.Commits = len(points)
		r.Latest = nil
		if len(points) > 0 {
			r.Latest = points[len(points)-1].Languages
		}
		r.CheckedAt = now
		r.UpdatedAt = now
		return nil
	})
	return err
}

// outdated reports whether a repo was analyzed with an older Point schema
// and needs a re-walk (cheap: line counts come from the blob cache).
func outdated(r *model.Repo) bool {
	return r.HeadCommit != "" && r.Schema < model.CurrentSchema
}

func (s *Service) putSeries(ctx context.Context, id string, points []model.Point) error {
	ser, err := s.series.Get(ctx, id)
	if err != nil {
		return err
	}
	if ser == nil {
		ser = &model.Series{Id: store.NewId(id)}
	}
	ser.Points = points
	return s.series.Put(ctx, ser)
}

func (s *Service) notifyTooBig(r *model.Repo) {
	s.tg.Send(fmt.Sprintf("cloc: repo too big to analyze automatically: %s (%d MB, limit %d MB)\n%s/%s",
		r.Slug(), r.SizeKB/1024, s.opts.MaxRepoSizeKB/1024, s.opts.BaseURL, r.Slug()))
}

// update does a serialized read-modify-write of a repo document, creating it
// if it does not exist.
func (s *Service) update(ctx context.Context, id string, fn func(r *model.Repo) error) (*model.Repo, error) {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	r, err := s.repos.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		r = &model.Repo{Id: store.NewId(id)}
	}
	if err := fn(r); err != nil {
		return nil, err
	}
	if err := s.repos.Put(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}
