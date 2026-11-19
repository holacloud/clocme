package chart

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"math"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/holacloud/clocme/model"
)

// syntheticPoints simulates a project migrating from JavaScript to TypeScript
// over ~3 years, with a Go backend, some Shell, CSS, HTML and data files.
func syntheticPoints(n int) []model.Point {
	r := rand.New(rand.NewSource(42))
	start := time.Date(2022, 3, 14, 10, 0, 0, 0, time.UTC).Unix()
	span := int64(3 * 365 * 24 * 3600)
	points := make([]model.Point, n)
	for i := range points {
		f := float64(i) / float64(max(1, n-1))
		jitter := func(v float64) int64 { return int64(math.Max(0, v*(1+0.02*r.NormFloat64()))) }
		js := 18000 * math.Min(1, f*2.5) * (1 - smooth((f-0.45)/0.4))
		ts := 26000 * smooth((f-0.4)/0.5)
		goCode := 4000 + 16000*f*f
		sh := 300 + 900*f
		css := 2500 + 1500*f
		html := 1200 + 400*math.Sin(f*6)
		md := 800 + 2000*f
		json := 30000 * f // would dwarf everything if not excluded
		t := start + int64(f*float64(span)) + int64(r.Intn(3600))
		points[i] = model.Point{
			Commit: "c" + time.Unix(t, 0).Format("20060102150405"),
			Time:   t,
			Languages: map[string]model.Stats{
				"JavaScript": {Code: jitter(js), Comments: jitter(js / 8), Blanks: jitter(js / 10), Files: jitter(js / 200)},
				"TypeScript": {Code: jitter(ts), Comments: jitter(ts / 9), Blanks: jitter(ts / 10), Files: jitter(ts / 180)},
				"Go":         {Code: jitter(goCode), Comments: jitter(goCode / 6), Blanks: jitter(goCode / 9), Files: jitter(goCode / 150)},
				"Shell":      {Code: jitter(sh), Comments: jitter(sh / 4), Blanks: jitter(sh / 8), Files: 4},
				"CSS":        {Code: jitter(css), Blanks: jitter(css / 10), Files: jitter(css / 300)},
				"HTML":       {Code: jitter(html), Files: 6},
				"Markdown":   {Code: jitter(md), Blanks: jitter(md / 5), Files: 8},
				"JSON":       {Code: jitter(json), Files: 20},
			},
		}
	}
	return points
}

func smooth(x float64) float64 {
	x = math.Max(0, math.Min(1, x))
	return x * x * (3 - 2*x)
}

func render(t *testing.T, points []model.Point, o Options) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, "fulldump/box · lines of code", points, o); err != nil {
		t.Fatalf("render: %v", err)
	}
	assertXML(t, buf.Bytes())
	return buf.String()
}

func assertXML(t *testing.T, b []byte) {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(b))
	for {
		_, err := d.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("invalid XML: %v\n%s", err, b)
		}
	}
}

func TestParseOptionsDefaults(t *testing.T) {
	o := ParseOptions(url.Values{})
	if !reflect.DeepEqual(o, DefaultOptions()) {
		t.Fatalf("got %+v", o)
	}
	if len(o.Query()) != 0 {
		t.Fatalf("defaults should produce empty query, got %v", o.Query())
	}
}

func TestParseOptions(t *testing.T) {
	q, _ := url.ParseQuery("top=5&x=commits&mode=percent&exclude=html,css&exclude=Go&include=json&theme=dark&w=600&h=300&title=0&legend=no&metric=lines")
	o := ParseOptions(q)
	want := Options{Top: 5, X: XCommits, Mode: ModePercent, Metric: MetricLines,
		Include: []string{"json"}, Exclude: []string{"html", "css", "Go"},
		Theme: ThemeDark, Width: 600, Height: 300, Title: false, Legend: false}
	if !reflect.DeepEqual(o, want) {
		t.Fatalf("got  %+v\nwant %+v", o, want)
	}
	// roundtrip
	if back := ParseOptions(o.Query()); !reflect.DeepEqual(back, o) {
		t.Fatalf("roundtrip: %+v vs %+v", back, o)
	}
}

func TestParseOptionsInvalid(t *testing.T) {
	q, _ := url.ParseQuery("top=-3&x=foo&mode=bar&theme=pink&w=abc&h=99999&metric=?")
	o := ParseOptions(q)
	d := DefaultOptions()
	d.Height = 1200
	if !reflect.DeepEqual(o, d) {
		t.Fatalf("got %+v", o)
	}
	q, _ = url.ParseQuery("top=100&w=10")
	o = ParseOptions(q)
	if o.Top != 20 || o.Width != 300 {
		t.Fatalf("clamp failed: %+v", o)
	}
}

