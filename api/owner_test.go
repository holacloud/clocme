package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/holacloud/store"

	"github.com/holacloud/clocme/githost"
	"github.com/holacloud/clocme/model"
	"github.com/holacloud/clocme/raster"
	"github.com/holacloud/clocme/service"
	"github.com/holacloud/clocme/telegram"
)

type fakeOwnerHost struct{}

func (fakeOwnerHost) Info(ctx context.Context, owner, name string) (*githost.RepoInfo, error) {
	return nil, githost.ErrNotFound
}

func (fakeOwnerHost) Owner(ctx context.Context, login string, maxRepos int) (*githost.OwnerListing, error) {
	if login != "acme" {
		return nil, githost.ErrNotFound
	}
	return &githost.OwnerListing{Login: "acme", Name: "Acme Inc", Type: "Organization", Repos: []githost.RepoSummary{
		{Name: "rocket", Description: "Goes up", Stars: 42},
		{Name: "anvil", Stars: 7},
	}}, nil
}

func TestOwnerPage(t *testing.T) {
	rs := store.NewStoreMemory[model.Repo]()
	ready := testRepo("acme", "rocket", model.StatusReady, "abcdef0123")
	rs.Put(context.Background(), ready)
	svc := service.New(rs, store.NewStoreMemory[model.Series](), store.NewStoreMemory[model.Owner](), nil, fakeOwnerHost{}, telegram.New(telegram.Config{}), service.Options{RefreshInterval: time.Hour})
	b, err := New(svc, raster.New(), "https://cloc.example", "test")
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(b)
	defer s.Close()

	code, body := get(t, s.URL+"/github.com/acme", "Accept", "text/html", "Sec-Fetch-Dest", "document")
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	for _, want := range []string{"Acme Inc", `href="/github.com/acme/rocket"`, `<link rel="canonical" href="https://cloc.example/github.com/acme">`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}

	_, body = get(t, s.URL+"/github.com/acme.json")
	var v ownerView
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err, body)
	}
	if v.Total != 2 || v.Ready != 1 || v.Code != 1200 || len(v.Languages) != 1 || v.Languages[0].Name != "Go" {
		t.Fatalf("view %+v", v)
	}
	if v.Repos[0].Name != "rocket" || !v.Repos[0].HasChart || v.Repos[1].Status != model.StatusPending {
		t.Fatalf("repos %+v", v.Repos)
	}

	// the listing registered the repo that did not exist
	if r, _ := svc.Get(context.Background(), "github.com~acme~anvil"); r == nil || r.Status != model.StatusPending {
		t.Fatalf("anvil not registered: %+v", r)
	}

	code, body = get(t, s.URL+"/github.com/nobody", "Accept", "text/html", "Sec-Fetch-Dest", "document")
	if code != 404 || !strings.Contains(body, "not found") {
		t.Fatalf("unknown owner: %d", code)
	}

	_, body = get(t, s.URL+"/sitemap.xml")
	if !strings.Contains(body, "<loc>https://cloc.example/github.com/acme</loc>") {
		t.Errorf("owner missing from sitemap: %s", body)
	}
}
