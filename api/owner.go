package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fulldump/box"

	"github.com/holacloud/clocme/chart"
	"github.com/holacloud/clocme/githost"
	"github.com/holacloud/clocme/model"
)

type langChip struct {
	Name    string  `json:"name"`
	Color   string  `json:"color"`
	Icon    string  `json:"icon,omitempty"`
	Percent float64 `json:"percent"`
}

type ownerRepoView struct {
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	Description string     `json:"description,omitempty"`
	Stars       int        `json:"stars"`
	Archived    bool       `json:"archived,omitempty"`
	PushedAt    time.Time  `json:"pushed_at"`
	Status      string     `json:"status"`
	HasChart    bool       `json:"has_chart"`
	Commits     int        `json:"commits"`
	Code        int64      `json:"code"`
	Languages   []langChip `json:"languages,omitempty"`
	Progress    any        `json:"progress,omitempty"`
}

type ownerView struct {
	Login     string          `json:"login"`
	Name      string          `json:"name,omitempty"`
	Type      string          `json:"type,omitempty"`
	AvatarURL string          `json:"avatar_url,omitempty"`
	Bio       string          `json:"bio,omitempty"`
	Slug      string          `json:"slug"`
	Truncated bool            `json:"truncated,omitempty"`
	ListedAt  time.Time       `json:"listed_at"`
	Total     int             `json:"total"`
	Ready     int             `json:"ready"`
	Code      int64           `json:"code"`
	Languages []langChip      `json:"languages,omitempty"`
	Repos     []ownerRepoView `json:"repos"`
}

type ownerData struct {
	Meta     pageMeta
	BaseURL  string
	Version  string
	View     ownerView
	ViewJSON string
}

// topLanguages turns per-language stats into the biggest code languages
// (data languages excluded) with their share of the total.
func topLanguages(stats map[string]model.Stats, n int) ([]langChip, int64) {
	chips, total := languageShares(stats, 0)
	if len(chips) > n {
		chips = chips[:n]
	}
	return chips, total
}

// languageShares returns every code language (data languages excluded) with
// its share, biggest first. Languages under minPercent are grouped as "Other"
// (last). Percentages have one decimal.
func languageShares(stats map[string]model.Stats, minPercent float64) ([]langChip, int64) {
	var total int64
	var chips []langChip
	for name, s := range stats {
		if model.DataLanguages[name] || s.Code <= 0 {
			continue
		}
		total += s.Code
		chips = append(chips, langChip{Name: name, Color: chart.LanguageColor(name), Icon: langIcon(name), Percent: float64(s.Code)})
	}
	sort.Slice(chips, func(i, j int) bool {
		if chips[i].Percent != chips[j].Percent {
			return chips[i].Percent > chips[j].Percent
		}
		return chips[i].Name < chips[j].Name
	})
	var other float64
	kept := chips[:0]
	for _, c := range chips {
		c.Percent = c.Percent * 100 / float64(total)
		if minPercent > 0 && c.Percent < minPercent {
			other += c.Percent
			continue
		}
		c.Percent = math.Round(c.Percent*10) / 10
		kept = append(kept, c)
	}
	if other > 0 {
		kept = append(kept, langChip{Name: "Other", Color: chart.LanguageColor("Other"), Percent: math.Round(other*10) / 10})
	}
	return kept, total
}

