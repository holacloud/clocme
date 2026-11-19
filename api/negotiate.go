package api

import (
	"net/http"
	"strings"
)

const (
	FormatHTML = "html"
	FormatSVG  = "svg"
	FormatJSON = "json"
	FormatPNG  = "png" // only when explicitly asked (extension or ?format=png)
)

// splitFormat removes a known extension from the repo segment:
// "box.svg" -> ("box", "svg"). Repo names may legitimately contain dots
// ("socket.io"), so only exact known extensions are stripped.
func splitFormat(name string) (string, string) {
	for _, f := range []string{FormatSVG, FormatJSON, FormatPNG} {
		if strings.HasSuffix(name, "."+f) && len(name) > len(f)+1 {
			return strings.TrimSuffix(name, "."+f), f
		}
	}
	return name, ""
}

// pageBots are User-Agent fragments (lowercase) of link preview and search
// crawlers. They fetch the page URL to read its og:/twitter: tags, but send
// "Accept: */*" and no Sec-Fetch-Dest, so they would get the SVG otherwise.
// GitHub's image proxy (github-camo) is deliberately not here.
var pageBots = []string{
	"twitterbot", "linkedinbot", "facebookexternalhit", "facebookcatalog",
	"meta-externalagent", "slackbot", "discordbot", "telegrambot", "whatsapp",
	"skypeuripreview", "pinterest", "redditbot", "mastodon", "bluesky",
	"embedly", "iframely", "vkshare", "snapchat", "applebot",
	"googlebot", "bingbot", "duckduckbot", "yandex", "baiduspider",
}

func isPageBot(ua string) bool {
	ua = strings.ToLower(ua)
	if strings.Contains(ua, "googlebot-image") {
		return false // fetches images, not pages
	}
	for _, b := range pageBots {
		if strings.Contains(ua, b) {
			return true
		}
	}
	return false
}

// negotiate decides what to answer for a repo URL. Priority:
//  1. explicit ?format= or extension (escape hatch if our guesses are wrong)
//  2. a browser navigating to the page: Accept has text/html and
//     Sec-Fetch-Dest is "document" (or absent, older clients) -> HTML
//  3. link preview and search crawlers (by User-Agent) -> HTML
//  4. anything else (<img>, GitHub camo proxy, curl...) -> SVG
func negotiate(r *http.Request, ext string) string {
	switch f := strings.ToLower(r.URL.Query().Get("format")); f {
	case FormatHTML, FormatSVG, FormatJSON, FormatPNG:
		return f
	}
	if ext != "" {
		return ext
	}

	accept := r.Header.Get("Accept")
	dest := r.Header.Get("Sec-Fetch-Dest")
	if strings.Contains(accept, "text/html") && (dest == "" || dest == "document") {
		return FormatHTML
	}
	if dest == "" && isPageBot(r.Header.Get("User-Agent")) {
		return FormatHTML
	}
	return FormatSVG
}
