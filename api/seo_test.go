package api

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/holacloud/store"

	"github.com/holacloud/clocme/model"
	"github.com/holacloud/clocme/raster"
	"github.com/holacloud/clocme/service"
	"github.com/holacloud/clocme/telegram"
)

func newTestAPI(t *testing.T, repos ...*model.Repo) *httptest.Server {
	t.Helper()
	rs := store.NewStoreMemory[model.Repo]()
	for _, r := range repos {
		if err := rs.Put(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	svc := service.New(rs, store.NewStoreMemory[model.Series](), store.NewStoreMemory[model.Owner](), nil, nil, telegram.New(telegram.Config{}), service.Options{RefreshInterval: time.Hour})
	b, err := New(svc, raster.New(), "https://cloc.example", "test")
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(b)
	t.Cleanup(s.Close)
	return s
}

func testRepo(owner, name, status, head string) *model.Repo {
	id, _ := model.RepoID("github.com", owner, name)
	return &model.Repo{
		Id: store.NewId(id), Host: "github.com", Owner: owner, Name: name,
		Status: status, HeadCommit: head, Schema: model.CurrentSchema, Commits: 10,
		CheckedAt: time.Now(), UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Latest: map[string]model.Stats{"Go": {Code: 1200}, "JSON": {Code: 99999}},
	}
}

func get(t *testing.T, url string, headers ...string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	req.RequestURI = ""
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestSitemap(t *testing.T) {
	s := newTestAPI(t,
		testRepo("fulldump", "box", model.StatusReady, "abc"),
		testRepo("someone", "pending", model.StatusPending, ""),
		testRepo("someone", "broken", model.StatusError, "def"), // failed refresh, still has a chart
		testRepo("someone", "gone", model.StatusNotFound, ""),
	)

	code, body := get(t, s.URL+"/sitemap.xml")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	var set sitemapURLSet
	if err := xml.Unmarshal([]byte(body), &set); err != nil {
		t.Fatal(err, body)
	}
	var locs []string
	for _, u := range set.URLs {
		locs = append(locs, u.Loc)
	}
	want := "https://cloc.example/ https://cloc.example/github.com/fulldump/box https://cloc.example/github.com/someone/broken"
	if got := strings.Join(locs, " "); got != want {
		t.Errorf("locs:\n got %s\nwant %s", got, want)
	}
	if set.URLs[1].LastMod != "2026-01-02T03:04:05Z" {
		t.Errorf("lastmod %q", set.URLs[1].LastMod)
	}

	code, body = get(t, s.URL+"/robots.txt")
	if code != 200 || !strings.Contains(body, "Sitemap: https://cloc.example/sitemap.xml") {
		t.Errorf("robots: %d %s", code, body)
	}
}

func TestRepoPageMeta(t *testing.T) {
	s := newTestAPI(t,
		testRepo("fulldump", "box", model.StatusReady, "abcdef0123"),
		testRepo("someone", "pending", model.StatusPending, ""),
	)
	googlebot := []string{"Accept", "text/html,application/xhtml+xml,application/signed-exchange;v=b3,application/xml;q=0.9,*/*;q=0.8", "User-Agent", "Googlebot/2.1"}

	_, body := get(t, s.URL+"/github.com/fulldump/box?top=3&x=date", googlebot...)
	for _, want := range []string{
		`<link rel="canonical" href="https://cloc.example/github.com/fulldump/box">`,
		`<meta name="description" content="Lines of code over time for fulldump/box: 1.2k lines of code across 10 commits, mostly Go.`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(body, "noindex") {
		t.Error("ready repo must be indexable")
	}
	for _, want := range []string{`<meta name="twitter:title" content="fulldump/box · lines of code · cloc.hola.cloud">`, `<meta property="og:site_name"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if raster.New().Available() {
		for _, want := range []string{
			`<meta name="twitter:card" content="summary_large_image">`,
			`<meta property="og:image" content="https://cloc.example/github.com/fulldump/box.png?v=abcdef0">`,
			`<meta property="og:image:width" content="1200">`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %s", want)
			}
		}
	} else if strings.Contains(body, "og:image") {
		t.Error("og:image without rasterizer")
	}

	_, body = get(t, s.URL+"/github.com/someone/pending", googlebot...)
	if !strings.Contains(body, `<meta name="robots" content="noindex">`) {
		t.Error("repo without chart must be noindex")
	}
}
