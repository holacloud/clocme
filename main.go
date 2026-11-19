package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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

	// Before anything else logs: optionally ship logs to instantlogs as well.
	shipper := logship.New(c.Instantlogs)
	if shipper != nil {
		log.SetOutput(io.MultiWriter(os.Stderr, shipper))
		log.Println("shipping logs to instantlogs")
	}

	// Metadata is small and read on every request: disk + memory cache.
	repos, err := store.NewStoreDiskCached[model.Repo](filepath.Join(c.DataDir, "repos"))
	if err != nil {
		log.Fatalf("repos store: %v", err)
	}
	// Series can be big: disk only, read on demand.
	series, err := store.NewStoreDisk[model.Series](filepath.Join(c.DataDir, "series"))
	if err != nil {
		log.Fatalf("series store: %v", err)
	}

	owners, err := store.NewStoreDiskCached[model.Owner](filepath.Join(c.DataDir, "owners"))
	if err != nil {
		log.Fatalf("owners store: %v", err)
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
		log.Println("rsvg-convert not found: PNG social previews (og:image) disabled")
	}

	b, err := api.New(svc, rz, c.BaseURL, config.VERSION)
	if err != nil {
		log.Fatalf("api: %v", err)
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

	log.Printf("clocme %s listening on %s", config.VERSION, c.Addr)
	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Println(err)
	}
	log.Println("bye")
	shipper.Close(5 * time.Second) // deliver the last lines
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		os.Exit(1)
	}
}
