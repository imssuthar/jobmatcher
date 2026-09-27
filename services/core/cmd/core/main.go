// Command core is the JobMatcher core service (Phase 1: profile and fact bank).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/imssuthar/jobmatcher/services/core/internal/extract"
	"github.com/imssuthar/jobmatcher/services/core/internal/llm"
	"github.com/imssuthar/jobmatcher/services/core/internal/objstore"
	"github.com/imssuthar/jobmatcher/services/core/internal/platform"
	"github.com/imssuthar/jobmatcher/services/core/internal/profile"
	"github.com/imssuthar/jobmatcher/services/core/internal/store"
	"github.com/imssuthar/jobmatcher/services/core/internal/web"
)

func main() {
	log := platform.NewLogger()
	if err := run(); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	log := platform.NewLogger()
	cfg, err := platform.LoadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := platform.SetupTracing(ctx, cfg)
	if err != nil {
		return err
	}
	defer shutdownTracing(context.Background())

	if err := store.WaitForDB(ctx, cfg.DatabaseURL, 60*time.Second); err != nil {
		return err
	}
	if err := store.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	db, err := store.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	objects, err := retry(ctx, 30*time.Second, func() (*objstore.Store, error) {
		return objstore.New(ctx, cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3UseSSL)
	})
	if err != nil {
		return err
	}
	tika := extract.NewTika(cfg.TikaURL)
	gateway := llm.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMTimeout)
	repo := profile.NewPGRepo(db)

	svc := &profile.Service{
		Repo:    repo,
		Objects: objects,
		Text:    tika,
		LLM:     gateway,
		Log:     log,
		Extractor: &profile.Extractor{
			LLM:          gateway,
			ProfileModel: cfg.ModelProfile,
			FactsModel:   cfg.ModelFacts,
			MaxRetries:   2,
		},
		EmbedModel:  cfg.ModelEmbed,
		DocPrefix:   cfg.EmbedDocPrefix,
		QueryPrefix: cfg.EmbedQueryPrefix,
		Workers:     cfg.Workers,
	}
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()
	if err := svc.Start(workerCtx); err != nil {
		return err
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer)
	r.Get("/", web.Index)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/readyz", readiness(map[string]func(context.Context) error{
		"postgres": repo.Ping,
		"s3":       objects.Ping,
		"tika":     tika.Ping,
		"llm":      gateway.Ping,
	}))
	(&profile.Handler{Svc: svc, MaxUploadBytes: cfg.MaxUploadBytes}).Routes(r)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           otelhttp.NewHandler(r, "http"),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.HTTPAddr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	stopWorkers() // in-flight resumes keep their status and resume on next start
	svc.Wait()
	return nil
}

func readiness(checks map[string]func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		result := map[string]string{}
		ok := true
		for name, check := range checks {
			if err := check(ctx); err != nil {
				result[name] = err.Error()
				ok = false
			} else {
				result[name] = "ok"
			}
		}
		status := http.StatusOK
		if !ok {
			status = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(result)
	}
}

func retry[T any](ctx context.Context, timeout time.Duration, fn func() (T, error)) (T, error) {
	deadline := time.Now().Add(timeout)
	for {
		v, err := fn()
		if err == nil || time.Now().After(deadline) || ctx.Err() != nil {
			return v, err
		}
		time.Sleep(time.Second)
	}
}