func TestCompact(t *testing.T) {
	cases := map[float64]string{
		0: "0", 950: "950", 999: "999", 1000: "1k", 1234: "1.2k", 12345: "12.3k",
		34000: "34k", 99960: "100k", 150000: "150k", 999999: "1M", 1500000: "1.5M", 2.5e9: "2.5G",
	}
	for in, want := range cases {
		if got := compact(in); got != want {
			t.Errorf("compact(%v) = %s, want %s", in, got, want)
		}
	}
	if FormatCompact(1234) != "1.2k" {
		t.Error("FormatCompact")
	}
}

func TestNiceTicks(t *testing.T) {
	cases := []struct {
		max   float64
		count int
		want  []float64
	}{
		{100, 5, []float64{0, 20, 40, 60, 80, 100}},
		{87, 4, []float64{0, 25, 50, 75, 100}},
		{12345, 5, []float64{0, 2500, 5000, 7500, 10000, 12500}},
		{3, 5, []float64{0, 1, 2, 3}},
		{0, 4, []float64{0, 1, 2, 3, 4}},
	}
	for _, c := range cases {
		got := niceTicks(c.max, c.count, 1)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("niceTicks(%v,%d) = %v, want %v", c.max, c.count, got, c.want)
		}
	}
}

func TestTimeTicks(t *testing.T) {
	from := time.Date(2019, 5, 3, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	ticks := timeTicks(from, to, 8)
	if len(ticks) == 0 || len(ticks) > 8 || ticks[0].Label != "2020" {
		t.Fatalf("years: %+v", ticks)
	}
	ticks = timeTicks(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC), time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC), 6)
	if len(ticks) == 0 || len(ticks) > 6 || !strings.Contains(ticks[0].Label, "2024") {
		t.Fatalf("months: %+v", ticks)
	}
	ticks = timeTicks(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC), time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC), 8)
	if len(ticks) == 0 || ticks[0].Label != "10 Jan" {
		t.Fatalf("days: %+v", ticks)
	}
}

func TestEnsureVisible(t *testing.T) {
	if ensureVisible("#3178c6") != "#3178c6" {
		t.Error("bright colors must not change")
	}
	if c := ensureVisible("#292929"); c == "#292929" {
		t.Error("dark colors must be lightened")
	}
}

func TestDownsample(t *testing.T) {
	points := syntheticPoints(5000)
	for _, x := range []string{XDate, XCommits} {
		o := DefaultOptions()
		o.X = x
		s := buildSeries(points, o, 400)
		if len(s.Index) > 401 || len(s.Index) < 300 {
			t.Fatalf("%s: samples = %d", x, len(s.Index))
		}
		if s.Index[0] != 0 || s.Index[len(s.Index)-1] != len(points)-1 {
			t.Fatalf("%s: first/last not kept", x)
		}
		for i := 1; i < len(s.Index); i++ {
			if s.Index[i] <= s.Index[i-1] {
				t.Fatalf("%s: not increasing", x)
			}
		}
	}
}

func TestSeriesSelection(t *testing.T) {
	points := syntheticPoints(100)
	o := DefaultOptions()
	s := buildSeries(points, o, 400)
	for _, n := range s.Names {
		if n == "JSON" || n == "Markdown" {
			t.Fatalf("data language %s should be hidden by default", n)
		}
	}
	if s.Names[0] != "TypeScript" {
		t.Fatalf("expected TypeScript at the bottom, got %v", s.Names)
	}

	o.Include = []string{"json"}
	o.Exclude = []string{"go"}
	o.Top = 2
	s = buildSeries(points, o, 400)
	if !reflect.DeepEqual(s.Names, []string{"JSON", "TypeScript", "Other"}) && !reflect.DeepEqual(s.Names, []string{"TypeScript", "JSON", "Other"}) {
		t.Fatalf("got %v", s.Names)
	}
}

func TestSeriesKeepsPastBigLanguages(t *testing.T) {
	o := DefaultOptions()
	o.Top = 3
	s := buildSeries(syntheticPoints(500), o, 400)
	if !reflect.DeepEqual(s.Names, []string{"TypeScript", "Go", "JavaScript", "Other"}) {
		t.Fatalf("JavaScript (big in the past) should be selected: %v", s.Names)
	}
}

func TestRenderVariants(t *testing.T) {
	points := syntheticPoints(1500)
	svg := render(t, points, DefaultOptions())
	for _, lang := range []string{"TypeScript", "Go", "CSS"} {
		if !strings.Contains(svg, ">"+lang+"<") {
			t.Errorf("legend should contain %s", lang)
		}
	}
	if strings.Contains(svg, ">JSON<") {
		t.Error("JSON should be hidden")
	}

	for _, q := range []string{"mode=percent", "mode=lines", "x=commits", "theme=dark", "top=0&include=all", "title=0&legend=0&w=300&h=200", "metric=files"} {
		v, _ := url.ParseQuery(q)
		render(t, points, ParseOptions(v))
	}
}

