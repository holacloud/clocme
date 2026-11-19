package api

import (
	"encoding/xml"
	"log/slog"
	"net/http"
	"time"
)

// sitemapMax is the protocol limit of URLs per sitemap file.
const sitemapMax = 50000

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

type sitemapURLSet struct {
	XMLName xml.Name     `xml:"urlset"`
	Xmlns   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

// sitemap lists the home page and every repo that has a chart.
func (a *API) sitemap(w http.ResponseWriter, r *http.Request) {
	repos, err := a.svc.Ready(r.Context())
	if err != nil {
		slog.Error("sitemap", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	set := sitemapURLSet{Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	set.URLs = append(set.URLs, sitemapURL{Loc: a.baseURL + "/"})

	owners, err := a.svc.Owners(r.Context())
	if err != nil {
		slog.Error("sitemap owners", "err", err)
	}
	for _, o := range owners {
		set.URLs = append(set.URLs, sitemapURL{Loc: a.baseURL + "/" + o.Slug(), LastMod: o.ListedAt.UTC().Format(time.RFC3339)})
	}
	for _, repo := range repos {
		if len(set.URLs) >= sitemapMax {
			break
		}
		u := sitemapURL{Loc: a.baseURL + "/" + repo.Slug()}
		if !repo.UpdatedAt.IsZero() {
			u.LastMod = repo.UpdatedAt.UTC().Format(time.RFC3339)
		}
		set.URLs = append(set.URLs, u)
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(set); err != nil {
		slog.Error("sitemap encode", "err", err)
	}
}

func (a *API) robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte("User-agent: *\nAllow: /\nDisallow: /go\n\nSitemap: " + a.baseURL + "/sitemap.xml\n"))
}
