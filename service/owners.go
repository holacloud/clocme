package service

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/holacloud/store"

	"github.com/holacloud/clocme/model"
)

// RequestOwner returns a user/organization with its repository listing.
// The first time it lists the owner on the host synchronously (a few API
// calls) and registers every repo in the bulk queue. Stale listings are
// served as they are and refreshed in the background.
func (s *Service) RequestOwner(ctx context.Context, host, login string) (*model.Owner, error) {
	id, err := model.OwnerID(host, login)
	if err != nil {
		return nil, err
	}
	o, err := s.owners.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return s.listOwner(ctx, strings.ToLower(host), login, id)
	}
	if time.Since(o.ListedAt) > s.opts.OwnerRefresh {
		s.refreshOwnerAsync(o.Host, o.Login, id)
	}
	return o, nil
}

// OwnerRepos returns the repo documents of a listing, aligned with o.Repos
// (nil entries for repos not registered).
func (s *Service) OwnerRepos(ctx context.Context, o *model.Owner) []*model.Repo {
	out := make([]*model.Repo, len(o.Repos))
	for i, r := range o.Repos {
		id, err := model.RepoID(o.Host, o.Login, r.Name)
		if err != nil {
			continue
		}
		out[i], _ = s.repos.Get(ctx, id)
	}
	return out
}

// Owners returns every known owner.
func (s *Service) Owners(ctx context.Context) ([]*model.Owner, error) {
	return s.owners.List(ctx)
}

func (s *Service) refreshOwnerAsync(host, login, id string) {
	s.mu.Lock()
	if s.ownerListing[id] {
		s.mu.Unlock()
		return
	}
	s.ownerListing[id] = true
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.ownerListing, id)
			s.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := s.listOwner(ctx, host, login, id); err != nil {
			log.Printf("owner %s: refresh: %v", id, err)
		}
	}()
}

func (s *Service) listOwner(ctx context.Context, host, login, id string) (*model.Owner, error) {
	s.ownerMu.Lock()
	defer s.ownerMu.Unlock()

	l, err := s.host.Owner(ctx, login, s.opts.MaxOwnerRepos)
	if err != nil {
		return nil, err
	}

	o, err := s.owners.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if o == nil {
		o = &model.Owner{Id: store.NewId(id), CreatedAt: now}
	}
	o.Host = host
	o.Login = l.Login
	o.Name = l.Name
	o.Type = l.Type
	o.AvatarURL = l.AvatarURL
	o.Bio = l.Bio
	o.Truncated = l.Truncated
	o.ListedAt = now
	o.Repos = o.Repos[:0]
	for _, r := range l.Repos {
		o.Repos = append(o.Repos, model.OwnerRepo{
			Name:        r.Name,
			Description: r.Description,
			Stars:       r.Stars,
			Archived:    r.Archived,
			PushedAt:    r.PushedAt,
			SizeKB:      r.SizeKB,
		})
	}
	if err := s.owners.Put(ctx, o); err != nil {
		return nil, err
	}

	// Enqueue the most starred first: they are the first cards people see.
	byStars := append([]model.OwnerRepo(nil), o.Repos...)
	sort.SliceStable(byStars, func(i, j int) bool { return byStars[i].Stars > byStars[j].Stars })
	for _, r := range byStars {
		if _, err := s.register(ctx, host, o.Login, r.Name, true); err != nil {
			log.Printf("owner %s: register %s: %v", id, r.Name, err)
		}
	}
	log.Printf("owner %s: listed %d repos", id, len(o.Repos))
	return o, nil
}
