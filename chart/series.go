package chart

import (
	"math"
	"sort"
	"strings"

	"github.com/holacloud/clocme/model"
)

const otherLabel = "Other"

// series is the chart-ready data: groups (languages + optional "Other") and
// sampled values, already in metric units (or percent).
type series struct {
	Names   []string    // stack order: biggest first (bottom), "Other" last
	Members [][]string  // languages summed in each group (one, or many for "Other")
	Values  [][]float64 // [group][sample]
	Index   []int       // original point index of each sample
	Times   []int64     // (monotonic) time of each sample
	Total   int         // number of original points
	Last    []float64   // value of each group at the last original point (metric units, never percent)
	FirstT  int64
	LastT   int64
	Percent bool
}

func metricFunc(metric string) func(model.Stats) float64 {
	switch metric {
	case MetricLines:
		return func(s model.Stats) float64 { return float64(s.Lines()) }
	case MetricComments:
		return func(s model.Stats) float64 { return float64(s.Comments) }
	case MetricFiles:
		return func(s model.Stats) float64 { return float64(s.Files) }
	default:
		return func(s model.Stats) float64 { return float64(s.Code) }
	}
}

func lowerSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, item := range items {
		m[strings.ToLower(item)] = true
	}
	return m
}

func isDataLanguage(lang string) bool {
	if model.DataLanguages[lang] {
		return true
	}
	for k, v := range model.DataLanguages {
		if v && strings.EqualFold(k, lang) {
			return true
		}
	}
	return false
}

// buildSeries selects languages, groups the tail into "Other" and samples
// the points down to at most maxSamples.
func buildSeries(points []model.Point, o Options, maxSamples int) *series {
	s := &series{Total: len(points)}
	if len(points) == 0 {
		return s
	}
	metric := metricFunc(o.Metric)
	include := lowerSet(o.Include)
	exclude := lowerSet(o.Exclude)

	// 1. candidate languages: non-zero somewhere, not excluded
	peak := map[string]float64{}
	for _, p := range points {
		for lang, st := range p.Languages {
			v := metric(st)
			if v <= 0 {
				continue
			}
			if v > peak[lang] {
				peak[lang] = v
			}
		}
	}
	var langs []string
	for lang := range peak {
		l := strings.ToLower(lang)
		if exclude[l] {
			continue
		}
		if isDataLanguage(lang) && !include[l] && !include["all"] {
			continue
		}
		langs = append(langs, lang)
	}
	if len(langs) == 0 {
		return s
	}

	// 2. select: a language matters if it is big now OR was big at some
	// point (so migrations like JS -> TS keep both sides visible)
	last := points[len(points)-1].Languages
	score := func(lang string) float64 { return max(metric(last[lang]), peak[lang]) }
	sort.Slice(langs, func(i, j int) bool {
		a, b := score(langs[i]), score(langs[j])
		if a != b {
			return a > b
		}
		return langs[i] < langs[j]
	})

	top := o.Top
	if top <= 0 || top > maxTop {
		top = maxTop
	}
	var rest []string
	if len(langs) > top {
		rest = langs[top:]
		langs = langs[:top]
	}

	// stack order: biggest at the last point at the bottom, then by peak
	sort.SliceStable(langs, func(i, j int) bool {
		a, b := metric(last[langs[i]]), metric(last[langs[j]])
		if a != b {
			return a > b
		}
		return peak[langs[i]] > peak[langs[j]]
	})

	group := map[string]int{}
	s.Names = langs
	for i, lang := range langs {
		group[lang] = i
	}
	if len(rest) > 0 {
		s.Names = append(append([]string{}, langs...), otherLabel)
		for _, lang := range rest {
			group[lang] = len(langs)
		}
	}
	s.Members = make([][]string, len(s.Names))
	for lang, g := range group {
		s.Members[g] = append(s.Members[g], lang)
	}
	for _, m := range s.Members {
		sort.Strings(m)
	}

	// 3. sampling: bucket by x position, keep the last point of each bucket
	// (plus the very first point)
	times := make([]int64, len(points))
	var tmax int64 = math.MinInt64
	for i, p := range points {
		tmax = max(tmax, p.Time)
		times[i] = tmax
	}
	s.FirstT, s.LastT = times[0], times[len(times)-1]

	xkey := func(i int) float64 {
		if o.X == XCommits || s.LastT == s.FirstT {
			if len(points) == 1 {
				return 0
			}
			return float64(i) / float64(len(points)-1)
		}
		return float64(times[i]-s.FirstT) / float64(s.LastT-s.FirstT)
	}
	if maxSamples < 2 {
		maxSamples = 2
	}
	bucket := func(i int) int { return int(xkey(i) * float64(maxSamples-1)) }
	selected := []int{0} // the first point is always kept
	for i := 1; i < len(points); i++ {
		prev := selected[len(selected)-1]
		if prev != 0 && bucket(prev) == bucket(i) {
			selected[len(selected)-1] = i // latest point of the bucket wins
			continue
		}
		selected = append(selected, i)
	}

	// 4. values
	n := len(s.Names)
	s.Values = make([][]float64, n)
	for g := range s.Values {
		s.Values[g] = make([]float64, len(selected))
	}
	s.Index = selected
	s.Times = make([]int64, len(selected))
	for k, i := range selected {
		s.Times[k] = times[i]
		for lang, st := range points[i].Languages {
			g, ok := group[lang]
			if !ok {
				continue
			}
			s.Values[g][k] += metric(st)
		}
	}
	s.Last = make([]float64, n)
	for lang, st := range last {
		if g, ok := group[lang]; ok {
			s.Last[g] += metric(st)
		}
	}

	if o.Mode == ModePercent {
		s.Percent = true
		for k := range selected {
			total := 0.0
			for g := 0; g < n; g++ {
				total += s.Values[g][k]
			}
			if total <= 0 {
				continue
			}
			for g := 0; g < n; g++ {
				s.Values[g][k] = s.Values[g][k] * 100 / total
			}
		}
	}

	return s
}

func (s *series) maxValue(mode string) float64 {
	m := 0.0
	if len(s.Names) == 0 {
		return 0
	}
	for k := range s.Values[0] {
		if mode == ModeLines {
			for g := range s.Values {
				m = max(m, s.Values[g][k])
			}
			continue
		}
		total := 0.0
		for g := range s.Values {
			total += s.Values[g][k]
		}
		m = max(m, total)
	}
	return m
}
