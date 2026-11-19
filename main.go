package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/holacloud/clocme/model"
	"github.com/holacloud/clocme/service"
	"github.com/holacloud/clocme/telegram"
)

func main() {
	c := config.Default()
	goconfig.Read(c)

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

	b, err := api.New(svc, c.BaseURL, config.VERSION)
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
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
