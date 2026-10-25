// seed-platform publishes missing starting tariffs without replacing administrator prices.
package main

import (
	"context"
	"fmt"
	"os"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/connection"
	"agentlab.local/backend/internal/platform"
	"agentlab.local/backend/internal/storage"
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
	key, err := connection.MasterKey()
	if err != nil {
		return err
	}
	connections, err := connection.NewStore(ctx, db, catalog.LocalTenant, key, cat.History(), connection.AllowedOrigins())
	if err != nil {
		return err
	}
	if err = connections.ImportEnvironment(ctx); err != nil {
		return err
	}
	svc, err := platform.OpenFromEnvironment(ctx, platform.Host{DB: db, Tenant: catalog.LocalTenant, Connections: connections, History: cat.History(), MasterKey: key})
	if err != nil {
		return err
	}
	admin := platform.Actor{Kind: platform.ActorUser, TenantID: catalog.LocalTenant, UserID: "seed-platform", Username: "seed-platform", DisplayName: "seed-platform", Admin: true}
	shared, err := connections.List(access.System(ctx))
	if err != nil {
		return err
	}
	providers := map[string]bool{}
	for _, c := range shared {
		if c.Scope == "tenant" {
			providers[c.Provider] = true
		}
	}
	if len(providers) == 0 {
		providers["openai-compatible"] = true
	}
	created := 0
	for provider := range providers {
		prices, e := svc.PublishPresetPrices(ctx, admin, provider)
		if e != nil {
			return e
		}
		created += len(prices)
	}
	fmt.Printf("已发布 %d 项缺失的模型/托管/沙箱/工具价格。模型参考 OpenRouter 基础价；资源为平台预设，不等于供应商账单。\n", created)
	if len(shared) == 0 {
		fmt.Println("提示：没有平台模型连接。在 .env.dev 中配置 BASE_URL/API_KEY 并用 --env-file 启动一次 API，或在工作台添加连接后再运行本命令。")
	}
	return nil
}
