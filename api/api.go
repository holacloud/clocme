// Package api is the HTTP layer: the same repo URL answers with an SVG chart
// (for READMEs) or with an HTML page (for humans), see negotiate.go.
package api

import (
	"bytes"
	"context"
	"crypto/sha1"
	"embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fulldump/box"

	"github.com/holacloud/clocme/chart"
	"github.com/holacloud/clocme/model"
	"github.com/holacloud/clocme/service"
)

//go:embed static
var staticFS embed.FS

//go:embed templates/*.html
var templatesFS embed.FS

type API struct {
	svc       *service.Service
	baseURL   string
	version   string
	templates *template.Template
}

func New(svc *service.Service, baseURL, version string) (*box.B, error) {
	a := &API{
		svc:     svc,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		version: version,
	}

	t, err := template.New("").Funcs(template.FuncMap{
		"compact": chart.FormatCompact,
		"since":   since,
		"short": func(sha string) string {
			if len(sha) > 7 {
				return sha[:7]
			}
			return sha
		},
	}).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	a.templates = t

	b := box.NewBox()
	// box serializes the nil "return" of plain http.HandlerFuncs as a 204,
	// which clashes with handlers that already wrote their response.
	b.Serializer = func(ctx context.Context, w io.Writer, v any) error {
		if v == nil {
			return nil
		}
		return box.DefaultSerialize(ctx, w, v)
	}
	b.Use(accessLog)
	b.Use(box.RecoverFromPanic)

	b.HandleFunc("GET", "/", a.home)
	b.HandleFunc("GET", "/go", a.goTo)
	b.HandleFunc("GET", "/sitemap.xml", a.sitemap)
	b.HandleFunc("GET", "/robots.txt", a.robots)
	b.HandleFunc("GET", "/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	static, _ := fs.Sub(staticFS, "static")
	staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(static)))
	b.HandleFunc("GET", "/static/*", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		staticHandler.ServeHTTP(w, r)
	})

	b.HandleFunc("GET", "/{host}/{owner}/{repo}", a.repo)

	return b, nil
}

func accessLog(next box.H) box.H {
	return func(ctx context.Context) {
		r := box.GetRequest(ctx)
		start := time.Now()
		next(ctx)
		log.Printf("%s %s %s dest=%q (%s)", r.Method, r.URL.RequestURI(), r.Header.Get("User-Agent"), r.Header.Get("Sec-Fetch-Dest"), time.Since(start).Round(time.Microsecond))
	}
}

