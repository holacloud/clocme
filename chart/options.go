// Package chart renders the lines-of-code evolution of a repository as a
// standalone SVG, using text/template (no chart library).
package chart

import (
	"net/url"
	"strconv"
	"strings"
)

const (
	XDate    = "date"
	XCommits = "commits"

	ModeStacked = "stacked"
	ModePercent = "percent"
	ModeLines   = "lines"

	MetricCode     = "code"
	MetricLines    = "lines"
	MetricComments = "comments"
	MetricFiles    = "files"

	ThemeAuto  = "auto"
	ThemeLight = "light"
	ThemeDark  = "dark"

	maxTop = 20
)

// Options configures a chart. Build it with DefaultOptions or ParseOptions.
type Options struct {
	Top     int      // top N languages (by value at the last point), rest grouped as "Other". 0 = all (capped at 20 + Other)
	X       string   // "date" | "commits"
	Mode    string   // "stacked" | "percent" | "lines"
	Metric  string   // "code" | "lines" | "comments" | "files"
	Include []string // force-include data languages (case-insensitive); "all" = every data language
	Exclude []string // languages to hide (case-insensitive)
	Theme   string   // "auto" | "light" | "dark"
	Width   int      // 300..2000
	Height  int      // 200..1200
	Title   bool
	Legend  bool

	// Interactive embeds the chart geometry (data-layout) and scopes the
	// CSS so the SVG can be inlined in a page with a hover tooltip.
	Interactive bool
}

func DefaultOptions() Options {
	return Options{
		Top:    8,
		X:      XDate,
		Mode:   ModeStacked,
		Metric: MetricCode,
		Theme:  ThemeAuto,
		Width:  800,
		Height: 400,
		Title:  true,
		Legend: true,
	}
}

// ParseOptions reads options from a query string. It never fails: invalid
// values fall back to defaults, out of range sizes are clamped.
func ParseOptions(q url.Values) Options {
	o := DefaultOptions()

	if v := q.Get("top"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			o.Top = min(n, maxTop)
		}
	}

	switch strings.ToLower(strings.TrimSpace(q.Get("x"))) {
	case "date", "time", "dates":
		o.X = XDate
	case "commits", "commit", "seq", "sequence":
		o.X = XCommits
	}

	switch strings.ToLower(strings.TrimSpace(q.Get("mode"))) {
	case "stacked", "stack", "area":
		o.Mode = ModeStacked
	case "percent", "percentage", "100", "normalized":
		o.Mode = ModePercent
	case "lines", "line":
		o.Mode = ModeLines
	}

	switch strings.ToLower(strings.TrimSpace(q.Get("metric"))) {
	case "code":
		o.Metric = MetricCode
	case "lines", "total":
		o.Metric = MetricLines
	case "comments", "comment":
		o.Metric = MetricComments
	case "files", "file":
		o.Metric = MetricFiles
	}

	o.Include = parseList(q["include"])
	o.Exclude = parseList(q["exclude"])

	switch strings.ToLower(strings.TrimSpace(q.Get("theme"))) {
	case "auto":
		o.Theme = ThemeAuto
	case "light":
		o.Theme = ThemeLight
	case "dark":
		o.Theme = ThemeDark
	}

	o.Width = parseSize(q.Get("w"), o.Width, 300, 2000)
	o.Height = parseSize(q.Get("h"), o.Height, 200, 1200)
	o.Title = parseBool(q.Get("title"), o.Title)
	o.Legend = parseBool(q.Get("legend"), o.Legend)
	o.Interactive = parseBool(q.Get("interactive"), false)

	return o
}

// Query returns only the values that differ from the defaults, suitable to
// build short embed URLs.
func (o Options) Query() url.Values {
	d := DefaultOptions()
	q := url.Values{}
	if o.Top != d.Top {
		q.Set("top", strconv.Itoa(o.Top))
	}
	if o.X != d.X && o.X != "" {
		q.Set("x", o.X)
	}
	if o.Mode != d.Mode && o.Mode != "" {
		q.Set("mode", o.Mode)
	}
	if o.Metric != d.Metric && o.Metric != "" {
		q.Set("metric", o.Metric)
	}
	if len(o.Include) > 0 {
		q.Set("include", strings.Join(o.Include, ","))
	}
	if len(o.Exclude) > 0 {
		q.Set("exclude", strings.Join(o.Exclude, ","))
	}
	if o.Theme != d.Theme && o.Theme != "" {
		q.Set("theme", o.Theme)
	}
	if o.Width != d.Width && o.Width != 0 {
		q.Set("w", strconv.Itoa(o.Width))
	}
	if o.Height != d.Height && o.Height != 0 {
		q.Set("h", strconv.Itoa(o.Height))
	}
	if !o.Title {
		q.Set("title", "0")
	}
	if !o.Legend {
		q.Set("legend", "0")
	}
	if o.Interactive {
		q.Set("interactive", "1")
	}
	return q
}

func parseList(values []string) []string {
	var result []string
	for _, v := range values {
		for _, item := range strings.Split(v, ",") {
			item = strings.TrimSpace(item)
			if item != "" {
				result = append(result, item)
			}
		}
	}
	return result
}

func parseSize(v string, def, lo, hi int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return max(lo, min(hi, n))
}

func parseBool(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return false
	case "1", "true", "yes", "on":
		return true
	}
	return def
}
