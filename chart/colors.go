package chart

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
)

const otherColor = "#9ba3ad"

// languageColors maps scc language names to GitHub Linguist colors.
// scc names differ from linguist in a few cases (e.g. "BASH", "C Header",
// "Objective C"); those are mapped to the closest linguist color.
var languageColors = map[string]string{
	"ActionScript":            "#882B0F",
	"Ada":                     "#02f88c",
	"Assembly":                "#6E4C13",
	"Astro":                   "#ff5a03",
	"AWK":                     "#c30e9b",
	"BASH":                    "#89e051",
	"Batch":                   "#C1F12E",
	"C":                       "#555555",
	"C Header":                "#7a7a7a",
	"C Shell":                 "#89e051",
	"C#":                      "#178600",
	"C++":                     "#f34b7d",
	"C++ Header":              "#f77ea0",
	"CMake":                   "#DA3434",
	"COBOL":                   "#4a6b8a",
	"CSS":                     "#663399",
	"Clojure":                 "#db5855",
	"ClojureScript":           "#db5855",
	"CoffeeScript":            "#244776",
	"Crystal":                 "#000100",
	"Cuda":                    "#3A4E3A",
	"D":                       "#ba595e",
	"Dart":                    "#00B4AB",
	"Dockerfile":              "#384d54",
	"Elixir":                  "#6e4a7e",
	"Elm":                     "#60B5CC",
	"Emacs Lisp":              "#c065db",
	"Erlang":                  "#B83998",
	"F#":                      "#b845fc",
	"Fish":                    "#4aae47",
	"FORTRAN Legacy":          "#4d41b1",
	"Fortran Modern":          "#4d41b1",
	"GDScript":                "#355570",
	"GLSL":                    "#5686a5",
	"Gleam":                   "#ffaff3",
	"Go":                      "#00ADD8",
	"Go Template":             "#00ADD8",
	"Gradle":                  "#02303a",
	"GraphQL":                 "#e10098",
	"Groovy":                  "#4298b8",
	"HCL":                     "#844FBA",
	"HTML":                    "#e34c26",
	"Handlebars":              "#f7931e",
	"Haskell":                 "#5e5086",
	"Haxe":                    "#df7900",
	"JSON":                    "#292929",
	"JSX":                     "#f1e05a",
	"Java":                    "#b07219",
	"JavaScript":              "#f1e05a",
	"JavaServer Pages":        "#2A6277",
	"Julia":                   "#a270ba",
	"Jupyter":                 "#DA5B0B",
	"Korn Shell":              "#89e051",
	"Kotlin":                  "#A97BFF",
	"LESS":                    "#1d365d",
	"LaTeX":                   "#3D6117",
	"Lisp":                    "#3fb68b",
	"Lua":                     "#000080",
	"MATLAB":                  "#e16737",
	"Makefile":                "#427819",
	"Markdown":                "#083fa1",
	"Nim":                     "#ffc200",
	"Nix":                     "#7e7eff",
	"OCaml":                   "#ef7a08",
	"Objective C":             "#438eff",
	"Objective C++":           "#6866fb",
	"Odin":                    "#60AFFE",
	"PHP":                     "#4F5D95",
	"Pascal":                  "#E3F171",
	"Perl":                    "#0298c3",
	"Powershell":              "#012456",
	"Prolog":                  "#74283c",
	"Protocol Buffers":        "#4a8bd1",
	"PureScript":              "#1D222D",
	"Python":                  "#3572A5",
	"R":                       "#198CE7",
	"Racket":                  "#3c5caa",
	"Raku":                    "#0000fb",
	"Razor":                   "#512be4",
	"ReasonML":                "#ff5847",
	"ReScript":                "#ed5051",
	"Ruby":                    "#701516",
	"Ruby HTML":               "#701516",
	"Rust":                    "#dea584",
	"SCSS":                    "#c6538c",
	"SQL":                     "#e38c00",
	"Sass":                    "#a53b70",
	"Scala":                   "#c22d40",
	"Scheme":                  "#1e4aec",
	"Shell":                   "#89e051",
	"Solidity":                "#AA6746",
	"Stylus":                  "#ff6347",
	"Svelte":                  "#ff3e00",
	"Swift":                   "#F05138",
	"SystemVerilog":           "#DAE1C2",
	"TCL":                     "#e4cc98",
	"TeX":                     "#3D6117",
	"Templ":                   "#66D0DD",
	"Terraform":               "#7B42BC",
	"TypeScript":              "#3178c6",
	"TypeScript Typings":      "#5a96d8",
	"V":                       "#4f87c4",
	"VHDL":                    "#adb2cb",
	"Vala":                    "#a56de2",
	"Verilog":                 "#b2b7f8",
	"Vim Script":              "#199f4b",
	"Visual Basic":            "#945db7",
	"Vue":                     "#41b883",
	"WebGPU Shading Language": "#1a5e9a",
	"XML":                     "#0060ac",
	"YAML":                    "#cb171e",
	"Zig":                     "#ec915c",
	"Zsh":                     "#89e051",
}

// fallbackPalette is used for languages without a known color. Medium
// saturation and lightness so they work on both light and dark backgrounds.
var fallbackPalette = []string{
	"#4e79a7", "#f28e2b", "#e15759", "#76b7b2", "#59a14f",
	"#edc948", "#b07aa1", "#ff9da7", "#9c755f", "#5fa2ce",
	"#d37295", "#8cd17d", "#b6992d", "#499894", "#e8743b",
}

func colorFor(lang string) string {
	if lang == otherLabel {
		return otherColor
	}
	if c, ok := languageColors[lang]; ok {
		return ensureVisible(c)
	}
	for k, c := range languageColors {
		if strings.EqualFold(k, lang) {
			return ensureVisible(c)
		}
	}
	h := fnv.New32a()
	h.Write([]byte(lang))
	return fallbackPalette[h.Sum32()%uint32(len(fallbackPalette))]
}

// assignColors returns one color per language, avoiding identical colors for
// different languages in the same chart (e.g. Shell and BASH share a color in
// linguist), which would make adjacent bands indistinguishable.
func assignColors(langs []string) []string {
	result := make([]string, len(langs))
	used := map[string]bool{}
	next := 0
	for i, lang := range langs {
		c := strings.ToLower(colorFor(lang))
		if used[c] && lang != otherLabel {
			for j := 0; j < len(fallbackPalette); j++ {
				candidate := fallbackPalette[(next+j)%len(fallbackPalette)]
				if !used[candidate] {
					c = candidate
					next += j + 1
					break
				}
			}
		}
		used[c] = true
		result[i] = c
	}
	return result
}

// ensureVisible lightens very dark colors (e.g. JSON #292929, Lua #000080)
// that would vanish on a dark background.
func ensureVisible(hex string) string {
	if len(hex) != 7 || hex[0] != '#' {
		return hex
	}
	v, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return hex
	}
	r, g, b := float64(v>>16&0xff), float64(v>>8&0xff), float64(v&0xff)
	lum := (0.2126*r + 0.7152*g + 0.0722*b) / 255
	if lum >= 0.22 {
		return hex
	}
	// blend toward white until the luminance reaches ~0.3
	t := (0.3 - lum) / (1 - lum)
	mix := func(c float64) uint64 { return uint64(c + (255-c)*t + 0.5) }
	return fmt.Sprintf("#%02x%02x%02x", mix(r), mix(g), mix(b))
}

// LanguageColor returns the chart color of a language (GitHub Linguist
// colors, deterministic fallback for unknown ones), adjusted to be visible
// on light and dark backgrounds.
func LanguageColor(lang string) string {
	return colorFor(lang)
}
