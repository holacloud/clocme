package config

import (
	"github.com/holacloud/clocme/logship"
	"github.com/holacloud/clocme/telegram"
)

var VERSION = "dev"

type Config struct {
	Addr    string `json:"addr" usage:"HTTP listen address"`
	BaseURL string `json:"base_url" usage:"Public base URL, used to build embed snippets"`
	DataDir string `json:"data_dir" usage:"Directory for metadata, series, bare clones and caches"`

	GithubToken string `json:"github_token" usage:"Optional GitHub token to raise the API rate limit (60/h unauthenticated)"`

	MaxRepoSizeMB         int64 `json:"max_repo_size_mb" usage:"Repos bigger than this (as reported by the host) are not analyzed automatically"`
	MaxBlobBytes          int64 `json:"max_blob_bytes" usage:"Files bigger than this are not counted"`
	RefreshMinutes        int   `json:"refresh_minutes" usage:"Minimum minutes between two checks of the same repo"`
	Workers               int   `json:"workers" usage:"Number of concurrent analysis workers"`
	MaxOwnerRepos         int   `json:"max_owner_repos" usage:"Max repos listed and enqueued per user/organization page (0 = no limit)"`
	AnalyzeTimeoutMinutes int   `json:"analyze_timeout_minutes" usage:"Max minutes for one sync+analysis"`

	Telegram telegram.Config `json:"telegram"`

	// Logs always go to stderr (docker logs); with a logger_id they are also
	// shipped to instantlogs.io.
	LogFormat   string         `json:"log_format" usage:"Log format: json (structured, one object per line) or text"`
	Instantlogs logship.Config `json:"instantlogs"`

	ShowConfig bool `json:"show_config" usage:"Print config and exit"`
	Version    bool `json:"version" usage:"Print version and exit"`
}

func Default() *Config {
	return &Config{
		Addr:                  ":8080",
		BaseURL:               "https://cloc.hola.cloud",
		DataDir:               "data",
		MaxRepoSizeMB:         500,
		MaxBlobBytes:          1 << 20,
		RefreshMinutes:        60,
		Workers:               1,
		MaxOwnerRepos:         300,
		AnalyzeTimeoutMinutes: 30,
		LogFormat:             "json",
	}
}
