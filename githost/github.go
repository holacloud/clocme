// Package githost asks the git host for repository metadata before cloning:
// existence, canonical name, default branch and size.
package githost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrNotFound = errors.New("repository not found")

type RepoInfo struct {
	Owner         string
	Name          string
	DefaultBranch string
	SizeKB        int64
	Private       bool
	Fork          bool
}

type Github struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

func NewGithub(token string) *Github {
	return &Github{
		BaseURL: "https://api.github.com",
		Token:   token,
		Client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (g *Github) Info(ctx context.Context, owner, name string) (*RepoInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/%s", g.BaseURL, owner, name), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "clocme")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}

	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusUnavailableForLegalReasons:
		return nil, ErrNotFound
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("github api: status %d: %s", resp.StatusCode, b)
	}

	var body struct {
		Name          string `json:"name"`
		DefaultBranch string `json:"default_branch"`
		Size          int64  `json:"size"` // KB
		Private       bool   `json:"private"`
		Fork          bool   `json:"fork"`
		Owner         struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("github api: decode: %w", err)
	}
	if body.Private {
		return nil, ErrNotFound
	}

	return &RepoInfo{
		Owner:         body.Owner.Login,
		Name:          body.Name,
		DefaultBranch: body.DefaultBranch,
		SizeKB:        body.Size,
		Private:       body.Private,
		Fork:          body.Fork,
	}, nil
}

// OwnerListing is a user or organization with its public, non-fork,
// non-empty repositories.
type OwnerListing struct {
	Login     string
	Name      string
	Type      string
	AvatarURL string
	Bio       string
	Repos     []RepoSummary
	Truncated bool
}

type RepoSummary struct {
	Name        string
	Description string
	Stars       int
	Archived    bool
	PushedAt    time.Time
	SizeKB      int64
}

// Owner returns the profile and public repositories of a user or
// organization, at most maxRepos (0 = no limit), most recently pushed first.
func (g *Github) Owner(ctx context.Context, login string, maxRepos int) (*OwnerListing, error) {
	var profile struct {
		Login     string `json:"login"`
		Name      string `json:"name"`
		Type      string `json:"type"`
		AvatarURL string `json:"avatar_url"`
		Bio       string `json:"bio"`
	}
	if _, err := g.getJSON(ctx, fmt.Sprintf("%s/users/%s", g.BaseURL, login), &profile); err != nil {
		return nil, err
	}

	out := &OwnerListing{
		Login:     profile.Login,
		Name:      profile.Name,
		Type:      profile.Type,
		AvatarURL: profile.AvatarURL,
		Bio:       profile.Bio,
	}

	// /users/{login}/repos works for organizations too (public repos only).
	next := fmt.Sprintf("%s/users/%s/repos?type=owner&sort=pushed&per_page=100", g.BaseURL, profile.Login)
	for next != "" {
		var page []struct {
			Name        string    `json:"name"`
			Description string    `json:"description"`
			Stars       int       `json:"stargazers_count"`
			Fork        bool      `json:"fork"`
			Archived    bool      `json:"archived"`
			Private     bool      `json:"private"`
			PushedAt    time.Time `json:"pushed_at"`
			Size        int64     `json:"size"`
		}
		resp, err := g.getJSON(ctx, next, &page)
		if err != nil {
			return nil, err
		}
		for _, r := range page {
			if r.Fork || r.Private || r.Size == 0 {
				continue
			}
			if maxRepos > 0 && len(out.Repos) >= maxRepos {
				out.Truncated = true
				return out, nil
			}
			out.Repos = append(out.Repos, RepoSummary{
				Name:        r.Name,
				Description: r.Description,
				Stars:       r.Stars,
				Archived:    r.Archived,
				PushedAt:    r.PushedAt,
				SizeKB:      r.Size,
			})
		}
		next = nextLink(resp.Header.Get("Link"))
	}
	return out, nil
}

// getJSON GETs an API url and decodes the body into v.
func (g *Github) getJSON(ctx context.Context, u string, v any) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "clocme")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusUnavailableForLegalReasons:
		return nil, ErrNotFound
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("github api: status %d: %s", resp.StatusCode, b)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return nil, fmt.Errorf("github api: decode: %w", err)
	}
	return resp, nil
}

// nextLink extracts the rel="next" url of a Link header (pagination).
func nextLink(h string) string {
	for _, part := range strings.Split(h, ",") {
		segs := strings.Split(part, ";")
		if len(segs) < 2 {
			continue
		}
		for _, s := range segs[1:] {
			if strings.TrimSpace(s) == `rel="next"` {
				return strings.Trim(strings.TrimSpace(segs[0]), "<>")
			}
		}
	}
	return ""
}
