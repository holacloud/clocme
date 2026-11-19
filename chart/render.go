package chart

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"math"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/holacloud/clocme/model"
)

const (
	pad          = 16.0
	fontFamily   = `-apple-system,BlinkMacSystemFont,'Segoe UI','Noto Sans',Helvetica,Arial,sans-serif`
	footerText   = "cloc.hola.cloud"
	charWidth12  = 6.1 // average glyph width at 12px for the system-ui stack
	charWidth11  = 5.7
	legendRowH   = 18.0
	legendSwatch = 10.0
)

type palette struct {
	Bg, Border, Text, Muted, Grid, Axis string
}

var (
	lightPalette = palette{Bg: "#ffffff", Border: "#d0d7de", Text: "#1f2328", Muted: "#656d76", Grid: "#eaeef2", Axis: "#afb8c1"}
	darkPalette  = palette{Bg: "#0d1117", Border: "#30363d", Text: "#e6edf3", Muted: "#8d96a0", Grid: "#21262d", Axis: "#484f58"}
)

// paletteCSS builds the color rules. pfx scopes every selector (e.g.
// "#cloc-chart ") so an inlined SVG does not leak styles into the page.
func paletteCSS(p palette, pfx string) string {
	return pfx + ".bg{fill:" + p.Bg + ";stroke:" + p.Border + "}" +
		pfx + ".t{fill:" + p.Text + "}" +
		pfx + ".m{fill:" + p.Muted + "}" +
		pfx + ".g{stroke:" + p.Grid + "}" +
		pfx + ".a{stroke:" + p.Axis + "}" +
		pfx + ".s{stroke:" + p.Bg + "}" +
		pfx + ".gh{fill:" + p.Grid + "}"
}

func styleFor(theme, pfx string) string {
	base := pfx + "text{font-family:" + fontFamily + "}"
	switch theme {
	case ThemeDark:
		return base + paletteCSS(darkPalette, pfx)
	case ThemeLight:
		return base + paletteCSS(lightPalette, pfx)
	default:
		return base + paletteCSS(lightPalette, pfx) +
			"@media (prefers-color-scheme:dark){" + paletteCSS(darkPalette, pfx) + "}"
	}
}

// InteractiveID is the id of the root <svg> element of interactive charts.
const InteractiveID = "cloc-chart"

// Layout is the chart geometry embedded as JSON in the data-layout attribute
// of interactive charts, so a script can map mouse positions to commits and
// values to pixels.
type Layout struct {
	Width  int           `json:"width"`
	Height int           `json:"height"`
	Plot   [4]float64    `json:"plot"`   // left, top, right, bottom (SVG units)
	X      string        `json:"x"`      // "date" (domain = monotonic unix times) | "commits" (domain = point indexes)
	Domain [2]int64      `json:"domain"` // x value at plot left and right
	YMax   float64       `json:"ymax"`   // value at plot top (100 in percent mode)
	Mode   string        `json:"mode"`
	Metric string        `json:"metric"`
	Groups []LayoutGroup `json:"groups"` // stack order, bottom first
}

type LayoutGroup struct {
	Name  string   `json:"name"`
	Color string   `json:"color"`
	Langs []string `json:"langs"`
}

type textItem struct {
	X, Y   string
	Text   string
	Anchor string
}

type lineItem struct {
	X1, Y1, X2, Y2 string
}

type legendItem struct {
	X, Y, TX, TY string
	Color        string
	Name, Value  string
}

type bandItem struct {
	Name  string
	Color string
	Path  string
}

type view struct {
	W, H        int
	InnerW      int
	InnerH      int
	Style       string
	Title       string
	ShowTitle   bool
	TitleY      string
	Legend      []legendItem
	Grid        []lineItem
	YLabels     []textItem
	XLabels     []textItem
	Baseline    lineItem
	Bands       []bandItem
	Lines       bool
	StrokeWidth string
	Message     string
	MessageX    string
	MessageY    string
	Ghost       string
	FooterX     string
	FooterY     string
	Footer      string
	PointsInfo  string
	ID          string
	Layout      string
}

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