func TestRenderEdgeCases(t *testing.T) {
	// empty
	svg := render(t, nil, DefaultOptions())
	if !strings.Contains(svg, "No commits analyzed yet") {
		t.Error("empty message")
	}
	// single point
	render(t, syntheticPoints(1), DefaultOptions())
	render(t, syntheticPoints(1), Options{Mode: ModeLines})
	// all zero
	zero := []model.Point{{Time: 1, Languages: map[string]model.Stats{"Go": {}}}, {Time: 2, Languages: map[string]model.Stats{"Go": {}}}}
	if svg := render(t, zero, DefaultOptions()); !strings.Contains(svg, "No source code found") {
		t.Error("zero message")
	}
	// same timestamp for every commit
	same := syntheticPoints(10)
	for i := range same {
		same[i].Time = 1700000000
	}
	render(t, same, DefaultOptions())
	// escaping
	var buf bytes.Buffer
	pts := []model.Point{{Time: 1, Languages: map[string]model.Stats{"A<&>\"B": {Code: 10}}}}
	if err := Render(&buf, "<script>&", pts, DefaultOptions()); err != nil {
		t.Fatal(err)
	}
	assertXML(t, buf.Bytes())
	buf.Reset()
	if err := RenderMessage(&buf, "a & b", "Generating chart…", DefaultOptions()); err != nil {
		t.Fatal(err)
	}
	assertXML(t, buf.Bytes())
}

func TestRenderHuge(t *testing.T) {
	svg := render(t, syntheticPoints(50000), DefaultOptions())
	if len(svg) > 200*1024 {
		t.Fatalf("svg too big: %d bytes", len(svg))
	}
	t.Logf("50k points -> %d bytes", len(svg))
}

// TestWriteSamples writes sample SVGs when CHART_SAMPLES=<dir> is set.
func TestWriteSamples(t *testing.T) {
	dir := os.Getenv("CHART_SAMPLES")
	if dir == "" {
		t.Skip("set CHART_SAMPLES=<dir> to write samples")
	}
	os.MkdirAll(dir, 0o755)
	points := syntheticPoints(1500)
	samples := map[string]string{
		"default":      "",
		"dark":         "theme=dark",
		"percent":      "mode=percent",
		"lines":        "mode=lines&theme=light",
		"commits":      "x=commits&top=3",
		"small":        "w=400&h=220",
		"all-included": "top=0&include=all",
	}
	for name, q := range samples {
		v, _ := url.ParseQuery(q)
		var buf bytes.Buffer
		if err := Render(&buf, "acme/webapp · lines of code", points, ParseOptions(v)); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, name+".svg"), buf.Bytes(), 0o644)
	}
	var buf bytes.Buffer
	RenderMessage(&buf, "torvalds/linux · lines of code", "Generating chart… come back in a minute", DefaultOptions())
	os.WriteFile(filepath.Join(dir, "message.svg"), buf.Bytes(), 0o644)
}

func TestRenderInteractive(t *testing.T) {
	points := syntheticPoints(300)

	plain := render(t, points, DefaultOptions())
	if strings.Contains(plain, "data-layout") || strings.Contains(plain, InteractiveID) {
		t.Fatal("non interactive chart must not embed the layout")
	}

	o := DefaultOptions()
	o.Interactive = true
	o.Top = 2
	o.X = XDate
	out := render(t, points, o)

	var root struct {
		ID     string `xml:"id,attr"`
		Layout string `xml:"data-layout,attr"`
		Style  string `xml:"style"`
	}
	if err := xml.Unmarshal([]byte(out), &root); err != nil {
		t.Fatal(err)
	}
	if root.ID != InteractiveID {
		t.Errorf("id %q", root.ID)
	}
	if !strings.HasPrefix(root.Style, "#"+InteractiveID+" text{") || strings.Contains(root.Style, "}.t{") {
		t.Errorf("style not scoped: %s", root.Style)
	}

	var l Layout
	if err := json.Unmarshal([]byte(root.Layout), &l); err != nil {
		t.Fatalf("layout json: %v (%s)", err, root.Layout)
	}
	if l.X != XDate || l.Domain[0] != points[0].Time || l.Domain[1] != points[len(points)-1].Time {
		t.Errorf("domain %+v", l)
	}
	if !(l.Plot[0] < l.Plot[2] && l.Plot[1] < l.Plot[3]) || l.YMax <= 0 {
		t.Errorf("geometry %+v", l)
	}
	if len(l.Groups) != 3 || l.Groups[2].Name != otherLabel || len(l.Groups[2].Langs) < 2 {
		t.Fatalf("groups %+v", l.Groups)
	}
	for _, g := range l.Groups[:2] {
		if len(g.Langs) != 1 || g.Langs[0] != g.Name || g.Color == "" {
			t.Errorf("group %+v", g)
		}
	}

	o.X = XCommits
	out = render(t, points, o)
	xml.Unmarshal([]byte(out), &root)
	json.Unmarshal([]byte(root.Layout), &l)
	if l.X != XCommits || l.Domain != [2]int64{0, 299} {
		t.Errorf("commits domain %+v", l)
	}
}
