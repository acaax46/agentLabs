package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"agentlab.local/backend/internal/platform/ids"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const (
	storageMeter = "storage_mib_days"
	storageUnit  = "mib_day"
	storageMiB   = 1 << 20
)

// usageGroupNote says, in words a person can act on, why a group of usage carries no charge. Groups
// that are charged need no note: their amount speaks for itself.
func usageGroupNote(category, status string) string {
	if status != BillingNotApplicable {
		return ""
	}
	switch category {
	case CategoryModel:
		return "模型费用由客户在自己的供应商账户承担，平台不代收。"
	case CategoryStorage:
		return "存储用量按日采样并披露；MVP 含在项目基础额度内，不单独计费，超额规则尚未制定。"
	}
	return ""
}

// sampleStorage records how much each project keeps stored: one usage line per project and UTC day,
// taken the first time a node looks after the project has files. The unit is MiB-days, rounded up
// per sample, so a day with any stored file is never reported as zero.
//
// This is disclosure, not billing. A line is "not applicable" with no amount: storage is included in
// the project's base allowance for now and there is no storage price. Sampling once a day is too coarse
// to bill from; charging for storage needs byte-second metering, which belongs with a storage price.
//
// Any number of nodes may run it. The line key makes a repeat harmless, and nothing is rewritten.
func (s *Service) sampleStorage(ctx context.Context) (int, error) {
	now := s.now().UTC()
	day := now.Format("2006-01-02")
	found, err := s.col(colArtifacts).Aggregate(ctx, bson.A{
		bson.M{"$match": bson.M{"status": ArtifactReady, "expiresAt": bson.M{"$gt": now}}},
		bson.M{"$group": bson.M{
			"_id":   bson.M{"tenantId": "$tenantId", "projectId": "$projectId"},
			"bytes": bson.M{"$sum": "$size"},
			"files": bson.M{"$sum": 1},
		}},
	})
	if err != nil {
		return 0, err
	}
	var rows []struct {
		ID struct {
			TenantID  string `bson:"tenantId"`
			ProjectID string `bson:"projectId"`
		} `bson:"_id"`
		Bytes int64 `bson:"bytes"`
		Files int   `bson:"files"`
	}
	if err = found.All(ctx, &rows); err != nil {
		return 0, err
	}
	recorded := 0
	for _, row := range rows {
		if row.Bytes <= 0 {
			continue
		}
		var project Project
		err := s.col(colProjects).FindOne(ctx, bson.M{"_id": row.ID.ProjectID, "tenantId": row.ID.TenantID}).Decode(&project)
		if errors.Is(err, mongo.ErrNoDocuments) {
			continue
		}
		if err != nil {
			return recorded, err
		}
		line := UsageLine{
			ID: ids.New(ids.Usage), TenantID: project.TenantID, ProjectID: project.ID,
			Category: CategoryStorage, Meter: storageMeter, Source: "平台存储 · 每日采样",
			Quantity: (row.Bytes + storageMiB - 1) / storageMiB, Unit: storageUnit,
			Currency: project.Currency, Status: BillingNotApplicable, Basis: "sampled",
			Note: fmt.Sprintf("按 UTC 日 %s 首次采样的已保留文件计量：%d 个文件，共 %d 字节，不足 1 MiB 按 1 MiB。MVP 含在项目基础额度内，不单独计费。", day, row.Files, row.Bytes),
			At:   now, EventKey: "storage:" + project.ID + ":" + day,
		}
		if _, err = s.col(colUsage).InsertOne(ctx, line); isDuplicate(err) {
			continue
		} else if err != nil {
			return recorded, err
		}
		recorded++
	}
	return recorded, nil
}

// storageLoop samples on a schedule. Several nodes may run it. The cluster lock is held for most of an
// interval, so adding nodes does not multiply the work (a node always sleeps at least one interval, so
// the lock has lapsed by the time the node that took it looks again); the one-line-per-project-day key
// makes any overlap harmless.
func (s *Service) storageLoop(ctx context.Context) {
	ttl := s.cfg.storageInterval() * 3 / 4
	for ctx.Err() == nil {
		if held, err := s.acquireLock(ctx, "storage-sample", ttl); err == nil && held {
			if recorded, err := s.sampleStorage(ctx); err != nil {
				if ctx.Err() == nil {
					slog.Error("platform storage sampling failed", "error", err)
				}
			} else if recorded > 0 {
				slog.Info("platform sampled storage", "projects", recorded)
			}
		}
		sleepContext(ctx, jitter(s.cfg.storageInterval()))
	}
}