var svgTemplate = template.Must(template.New("svg").Funcs(template.FuncMap{"x": xmlEscape}).Parse(
	`<svg xmlns="http://www.w3.org/2000/svg" width="{{.W}}" height="{{.H}}" viewBox="0 0 {{.W}} {{.H}}" role="img" aria-label="{{x .Title}}"{{if .ID}} id="{{.ID}}"{{end}}{{if .Layout}} data-layout="{{x .Layout}}"{{end}}>
<title>{{x .Title}}</title>
<style>{{.Style}}</style>
<rect class="bg" x="0.5" y="0.5" width="{{.InnerW}}" height="{{.InnerH}}" rx="6" stroke-width="1"/>
{{- if .ShowTitle}}
<text class="t" x="16" y="{{.TitleY}}" font-size="15" font-weight="600">{{x .Title}}</text>
{{- end}}
{{- if .Ghost}}
<path class="gh" d="{{.Ghost}}" opacity="0.7"/>
{{- end}}
{{- if .Message}}
<text class="m" x="{{.MessageX}}" y="{{.MessageY}}" font-size="14" text-anchor="middle">{{x .Message}}</text>
{{- end}}
{{- if .Legend}}
<g font-size="12">
{{- range .Legend}}
<rect x="{{.X}}" y="{{.Y}}" width="10" height="10" rx="2" fill="{{.Color}}"/><text x="{{.TX}}" y="{{.TY}}"><tspan class="t">{{x .Name}}</tspan> <tspan class="m">{{x .Value}}</tspan></text>
{{- end}}
</g>
{{- end}}
{{- if .Grid}}
<g class="g" stroke-width="1">
{{- range .Grid}}
<line x1="{{.X1}}" y1="{{.Y1}}" x2="{{.X2}}" y2="{{.Y2}}"/>
{{- end}}
</g>
{{- end}}
{{- if .Bands}}
{{- if .Lines}}
<g fill="none" stroke-width="{{.StrokeWidth}}" stroke-linejoin="round" stroke-linecap="round">
{{- range .Bands}}
<path d="{{.Path}}" stroke="{{.Color}}"><title>{{x .Name}}</title></path>
{{- end}}
</g>
{{- else}}
<g class="s" stroke-width="0.6" stroke-linejoin="round">
{{- range .Bands}}
<path d="{{.Path}}" fill="{{.Color}}"><title>{{x .Name}}</title></path>
{{- end}}
</g>
{{- end}}
{{- end}}
{{- if .Grid}}
<line class="a" x1="{{.Baseline.X1}}" y1="{{.Baseline.Y1}}" x2="{{.Baseline.X2}}" y2="{{.Baseline.Y2}}" stroke-width="1"/>
{{- end}}
{{- if .YLabels}}
<g class="m" font-size="11" text-anchor="end">
{{- range .YLabels}}
<text x="{{.X}}" y="{{.Y}}">{{x .Text}}</text>
{{- end}}
</g>
{{- end}}
{{- if .XLabels}}
<g class="m" font-size="11">
{{- range .XLabels}}
<text x="{{.X}}" y="{{.Y}}" text-anchor="{{.Anchor}}">{{x .Text}}</text>
{{- end}}
</g>
{{- end}}
<text class="m" x="{{.FooterX}}" y="{{.FooterY}}" font-size="10" text-anchor="end" opacity="0.8">{{.Footer}}</text>
</svg>
`))

func newView(title string, o Options) *view {
	o = sanitize(o)
	id, pfx := "", ""
	if o.Interactive {
		id, pfx = InteractiveID, "#"+InteractiveID+" "
	}
	return &view{
		ID:        id,
		W:         o.Width,
		H:         o.Height,
		InnerW:    o.Width - 1,
		InnerH:    o.Height - 1,
		Style:     styleFor(o.Theme, pfx),
		Title:     title,
		ShowTitle: o.Title && title != "",
		TitleY:    fmtCoord(pad + 13),
		Footer:    footerText,
		FooterX:   fmtCoord(float64(o.Width) - pad + 4),
		FooterY:   fmtCoord(float64(o.Height) - 8),
	}
}

// sanitize fixes zero/invalid options for callers that build Options by hand.
func sanitize(o Options) Options {
	d := DefaultOptions()
	if o.Width == 0 {
		o.Width = d.Width
	}
	if o.Height == 0 {
		o.Height = d.Height
	}
	o.Width = max(300, min(2000, o.Width))
	o.Height = max(200, min(1200, o.Height))
	if o.X != XCommits {
		o.X = XDate
	}
	switch o.Mode {
	case ModePercent, ModeLines:
	default:
		o.Mode = ModeStacked
	}
	switch o.Theme {
	case ThemeLight, ThemeDark:
	default:
		o.Theme = ThemeAuto
	}
	return o
}

// RenderMessage writes a placeholder card with the title and a message.
func RenderMessage(w io.Writer, title, message string, o Options) error {
	o = sanitize(o)
	v := newView(title, o)
	W, H := float64(o.Width), float64(o.Height)
	top := pad
	if v.ShowTitle {
		top += 26
	}
	v.Message = message
	v.MessageX = fmtCoord(W / 2)
	v.MessageY = fmtCoord(top + (H-top-pad)/2 - 4)
	v.Ghost = ghostPath(pad, H-pad-14, W-pad, H*0.38)
	return execute(w, v)
}