func (a *API) buildOwnerView(r *http.Request, o *model.Owner) ownerView {
	v := ownerView{
		Login:     o.Login,
		Name:      o.Name,
		Type:      o.Type,
		AvatarURL: o.AvatarURL,
		Bio:       o.Bio,
		Slug:      o.Slug(),
		Truncated: o.Truncated,
		ListedAt:  o.ListedAt,
		Total:     len(o.Repos),
	}
	aggregate := map[string]model.Stats{}
	repos := a.svc.OwnerRepos(r.Context(), o)
	for i, or := range o.Repos {
		rv := ownerRepoView{
			Name:        or.Name,
			Slug:        o.Host + "/" + o.Login + "/" + or.Name,
			Description: or.Description,
			Stars:       or.Stars,
			Archived:    or.Archived,
			PushedAt:    or.PushedAt,
			Status:      model.StatusPending,
		}
		if repo := repos[i]; repo != nil {
			rv.Status = repo.Status
			rv.HasChart = repo.HeadCommit != ""
			rv.Commits = repo.Commits
			rv.Languages, rv.Code = topLanguages(repo.Latest, 3)
			if p, ok := a.svc.Progress(repo.Id.Id); ok {
				rv.Progress = p
				rv.Status = model.StatusProcessing
			}
			if rv.HasChart {
				v.Ready++
				for lang, s := range repo.Latest {
					agg := aggregate[lang]
					agg.Add(s)
					aggregate[lang] = agg
				}
			}
		}
		v.Repos = append(v.Repos, rv)
	}
	v.Languages, v.Code = languageShares(aggregate, 1)
	return v
}

func (a *API) owner(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(box.Param(r, "host"))
	login, ext := splitFormat(box.Param(r, "owner"))
	format := negotiate(r, ext)
	opts := chart.ParseOptions(r.URL.Query())
	w.Header().Set("Vary", "Accept, Sec-Fetch-Dest")

	if !model.SupportedHosts[host] {
		a.notFound(w, r, format, opts, host+"/"+login, "Only github.com is supported for now")
		return
	}

	o, err := a.svc.RequestOwner(r.Context(), host, login)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, githost.ErrNotFound) {
			msg = "GitHub user or organization '" + login + "' not found"
		} else if !strings.HasPrefix(msg, "invalid") {
			slog.Error("list owner", "owner", host+"/"+login, "err", err)
			msg = "Could not list the repositories of '" + login + "' right now, try again later"
		}
		a.notFound(w, r, format, opts, host+"/"+login, msg)
		return
	}

	switch format {
	case FormatJSON:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(a.buildOwnerView(r, o))
	case FormatHTML:
		v := a.buildOwnerView(r, o)
		b, _ := json.Marshal(v)
		a.render(w, http.StatusOK, "owner.html", ownerData{
			Meta:     ownerMeta(v, a.baseURL+"/"+o.Slug()),
			BaseURL:  a.baseURL,
			Version:  a.version,
			View:     v,
			ViewJSON: string(b),
		})
	default:
		// An owner has no single chart: point to the repo URL.
		writeSVG(w, "no-cache", func(buf *bytes.Buffer) error {
			return chart.RenderMessage(buf, o.Login, "Add a repository to the URL: "+o.Slug()+"/<repo>", opts)
		})
	}
}

func ownerMeta(v ownerView, url string) pageMeta {
	who := v.Login
	if v.Name != "" && !strings.EqualFold(v.Name, v.Login) {
		who = v.Name + " (" + v.Login + ")"
	}
	desc := "Lines of code over time for the " + strconv.Itoa(v.Total) + " public repositories of " + who
	if v.Code > 0 {
		desc += ": " + chart.FormatCompact(v.Code) + " lines of code"
	}
	var langs []string
	for _, l := range v.Languages {
		if len(langs) == 3 || l.Name == "Other" {
			break
		}
		langs = append(langs, l.Name)
	}
	if len(langs) > 0 {
		desc += ", mostly " + strings.Join(langs, ", ")
	}
	m := pageMeta{
		Title:       v.Login + " · repositories · cloc.hola.cloud",
		Description: desc + ".",
		Canonical:   url,
		NoIndex:     v.Ready == 0,
	}
	if v.AvatarURL != "" {
		// GitHub avatars are square PNG/JPEG: "summary" card.
		m.Image = v.AvatarURL
		if strings.Contains(m.Image, "?") {
			m.Image += "&s=400"
		} else {
			m.Image += "?s=400"
		}
		m.ImageWidth, m.ImageHeight = 400, 400
		m.ImageAlt = "Avatar of " + v.Login
	}
	return m
}
