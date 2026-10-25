//go:build desktop

package main

import (
	"context"
	"embed"
	"io/fs"
	"log"

	"agentlab.local/backend/internal/app"
	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/execution"
	"agentlab.local/backend/internal/learning"
	"agentlab.local/backend/internal/runtime"
	"agentlab.local/backend/internal/storage"
	"agentlab.local/backend/internal/training"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"os"
)

//go:embed all:webdist
var bundled embed.FS

func main() {
	assets, err := fs.Sub(bundled, "webdist")
	if err != nil {
		log.Fatal(err)
	}
	client, db, err := storage.OpenMongo(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	store, err := catalog.NewStore(context.Background(), db, catalog.LocalTenant)
	if err != nil {
		log.Fatal("cannot initialize management database")
	}
	service := app.New(runtime.Unconfigured{}, store)
	authStore, err := auth.NewStore(context.Background(), db, catalog.LocalTenant, store)
	if err != nil {
		log.Fatal("cannot initialize account database")
	}
	service.SetAuthentication(authStore)
	learningStore, err := learning.NewStore(context.Background(), db, catalog.LocalTenant, store)
	if err != nil {
		log.Fatal("cannot initialize learning database")
	}
	service.SetLearning(learningStore)
	trainingStore, err := training.NewStore(context.Background(), db, catalog.LocalTenant, learningStore)
	if err != nil {
		log.Fatal("cannot initialize training database")
	}
	service.SetTraining(trainingStore)
	key, err := connection.MasterKey()
	if err != nil {
		log.Fatal("cannot unlock credential store")
	}
	connections, err := connection.NewStore(context.Background(), db, catalog.LocalTenant, key, store.History(), connection.AllowedOrigins())
	if err != nil {
		log.Fatal("cannot initialize credential store")
	}
	if err = connections.ImportEnvironment(context.Background()); err != nil {
		log.Fatal("cannot import host model connection")
	}
	service.SetConnections(connections)
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	if os.Getenv("AGENTLAB_LEARNING_EXECUTOR_READY") != "0" {
		exec := execution.New(store.History(), os.Getenv("AGENTLAB_AGENT_IMAGE"))
		service.SetExecution(exec)
		learningStore.SetExecutor(execution.NewLearningExecutor(exec))
	}
	go learningStore.RunWorker(workerCtx, "desktop")
	if err := wails.Run(&options.App{
		Title: "AgentLab", Width: 1280, Height: 860, MinWidth: 760, MinHeight: 600,
		AssetServer:      &assetserver.Options{Assets: assets},
		Bind:             []interface{}{app.NewDesktop(service)},
		OnShutdown:       func(context.Context) { cancelWorker() },
		BackgroundColour: &options.RGBA{R: 247, G: 249, B: 245, A: 255},
	}); err != nil {
		log.Fatal(err)
	}
}