func (a *API) render(w http.ResponseWriter, status int, name string, data any) {
	buf := &bytes.Buffer{}
	if err := a.templates.ExecuteTemplate(buf, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

// pageMeta feeds the <head> of every page (title, SEO tags).
type pageMeta struct {
	Title       string
	Description string
	Canonical   string
	NoIndex     bool
}

type homeData struct {
	Meta    pageMeta
	BaseURL string
	Version string
	Recent  []*model.Repo
	Error   string
	Input   string
}

func (a *API) homeMeta(noIndex bool) pageMeta {
	return pageMeta{
		Title:       "cloc.hola.cloud · lines of code history for your README",
		Description: "Chart the lines of code of any public GitHub repository, per language, commit after commit, and embed it in your README with one line.",
		Canonical:   a.baseURL + "/",
		NoIndex:     noIndex,
	}
}

func (a *API) home(w http.ResponseWriter, r *http.Request) {
	recent, err := a.svc.Recent(r.Context(), 12)
	if err != nil {
		log.Println("recent:", err)
	}
	a.render(w, http.StatusOK, "home.html", homeData{Meta: a.homeMeta(false), BaseURL: a.baseURL, Version: a.version, Recent: recent})
}

// goTo receives the home form (?repo=...) and redirects to the repo page.
func (a *API) goTo(w http.ResponseWriter, r *http.Request) {
	input := r.URL.Query().Get("repo")
	slug, err := parseRepoInput(input)
	if err != nil {
		recent, _ := a.svc.Recent(r.Context(), 12)
		a.render(w, http.StatusBadRequest, "home.html", homeData{Meta: a.homeMeta(true), BaseURL: a.baseURL, Version: a.version, Recent: recent, Error: err.Error(), Input: input})
		return
	}
	http.Redirect(w, r, "/"+slug, http.StatusSeeOther)
}

var reRepoInput = regexp.MustCompile(`^(?:(?:https?://)?(?:www\.)?github\.com[/:]|git@github\.com:)?([A-Za-z0-9-]+)/([A-Za-z0-9._-]+?)(?:\.git)?(?:/.*)?$`)

func parseRepoInput(s string) (string, error) {
	s = strings.TrimSpace(s)
	m := reRepoInput.FindStringSubmatch(s)
	if m == nil {
		return "", errBadInput
	}
	if _, err := model.RepoID("github.com", m[1], m[2]); err != nil {
		return "", err
	}
	return "github.com/" + m[1] + "/" + m[2], nil
}

type inputError string

func (e inputError) Error() string { return string(e) }

const errBadInput = inputError("That doesn't look like a GitHub repository. Try github.com/owner/repo")

func (a *API) repo(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(box.Param(r, "host"))
	owner := box.Param(r, "owner")
	name, ext := splitFormat(box.Param(r, "repo"))
	format := negotiate(r, ext)
	opts := chart.ParseOptions(r.URL.Query())

	w.Header().Set("Vary", "Accept, Sec-Fetch-Dest")

	if !model.SupportedHosts[host] {
		a.notFound(w, r, format, opts, host+"/"+owner+"/"+name, "Only github.com repositories are supported for now")
		return
	}

	repo, err := a.svc.Request(r.Context(), host, owner, name)
	if err != nil {
		a.notFound(w, r, format, opts, host+"/"+owner+"/"+name, err.Error())
		return
	}

	switch format {
	case FormatJSON:
		a.repoJSON(w, r, repo)
	case FormatHTML:
		a.repoHTML(w, r, repo, opts)
	default:
		a.repoSVG(w, r, repo, opts)
	}
}

func (a *API) notFound(w http.ResponseWriter, r *http.Request, format string, opts chart.Options, slug, msg string) {
	switch format {
	case FormatJSON:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": msg})
	case FormatHTML:
		recent, _ := a.svc.Recent(r.Context(), 12)
		a.render(w, http.StatusNotFound, "home.html", homeData{Meta: a.homeMeta(true), BaseURL: a.baseURL, Version: a.version, Recent: recent, Error: msg, Input: slug})
	default:
		// 200 so that image proxies display the message instead of a broken image.
		writeSVG(w, "no-cache", func(buf *bytes.Buffer) error { return chart.RenderMessage(buf, slug, msg, opts) })
	}
}

func chartTitle(repo *model.Repo) string {
	return repo.Owner + "/" + repo.Name
}

func (a *API) repoSVG(w http.ResponseWriter, r *http.Request, repo *model.Repo, opts chart.Options) {
	title := chartTitle(repo)

	if repo.HeadCommit == "" { // nothing to draw yet
		var msg string
		switch repo.Status {
		case model.StatusNotFound:
			msg = "Repository not found (or private)"
		case model.StatusTooBig:
			msg = "This repository is huge. We're working on it, check back later!"
		case model.StatusError:
			msg = "Something went wrong analyzing this repository. We'll retry."
		default:
			msg = "Counting lines of code… reload in a minute"
		}
		writeSVG(w, "no-cache, no-store, must-revalidate", func(buf *bytes.Buffer) error {
			return chart.RenderMessage(buf, title, msg, opts)
		})
		return
	}

	// Cheap conditional request: same head + same options = same image.
	h := sha1.Sum([]byte(repo.HeadCommit + "|" + opts.Query().Encode() + "|" + repo.Owner + "/" + repo.Name))
	etag := `"` + hex.EncodeToString(h[:10]) + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	points, err := a.svc.Series(r.Context(), repo.Id.Id)
	if err != nil {
		log.Printf("series %s: %v", repo.Id.Id, err)
		writeSVG(w, "no-cache", func(buf *bytes.Buffer) error {
			return chart.RenderMessage(buf, title, "Temporary error, try again later", opts)
		})
		return
	}

	writeSVG(w, "public, max-age=1800", func(buf *bytes.Buffer) error {
		return chart.Render(buf, title+" · lines of code", points, opts)
	})
}

func writeSVG(w http.ResponseWriter, cacheControl string, fn func(buf *bytes.Buffer) error) {
	buf := &bytes.Buffer{}
	if err := fn(buf); err != nil {
		log.Println("render svg:", err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", cacheControl)
	w.Write(buf.Bytes())
}

type repoJSON struct {
	Repo     *model.Repo       `json:"repo"`
	Progress *service.Progress `json:"progress,omitempty"`
	Points   []model.Point     `json:"points,omitempty"`
}

func (a *API) repoJSON(w http.ResponseWriter, r *http.Request, repo *model.Repo) {
	out := repoJSON{Repo: repo}
	if p, ok := a.svc.Progress(repo.Id.Id); ok {
		out.Progress = &p
	}
	if r.URL.Query().Get("points") != "0" {
		points, err := a.svc.Series(r.Context(), repo.Id.Id)
		if err != nil {
			http.Error(w, "series error", http.StatusInternalServerError)
			return
		}
		out.Points = points
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(out)
}

type langRow struct {
	Name    string
	Stats   model.Stats
	Percent float64
	Data    bool
}

type repoData struct {
	Meta      pageMeta
	BaseURL   string
	Version   string
	Repo      *model.Repo
	Slug      string
	PageURL   string
	ImageURL  string // absolute, with current options
	Markdown  string
	Languages []langRow
	Total     model.Stats
	Options   chart.Options
	OptsJSON  template.JS
}

func (a *API) repoHTML(w http.ResponseWriter, r *http.Request, repo *model.Repo, opts chart.Options) {
	slug := repo.Slug()
	pageURL := a.baseURL + "/" + slug

	imageURL := pageURL
	if q := opts.Query().Encode(); q != "" {
		imageURL += "?" + q
	}

	data := repoData{
		BaseURL:  a.baseURL,
		Version:  a.version,
		Repo:     repo,
		Slug:     slug,
		PageURL:  pageURL,
		ImageURL: imageURL,
		Markdown: "[![Lines of code](" + imageURL + ")](" + pageURL + ")",
		Options:  opts,
	}

	for name, s := range repo.Latest {
		data.Languages = append(data.Languages, langRow{Name: name, Stats: s, Data: model.DataLanguages[name]})
		if !model.DataLanguages[name] {
			data.Total.Add(s)
		}
	}
	sort.Slice(data.Languages, func(i, j int) bool {
		if data.Languages[i].Data != data.Languages[j].Data {
			return !data.Languages[i].Data
		}
		return data.Languages[i].Stats.Code > data.Languages[j].Stats.Code
	})
	for i := range data.Languages {
		if data.Total.Code > 0 && !data.Languages[i].Data {
			data.Languages[i].Percent = 100 * float64(data.Languages[i].Stats.Code) / float64(data.Total.Code)
		}
	}

	data.Meta = repoMeta(repo, pageURL, data.Total, data.Languages)

	oj, _ := json.Marshal(map[string]any{
		"slug":     slug,
		"pageURL":  pageURL,
		"status":   repo.Status,
		"hasChart": repo.HeadCommit != "",
		"query":    opts.Query().Encode(),
		"defaults": chart.DefaultOptions().Query().Encode(),
	})
	data.OptsJSON = template.JS(oj)

	a.render(w, http.StatusOK, "repo.html", data)
}

func repoMeta(repo *model.Repo, pageURL string, total model.Stats, langs []langRow) pageMeta {
	name := repo.Owner + "/" + repo.Name
	m := pageMeta{
		Title:     name + " · lines of code · cloc.hola.cloud",
		Canonical: pageURL, // without chart options: one indexed page per repo
		NoIndex:   repo.HeadCommit == "",
	}
	if repo.HeadCommit == "" {
		m.Description = "Lines of code over time for " + name + "."
		return m
	}
	var top []string
	for _, l := range langs {
		if l.Data || len(top) == 3 {
			continue
		}
		top = append(top, l.Name)
	}
	m.Description = "Lines of code over time for " + name + ": " + chart.FormatCompact(total.Code) +
		" lines of code across " + strconv.Itoa(repo.Commits) + " commits"
	if len(top) > 0 {
		m.Description += ", mostly " + strings.Join(top, ", ")
	}
	m.Description += ". Embeddable chart for your README."
	return m
}

func since(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute") + " ago"
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "hour") + " ago"
	default:
		return plural(int(d.Hours()/24), "day") + " ago"
	}
}

func plural(n int, unit string) string {
	s := strconv.Itoa(n) + " " + unit
	if n != 1 {
		s += "s"
	}
	return s
}
