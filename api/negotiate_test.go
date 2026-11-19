package api

import (
	"net/http/httptest"
	"testing"
)

func TestNegotiate(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		ext    string
		accept string
		dest   string
		want   string
	}{
		{"browser navigation", "/x", "", "text/html,application/xhtml+xml,*/*;q=0.8", "document", FormatHTML},
		{"old browser without fetch metadata", "/x", "", "text/html,*/*", "", FormatHTML},
		{"img tag", "/x", "", "image/avif,image/webp,*/*", "image", FormatSVG},
		{"img tag with weird accept", "/x", "", "text/html,*/*", "image", FormatSVG},
		{"camo proxy", "/x", "", "image/*", "", FormatSVG},
		{"curl", "/x", "", "*/*", "", FormatSVG},
		{"iframe", "/x", "", "text/html", "iframe", FormatSVG},
		{"forced svg", "/x?format=svg", "", "text/html", "document", FormatSVG},
		{"forced json", "/x?format=json", "", "", "", FormatJSON},
		{"extension", "/x", FormatSVG, "text/html", "document", FormatSVG},
		{"query beats extension", "/x?format=html", FormatSVG, "", "", FormatHTML},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", c.url, nil)
			if c.accept != "" {
				r.Header.Set("Accept", c.accept)
			}
			if c.dest != "" {
				r.Header.Set("Sec-Fetch-Dest", c.dest)
			}
			if got := negotiate(r, c.ext); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestSplitFormat(t *testing.T) {
	cases := map[string][2]string{
		"box":           {"box", ""},
		"box.svg":       {"box", "svg"},
		"box.json":      {"box", "json"},
		"socket.io":     {"socket.io", ""},
		"socket.io.svg": {"socket.io", "svg"},
		".svg":          {".svg", ""},
	}
	for in, want := range cases {
		n, f := splitFormat(in)
		if n != want[0] || f != want[1] {
			t.Errorf("%s: got (%s,%s) want %v", in, n, f, want)
		}
	}
}

func TestParseRepoInput(t *testing.T) {
	cases := map[string]string{
		"fulldump/box":                                 "github.com/fulldump/box",
		"github.com/fulldump/box":                      "github.com/fulldump/box",
		"https://github.com/fulldump/box":              "github.com/fulldump/box",
		"https://github.com/fulldump/box.git":          "github.com/fulldump/box",
		"git@github.com:fulldump/box.git":              "github.com/fulldump/box",
		"https://github.com/fulldump/box/tree/master/": "github.com/fulldump/box",
		"  http://www.github.com/fulldump/box  ":       "github.com/fulldump/box",
		"https://gitlab.com/a/b":                       "",
		"nonsense":                                     "github.com/nonsense",
		"fulldump":                                     "github.com/fulldump",
		"@fulldump":                                    "github.com/fulldump",
		"https://github.com/fulldump/":                 "github.com/fulldump",
		"not a repo":                                   "",
		"https://gitlab.com/someone":                   "",
	}
	for in, want := range cases {
		got, _ := parseRepoInput(in)
		if got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
