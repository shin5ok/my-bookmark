package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cloud.google.com/go/firestore"
	"golang.org/x/oauth2/google"
	"my-bookmark/internal/app"
	"my-bookmark/internal/content"
	"my-bookmark/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := app.LoadConfig()
	if err != nil {
		return err
	}
	db, err := firestore.NewClient(context.Background(), cfg.Project)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	tokenSource, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return err
	}
	var summarizer app.Summarizer = &content.Gemini{Project: cfg.Project, Location: cfg.GeminiLocation, Model: cfg.GeminiModel, TokenSource: tokenSource}
	database := store.New(db)
	worker := app.NewWorker(database, summarizer, cfg.GeminiModel)
	var handler http.Handler
	if cfg.WorkerOnly {
		handler = app.TaskHandler(database, worker)
	} else {
		application, err := app.New(cfg, database, summarizer)
		if err != nil {
			return err
		}
		handler = application.Handler()
	}
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		if cfg.WorkerOnly || cfg.APIOnly {
			return
		}
		if cfg.TasksQueue != "" {
			publisher := &app.CloudTasks{Queue: cfg.TasksQueue, WorkerURL: cfg.TasksWorkerURL, ServiceAccount: cfg.TasksServiceAccount, TokenSource: tokenSource}
			app.RunDispatcher(ctx, database, publisher)
		} else {
			worker.Run(ctx)
		}
	}()
	writeTimeout := 100 * time.Second
	if cfg.WorkerOnly {
		writeTimeout = 610 * time.Second
	}
	addr := ":" + cfg.Port
	if cfg.Env == "development" {
		addr = "127.0.0.1:" + cfg.Port
	}
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: writeTimeout, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	done := make(chan error, 1)
	go func() { slog.Info("listening", "address", addr); done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		stop()
		select {
		case <-workerDone:
		case <-time.After(4 * time.Second):
		}
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := server.Shutdown(shutdown)
		select {
		case <-workerDone:
		case <-shutdown.Done():
		}
		return err
	}
}
