package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fulldump/goconfig"
	"github.com/holacloud/store"

	"github.com/holacloud/clocme/analyzer"
	"github.com/holacloud/clocme/api"
	"github.com/holacloud/clocme/config"
	"github.com/holacloud/clocme/githost"
	"github.com/holacloud/clocme/logship"
	"github.com/holacloud/clocme/model"
	"github.com/holacloud/clocme/raster"
	"github.com/holacloud/clocme/service"
	"github.com/holacloud/clocme/telegram"
)

func main() {
	c := config.Default()
	goconfig.Read(c)

	// Informational flags first: they must not start the log shipper.
	if c.Version {
		fmt.Println(config.VERSION)
		return
	}
	if c.ShowConfig {
		e := json.NewEncoder(os.Stdout)
		e.SetIndent("", "  ")
		e.Encode(c)
		return
	}

	// Structured logging (log/slog) to stderr and, optionally, instantlogs.
	// slog.SetDefault also routes the classic log package (net/http, box...)
	// through the same handler.
	var out io.Writer = os.Stderr
	shipper := logship.New(c.Instantlogs)
	if shipper != nil {
		out = io.MultiWriter(os.Stderr, shipper)
		// logship's own logs in the same format (its errors go to stderr only)
		shipper.SetHandler(func(w io.Writer) slog.Handler { return newLogHandler(c.LogFormat, w) })
	}
	slog.SetDefault(slog.New(newLogHandler(c.LogFormat, out)))
	if shipper != nil {
		slog.Info("shipping logs to instantlogs")
	}

	// Metadata is small and read on every request: disk + memory cache.
	repos, err := store.NewStoreDiskCached[model.Repo](filepath.Join(c.DataDir, "repos"))
	if err != nil {
		fatal("repos store", err)
	}
	// Series can be big: disk only, read on demand.
	series, err := store.NewStoreDisk[model.Series](filepath.Join(c.DataDir, "series"))
	if err != nil {
		fatal("series store", err)
	}

	owners, err := store.NewStoreDiskCached[model.Owner](filepath.Join(c.DataDir, "owners"))
	if err != nil {
		fatal("owners store", err)
	}

	a := analyzer.New(analyzer.Config{
		ReposDir:     filepath.Join(c.DataDir, "clones"),
		CacheDir:     filepath.Join(c.DataDir, "blobcache"),
		MaxBlobBytes: c.MaxBlobBytes,
	})

	tg := telegram.New(c.Telegram)

	svc := service.New(repos, series, owners, a, githost.NewGithub(c.GithubToken), tg, service.Options{
		Workers:         c.Workers,
		RefreshInterval: time.Duration(c.RefreshMinutes) * time.Minute,
		AnalyzeTimeout:  time.Duration(c.AnalyzeTimeoutMinutes) * time.Minute,
		MaxRepoSizeKB:   c.MaxRepoSizeMB * 1024,
		MaxOwnerRepos:   c.MaxOwnerRepos,
		BaseURL:         c.BaseURL,
	})

	rz := raster.New()
	if !rz.Available() {
		slog.Warn("rsvg-convert not found: PNG social previews (og:image) disabled")
	}

	b, err := api.New(svc, rz, c.BaseURL, config.VERSION)
	if err != nil {
		fatal("api", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go svc.Run(ctx)

	server := &http.Server{
		Addr:              c.Addr,
		Handler:           b,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	slog.Info("listening", "version", config.VERSION, "addr", c.Addr)
	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("http server", "err", err)
	}
	slog.Info("bye")
	shipper.Close(5 * time.Second) // deliver the last lines
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		os.Exit(1)
	}
}

// newLogHandler returns the slog handler for the configured format: JSON
// (default, one object per line) or text (key=value, easier to read locally).
func newLogHandler(format string, w io.Writer) slog.Handler {
	if strings.EqualFold(format, "text") {
		return slog.NewTextHandler(w, nil)
	}
	return slog.NewJSONHandler(w, nil)
}

func fatal(msg string, err error) {
	slog.Error(msg, "err", err)
	os.Exit(1)
}
