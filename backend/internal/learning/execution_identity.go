package learning

import (
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	"regexp"
)

func (s *Store) executionIdentity(ctx context.Context, claim Job, imageID string) error {
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(imageID) {
		return invalid("执行镜像标识无效。")
	}
	return s.tx(ctx, func(tx context.Context) error {
		j, e := s.lease(tx, claim)
		if e != nil {
			return e
		}
		if e = s.validUser(tx, j); e != nil {
			return e
		}
		if j.Dispatched && j.RuntimeImageID != imageID {
			return conflict()
		}
		_, e = s.db.Collection("learning_jobs").UpdateOne(tx, releaseFilter(s.tenant, j.ID), bson.M{"$set": bson.M{"runtimeImageId": imageID}})
		return e
	})
}
