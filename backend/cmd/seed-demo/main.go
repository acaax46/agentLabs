package main

import (
	"context"
	"fmt"
	"os"

	"agentlab.local/backend/internal/auth"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/learning"
	"agentlab.local/backend/internal/storage"
	"agentlab.local/backend/internal/training"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	ctx := context.Background()
	client, db, err := storage.OpenMongo(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect(ctx) }()
	cat, err := catalog.NewStore(ctx, db, catalog.LocalTenant)
	if err != nil {
		return err
	}
	accounts, err := auth.NewStore(ctx, db, catalog.LocalTenant, cat)
	if err != nil {
		return err
	}
	users, err := accounts.SeedDemo(ctx)
	if err != nil {
		return err
	}
	for _, user := range users {
		fmt.Printf("%s (%s) ready\n", user.Username, user.Role)
	}
	learningStore, err := learning.NewStore(ctx, db, catalog.LocalTenant, cat)
	if err != nil {
		return err
	}
	trainingStore, err := training.NewStore(ctx, db, catalog.LocalTenant, learningStore)
	if err != nil {
		return err
	}
	if err := trainingStore.SeedDemo(ctx); err != nil {
		return err
	}
	fmt.Println("演示账号、训练题库与课程资源已准备；既有账号密码不会重置。")
	return nil
}