// ghostPath draws a soft, decorative stacked-area silhouette for placeholders.
func ghostPath(left, bottom, right, height float64) string {
	var b strings.Builder
	n := 48
	b.WriteString("M" + fmtCoord(left) + "," + fmtCoord(bottom))
	for i := 0; i <= n; i++ {
		f := float64(i) / float64(n)
		x := left + f*(right-left)
		y := bottom - height*(0.15+0.7*f*f*(0.85+0.15*math.Sin(f*9)))
		b.WriteString("L" + fmtCoord(x) + "," + fmtCoord(y))
	}
	b.WriteString("L" + fmtCoord(right) + "," + fmtCoord(bottom) + "Z")
	return b.String()
}

func execute(w io.Writer, v *view) error {
	var buf bytes.Buffer
	if err := svgTemplate.Execute(&buf, v); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// Render writes a complete standalone SVG for the series.
func Render(w io.Writer, title string, points []model.Point, o Options) error {
	o = sanitize(o)
	W, H := float64(o.Width), float64(o.Height)

	s := buildSeries(points, o, max(2, o.Width/2))
	if len(s.Names) == 0 || s.maxValue(o.Mode) <= 0 {
		msg := "No source code found yet"
		if len(points) == 0 {
			msg = "No commits analyzed yet"
		}
		return RenderMessage(w, title, msg, o)
	}

	v := newView(title, o)
	top := pad
	if v.ShowTitle {
		top += 28
	}

	colors := assignColors(s.Names)

	// Legend
	if o.Legend {
		items, height := layoutLegend(s, colors, pad, top, W-2*pad)
		// only keep the legend if there is still room for a meaningful plot
		if H-(top+height)-56 >= 80 {
			v.Legend = items
			top += height + 4
		}
	}

	// Y scale
	maxV := s.maxValue(o.Mode)
	var ticks []float64
	if s.Percent {
		ticks = []float64{0, 25, 50, 75, 100}
	} else {
		ticks = bestTicks(maxV, 3, max(3, min(6, int((H-top-40)/40)))) // counts are integers
	}
	yMax := ticks[len(ticks)-1]
	labels := make([]string, len(ticks))
	labelW := 0.0
	for i, t := range ticks {
		if s.Percent {
			labels[i] = strconv.Itoa(int(t)) + "%"
		} else {
			labels[i] = compact(t)
		}
		labelW = max(labelW, float64(len(labels[i]))*charWidth11)
	}

	plotL := pad + labelW + 8
	plotR := W - pad
	plotT := top + 6
	plotB := H - pad - 26
	plotW := plotR - plotL
	plotH := plotB - plotT

	yOf := func(val float64) float64 { return plotB - val/yMax*plotH }

	for i, t := range ticks {
		y := fmtCoord(yOf(t))
		if i > 0 {
			v.Grid = append(v.Grid, lineItem{X1: fmtCoord(plotL), Y1: y, X2: fmtCoord(plotR), Y2: y})
		}
		v.YLabels = append(v.YLabels, textItem{X: fmtCoord(plotL - 8), Y: fmtCoord(yOf(t) + 4), Text: labels[i]})
	}
	v.Baseline = lineItem{X1: fmtCoord(plotL), Y1: fmtCoord(plotB), X2: fmtCoord(plotR), Y2: fmtCoord(plotB)}

	// X positions of samples
	nS := len(s.Index)
	xs := make([]float64, nS)
	byTime := o.X == XDate && s.LastT > s.FirstT
	for k := range s.Index {
		var f float64
		switch {
		case byTime:
			f = float64(s.Times[k]-s.FirstT) / float64(s.LastT-s.FirstT)
		case s.Total > 1:
			f = float64(s.Index[k]) / float64(s.Total-1)
		}
		xs[k] = plotL + f*plotW
	}
	values := s.Values
	if nS == 1 {
		// single point: flat band across the whole plot
		xs = []float64{plotL, plotR}
		for g := range values {
			values[g] = []float64{values[g][0], values[g][0]}
		}
		nS = 2
	}

	// X labels
	labelY := fmtCoord(plotB + 17)
	if byTime {
		maxTicks := max(2, int(plotW/90))
		for _, tt := range timeTicks(time.Unix(s.FirstT, 0), time.Unix(s.LastT, 0), maxTicks) {
			f := float64(tt.T.Unix()-s.FirstT) / float64(s.LastT-s.FirstT)
			x := plotL + f*plotW
			v.XLabels = append(v.XLabels, textItem{X: fmtCoord(x), Y: labelY, Text: tt.Label, Anchor: "middle"})
			v.Grid = append(v.Grid, lineItem{X1: fmtCoord(x), Y1: fmtCoord(plotB), X2: fmtCoord(x), Y2: fmtCoord(plotB + 4)})
		}
	} else if o.X == XDate {
		// all commits at the same instant
		v.XLabels = append(v.XLabels, textItem{X: fmtCoord(plotL), Y: labelY, Text: time.Unix(s.FirstT, 0).UTC().Format("2 Jan 2006"), Anchor: "start"})
	} else {
		last := max(1, s.Total-1)
		ct := niceTicks(float64(last), max(2, min(8, int(plotW/80))), 1)
		for _, t := range ct {
			if t > float64(last) {
				break
			}
			x := plotL + t/float64(last)*plotW
			v.XLabels = append(v.XLabels, textItem{X: fmtCoord(x), Y: labelY, Text: "#" + strconv.Itoa(int(t)), Anchor: "middle"})
			v.Grid = append(v.Grid, lineItem{X1: fmtCoord(x), Y1: fmtCoord(plotB), X2: fmtCoord(x), Y2: fmtCoord(plotB + 4)})
		}
	}
	// keep edge labels inside the card
	for i := range v.XLabels {
		l := &v.XLabels[i]
		x, _ := strconv.ParseFloat(l.X, 64)
		half := float64(len(l.Text)) * charWidth11 / 2
		if l.Anchor == "middle" && x-half < pad {
			l.Anchor = "start"
			l.X = fmtCoord(max(pad, x-half))
		}
		if l.Anchor == "middle" && x+half > W-pad {
			l.Anchor = "end"
			l.X = fmtCoord(min(W-pad, x+half))
		}
	}

	// Bands / lines
	if o.Mode == ModeLines {
		v.Lines = true
		v.StrokeWidth = "2"
		// draw smallest first so the biggest stays on top
		for g := len(s.Names) - 1; g >= 0; g-- {
			var b strings.Builder
			for k := 0; k < nS; k++ {
				if k == 0 {
					b.WriteByte('M')
				} else {
					b.WriteByte('L')
				}
				b.WriteString(fmtCoord(xs[k]) + "," + fmtCoord(yOf(values[g][k])))
			}
			v.Bands = append(v.Bands, bandItem{Name: s.Names[g], Color: colors[g], Path: b.String()})
		}
	} else {
		lower := make([]float64, nS)
		for g := range s.Names {
			upper := make([]float64, nS)
			nonZero := false
			for k := 0; k < nS; k++ {
				upper[k] = lower[k] + values[g][k]
				if values[g][k] > 0 {
					nonZero = true
				}
			}
			if nonZero {
				var b strings.Builder
				for k := 0; k < nS; k++ {
					if k == 0 {
						b.WriteByte('M')
					} else {
						b.WriteByte('L')
					}
					b.WriteString(fmtCoord(xs[k]) + "," + fmtCoord(yOf(upper[k])))
				}
				for k := nS - 1; k >= 0; k-- {
					b.WriteString("L" + fmtCoord(xs[k]) + "," + fmtCoord(yOf(lower[k])))
				}
				b.WriteByte('Z')
				v.Bands = append(v.Bands, bandItem{Name: s.Names[g], Color: colors[g], Path: b.String()})
			}
			lower = upper
		}
	}

	if o.Interactive {
		l := Layout{
			Width:  o.Width,
			Height: o.Height,
			Plot:   [4]float64{round1(plotL), round1(plotT), round1(plotR), round1(plotB)},
			X:      XCommits,
			Domain: [2]int64{0, int64(max(0, s.Total-1))},
			YMax:   yMax,
			Mode:   o.Mode,
			Metric: o.Metric,
		}
		if byTime {
			l.X = XDate
			l.Domain = [2]int64{s.FirstT, s.LastT}
		}
		for g, name := range s.Names {
			l.Groups = append(l.Groups, LayoutGroup{Name: name, Color: colors[g], Langs: s.Members[g]})
		}
		b, err := json.Marshal(l)
		if err != nil {
			return err
		}
		v.Layout = string(b)
	}

	return execute(w, v)
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

// layoutLegend flows legend items left to right, wrapping into rows.
func layoutLegend(s *series, colors []string, left, top, width float64) ([]legendItem, float64) {
	var items []legendItem
	x, row := left, 0
	for g, name := range s.Names {
		value := compact(s.Last[g])
		w := legendSwatch + 5 + float64(len([]rune(name+" "+value)))*charWidth12 + 14
		if x > left && x+w > left+width {
			x = left
			row++
		}
		y := top + float64(row)*legendRowH
		items = append(items, legendItem{
			X:     fmtCoord(x),
			Y:     fmtCoord(y),
			TX:    fmtCoord(x + legendSwatch + 5),
			TY:    fmtCoord(y + 9.5),
			Color: colors[g],
			Name:  name,
			Value: value,
		})
		x += w
	}
	return items, float64(row+1) * legendRowH
}
