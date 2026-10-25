package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/execution"
	"agentlab.local/backend/internal/httpapi"
	"agentlab.local/backend/internal/learning"
	"agentlab.local/backend/internal/platform"
	platformapi "agentlab.local/backend/internal/platform/api"
	"agentlab.local/backend/internal/runtime"
	"agentlab.local/backend/internal/storage"
	"agentlab.local/backend/internal/training"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

// role selects what this process does. Scale by running more processes of the right role:
// "api" replicas serve HTTP only, "worker" processes execute runs only, "all" does both (default,
// for development and small installations).
func role() (api, worker bool, err error) {
	switch value := strings.ToLower(strings.TrimSpace(os.Getenv("AGENTLAB_ROLE"))); value {
	case "", "all":
		return true, true, nil
	case "api":
		return true, false, nil
	case "worker":
		return false, true, nil
	default:
		return false, false, fmt.Errorf("AGENTLAB_ROLE must be all, api or worker")
	}
}

// publicServer serves only the machine API and health checks, for deployment behind a TLS
// terminating proxy. It never accepts workbench cookies and never mounts the local-only API.
func publicServer(address string, svc *platform.Service) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/v1/", platformapi.Mount("/v1", platformapi.Handler(svc, platformapi.Options{AllowSessions: false})))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		status := svc.Readiness(r.Context())
		w.Header().Set("Content-Type", "application/json")
		if !status.Ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(status)
	})
	return &http.Server{
		Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 16 << 10,
	}
}

func run() error {
	serveAPI, runWorker, err := role()
	if err != nil {
		return err
	}
	address := os.Getenv("AGENTLAB_LISTEN_ADDR")
	if address == "" {
		address = "127.0.0.1:8787"
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("development API must bind to a numeric loopback address")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	client, db, err := storage.OpenMongo(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	store, err := catalog.NewStore(ctx, db, catalog.LocalTenant)
	if err != nil {
		return fmt.Errorf("cannot initialize management database")
	}
	service := app.New(runtime.Unconfigured{}, store)
	service.SetExecution(execution.New(store.History(), os.Getenv("AGENTLAB_AGENT_IMAGE")))
	authStore, err := auth.NewStore(ctx, db, catalog.LocalTenant, store)
	if err != nil {
		return fmt.Errorf("cannot initialize account database")
	}
	service.SetAuthentication(authStore)
	learningStore, err := learning.NewStore(ctx, db, catalog.LocalTenant, store)
	if err != nil {
		return fmt.Errorf("cannot initialize learning database: %w", err)
	}
	service.SetLearning(learningStore)
	trainingStore, err := training.NewStore(ctx, db, catalog.LocalTenant, learningStore)
	if err != nil {
		return fmt.Errorf("cannot initialize training database: %w", err)
	}
	service.SetTraining(trainingStore)
	key, err := connection.MasterKey()
	if err != nil {
		return fmt.Errorf("cannot unlock credential store: %w", err)
	}
	connections, err := connection.NewStore(ctx, db, catalog.LocalTenant, key, store.History(), connection.AllowedOrigins())
	if err != nil {
		return fmt.Errorf("cannot initialize credential store")
	}
	if err = connections.ImportEnvironment(ctx); err != nil {
		return fmt.Errorf("cannot import host model connection: %w", err)
	}
	service.SetConnections(connections)
	hosted, err := platform.OpenFromEnvironment(ctx, platform.Host{DB: db, Tenant: catalog.LocalTenant, Connections: connections, Sessions: authStore, History: store.History(), MasterKey: key})
	if err != nil {
		return fmt.Errorf("cannot initialize the hosted platform: %w", err)
	}
	workerDone := make(chan struct{})
	if runWorker {
		go func() { defer close(workerDone); hosted.RunWorker(ctx) }()
	} else {
		close(workerDone)
	}
	if !serveAPI {
		slog.Info("AgentLab worker started", "role", "worker")
		<-workerDone
		return nil
	}
	service.SetPlatform(platformapi.Handler(hosted, platformapi.Options{AllowSessions: true}))
	// The legacy learning worker belongs to the local development API only.
	if os.Getenv("AGENTLAB_LEARNING_EXECUTOR_READY") != "0" && runWorker {
		if exec, err := service.Execution(); err == nil {
			learningStore.SetExecutor(execution.NewLearningExecutor(exec))
		}
	}
	if runWorker {
		go learningStore.RunWorker(ctx, "api-"+fmt.Sprint(os.Getpid()))
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	servers := []*http.Server{{
		Handler: httpapi.New(service), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}}
	serveErr := make(chan error, 2)
	go func() { serveErr <- servers[0].Serve(listener) }()
	if public := strings.TrimSpace(os.Getenv("AGENTLAB_PLATFORM_LISTEN_ADDR")); public != "" {
		server := publicServer(public, hosted)
		servers = append(servers, server)
		go func() { serveErr <- server.ListenAndServe() }()
		slog.Info("AgentLab public platform API started", "address", public)
	}
	slog.Info("AgentLab development API started", "address", address, "runtime", "unconfigured", "workers", runWorker)
	select {
	case err := <-serveErr:
		if err != http.ErrServerClosed {
			return err
		}
	case <-ctx.Done():
		shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		for _, server := range servers {
			if err := server.Shutdown(shutdownCtx); err != nil {
				return err
			}
		}
		<-workerDone
	}
	return nil
}
