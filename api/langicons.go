package api

import (
	"io/fs"
	"strings"
)

// langIconFiles maps scc language names to Devicon icons vendored in
// static/icons/lang (MIT, see LICENSE there).
var langIconFiles = map[string]string{
	"Go": "go", "Go Template": "go",
	"JavaScript": "javascript", "JSX": "javascript",
	"TypeScript": "typescript", "TypeScript Typings": "typescript", "TSX": "typescript",
	"Python": "python", "Java": "java",
	"C": "c", "C Header": "c",
	"C++": "cplusplus", "C++ Header": "cplusplus",
	"C#": "csharp", "Rust": "rust", "Ruby": "ruby", "PHP": "php",
	"HTML": "html5", "CSS": "css3",
	"Shell": "bash", "BASH": "bash", "Zsh": "bash", "Fish": "bash",
	"Kotlin": "kotlin", "Swift": "swift", "Dart": "dart", "Lua": "lua",
	"Perl": "perl", "R": "r", "Scala": "scala", "Elixir": "elixir",
	"Erlang": "erlang", "Haskell": "haskell", "Clojure": "clojure",
	"ClojureScript": "clojure", "Zig": "zig", "Vue": "vuejs",
	"Svelte": "svelte", "Sass": "sass", "SCSS": "sass",
	"Dockerfile": "docker", "Objective C": "objectivec",
	"Objective C++": "objectivec", "Groovy": "groovy", "Julia": "julia",
	"OCaml": "ocaml", "F#": "fsharp", "Elm": "elm", "Crystal": "crystal",
	"Powershell": "powershell", "CoffeeScript": "coffeescript",
	"Vim Script": "vim", "TeX": "latex", "LaTeX": "latex",
	"Solidity": "solidity", "HCL": "terraform", "Terraform": "terraform",
	"FORTRAN Modern": "fortran", "FORTRAN Legacy": "fortran",
	"MATLAB": "matlab", "Gradle": "gradle", "Nix": "nixos",
	"CMake": "cmake", "Markdown": "markdown", "JSON": "json",
	"YAML": "yaml", "Nim": "nim", "Handlebars": "handlebars",
}

var langIconLower map[string]string

func init() {
	langIconLower = make(map[string]string, len(langIconFiles))
	for lang, file := range langIconFiles {
		// only keep icons actually vendored
		if _, err := fs.Stat(staticFS, "static/icons/lang/"+file+".svg"); err == nil {
			langIconLower[strings.ToLower(lang)] = "/static/icons/lang/" + file + ".svg"
		}
	}
}

// langIcon returns the URL of the icon of a language, or "".
func langIcon(lang string) string {
	return langIconLower[strings.ToLower(lang)]
}
