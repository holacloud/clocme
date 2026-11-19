package githost

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestGithub(t *testing.T, h http.HandlerFunc) *Github {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	g := NewGithub("tok")
	g.BaseURL = srv.URL
	return g
}

const repoJSON = `{"name":"clocme","default_branch":"master","size":10,"owner":{"login":"holacloud"}}`

func TestRejectedTokenFallsBackToAnonymous(t *testing.T) {
	var auths []string
	g := newTestGithub(t, func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"message":"The 'holacloud' organization forbids access via a fine-grained personal access tokens if the token's lifetime is greater than 366 days."}`))
			return
		}
		w.Write([]byte(repoJSON))
	})
	info, err := g.Info(context.Background(), "holacloud", "clocme")
	if err != nil {
		t.Fatal(err)
	}
	if info.DefaultBranch != "master" || info.Owner != "holacloud" {
		t.Fatalf("info %+v", info)
	}
	if len(auths) != 2 || auths[0] != "Bearer tok" || auths[1] != "" {
		t.Fatalf("auth headers %q", auths)
	}
}

func TestRateLimitIsNotRetriedAnonymously(t *testing.T) {
	calls := 0
	g := newTestGithub(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"API rate limit exceeded for user ID 1."}`))
	})
	if _, err := g.Info(context.Background(), "a", "b"); err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls %d", calls)
	}
}

func TestNotFound(t *testing.T) {
	g := newTestGithub(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	if _, err := g.Info(context.Background(), "a", "b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestOwnerPagination(t *testing.T) {
	var srvURL string
	g := newTestGithub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/users/acme":
			w.Write([]byte(`{"login":"Acme","type":"Organization"}`))
		case r.URL.Query().Get("page") == "":
			w.Header().Set("Link", `<`+srvURL+`/users/Acme/repos?page=2>; rel="next"`)
			w.Write([]byte(`[{"name":"a","size":1},{"name":"fork","size":1,"fork":true},{"name":"empty","size":0}]`))
		default:
			w.Write([]byte(`[{"name":"b","size":1,"stargazers_count":3}]`))
		}
	})
	srvURL = g.BaseURL
	l, err := g.Owner(context.Background(), "acme", 0)
	if err != nil {
		t.Fatal(err)
	}
	if l.Login != "Acme" || len(l.Repos) != 2 || l.Repos[0].Name != "a" || l.Repos[1].Stars != 3 {
		t.Fatalf("listing %+v", l)
	}
}
