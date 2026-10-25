package platform

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// stagesOf collects the name of every stage in an explain plan, whatever the execution engine
// nests them under.
func stagesOf(node any, into *[]string) {
	switch value := node.(type) {
	case bson.M:
		if stage, ok := value["stage"].(string); ok {
			*into = append(*into, stage)
		}
		// Index scans are reported as "index:<name>" next to the stage names.
		if name, ok := value["indexName"].(string); ok {
			*into = append(*into, "index:"+name)
		}
		for key, child := range value {
			if key == "slotBasedPlan" || key == "rejectedPlans" {
				continue
			}
			stagesOf(child, into)
		}
	case bson.A:
		for _, child := range value {
			stagesOf(child, into)
		}
	}
}

// explainFind asks the database how it would run a query against the (empty) test collections.
func (h *harness) explainFind(collection string, filter any, sort bson.D, limit int64) []string {
	h.t.Helper()
	find := bson.D{{Key: "find", Value: collection}, {Key: "filter", Value: filter}}
	if len(sort) > 0 {
		find = append(find, bson.E{Key: "sort", Value: sort})
	}
	find = append(find, bson.E{Key: "limit", Value: limit})
	var explained bson.M
	h.must(h.db.RunCommand(context.Background(), bson.D{{Key: "explain", Value: find}, {Key: "verbosity", Value: "queryPlanner"}}).Decode(&explained))
	planner, ok := explained["queryPlanner"].(bson.M)
	if !ok {
		h.t.Fatalf("explain output has no query planner: %+v", explained)
	}
	var stages []string
	stagesOf(planner["winningPlan"], &stages)
	return stages
}

// A queue that is fast with ten runs but scans every waiting run once it holds a million is the
// classic way a platform stops scaling. Each hot query must be answered from an index in index
// order: no collection scan, and no in-memory sort whose cost grows with the data.
func TestHotQueriesAreServedFromIndexesInOrder(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	// Two indexes can serve the same query in the same order; with no data the planner has nothing to
	// choose by, so give the busy collections some other runs' lines to rule the wide index out.
	var usage, ledger []any
	for i := 0; i < 120; i++ {
		common := bson.M{"tenantId": testTenant, "projectId": "prj_x", "runId": fmt.Sprintf("run_other_%d", i%12), "at": now.Add(time.Duration(i) * time.Second)}
		usage = append(usage, bson.M{"_id": fmt.Sprintf("use_%d", i), "eventKey": fmt.Sprintf("u%d", i), "tenantId": common["tenantId"], "projectId": "prj_x", "runId": common["runId"], "at": common["at"]})
		ledger = append(ledger, bson.M{"_id": fmt.Sprintf("led_%d", i), "eventKey": fmt.Sprintf("l%d", i), "tenantId": common["tenantId"], "projectId": "prj_x", "runId": common["runId"], "at": common["at"]})
	}
	_, err := h.db.Collection(colUsage).InsertMany(context.Background(), usage)
	h.must(err)
	_, err = h.db.Collection(colLedger).InsertMany(context.Background(), ledger)
	h.must(err)
	newestFirst := ordered("createdAt", -1, "_id", -1)
	byTime := ordered("at", -1, "_id", -1)
	for _, c := range []struct {
		name       string
		index      string // the index the query must be answered from
		collection string
		filter     bson.M
		sort       bson.D
		limit      int64
	}{
		{"a worker claims the next queued run", "run_queue", colRuns, bson.M{"status": RunQueued, "availableAt": bson.M{"$lte": now}}, claimOrder, 1},
		{"recovery finds runs whose lease ran out", "run_lease", colRuns, bson.M{"status": bson.M{"$in": liveStatuses}, "leaseUntil": bson.M{"$lt": now}}, nil, 50},
		{"retention finds expired files", "artifact_expiry", colArtifacts, bson.M{"status": bson.M{"$in": bson.A{ArtifactReady, ArtifactPending}}, "expiresAt": bson.M{"$lte": now}}, nil, 200},
		{"storage sampling reads the files still held", "artifact_expiry", colArtifacts, bson.M{"status": ArtifactReady, "expiresAt": bson.M{"$gt": now}}, nil, 0},
		{"an API key is found by its prefix", "key_prefix", colKeys, bson.M{"prefix": "al_live_deadbeef"}, nil, 1},
		{"a project's runs, newest first", "run_list", colRuns, bson.M{"tenantId": testTenant, "projectId": "prj_x"}, newestFirst, 101},
		{"a project's runs in one status", "run_status", colRuns, bson.M{"tenantId": testTenant, "projectId": "prj_x", "status": RunSucceeded}, newestFirst, 101},
		{"a person's runs", "run_actor", colRuns, bson.M{"tenantId": testTenant, "actorId": "user_alice"}, ordered("createdAt", -1), 101},
		{"conversation turns", "run_conversation", colRuns, bson.M{"tenantId": testTenant, "projectId": "prj_x", "conversationId": "chat_x"}, ordered("conversationTurn", 1), 100},
		{"conversation history", "conversation_list", colConversations, bson.M{"tenantId": testTenant, "projectId": "prj_x", "createdBy": "user:user_alice"}, newestFirst, 25},
		{"usage lines of a project", "usage_list", colUsage, bson.M{"tenantId": testTenant, "projectId": "prj_x"}, byTime, 101},
		{"usage lines of one run", "usage_run", colUsage, bson.M{"tenantId": testTenant, "projectId": "prj_x", "runId": "run_x"}, byTime, 101},
		{"usage lines of one key", "usage_key", colUsage, bson.M{"tenantId": testTenant, "projectId": "prj_x", "keyId": "key_x", "at": bson.M{"$gte": now}}, nil, 0},
		{"ledger entries of a project", "ledger_list", colLedger, bson.M{"tenantId": testTenant, "projectId": "prj_x"}, byTime, 101},
		{"ledger entries of one run", "ledger_run", colLedger, bson.M{"tenantId": testTenant, "projectId": "prj_x", "runId": "run_x"}, byTime, 101},
		{"events after a cursor", "event_order", colEvents, bson.M{"runId": "run_x", "seq": bson.M{"$gt": 5}}, ordered("seq", 1), 201},
		{"tool calls of a run", "call_run", colToolCalls, bson.M{"tenantId": testTenant, "runId": "run_x"}, ordered("seq", 1), 100},
		{"tool calls of a project", "call_list", colToolCalls, bson.M{"tenantId": testTenant, "projectId": "prj_x"}, newestFirst, 101},
		{"artifacts of a project", "artifact_list", colArtifacts, bson.M{"tenantId": testTenant, "projectId": "prj_x", "kind": "output"}, newestFirst, 101},
		{"artifacts of a run", "artifact_run", colArtifacts, bson.M{"tenantId": testTenant, "runId": "run_x"}, ordered("createdAt", 1), 101},
		{"a person's projects", "member_projects", colMembers, bson.M{"tenantId": testTenant, "userId": "user_alice"}, nil, 101},
		{"keys of a project", "key_list", colKeys, bson.M{"tenantId": testTenant, "projectId": "prj_x"}, newestFirst, 101},
		{"a project's connection grants", "grant_identity", colGrants, bson.M{"tenantId": testTenant, "projectId": "prj_x"}, nil, 100},
		{"projects that use a connection", "grant_connection", colGrants, bson.M{"tenantId": testTenant, "connectionId": "conn_x"}, nil, 100},
		{"a tenant's projects", "project_list", colProjects, bson.M{"tenantId": testTenant}, newestFirst, 101},
	} {
		t.Run(c.name, func(t *testing.T) {
			stages := h.explainFind(c.collection, c.filter, c.sort, c.limit)
			if !slices.Contains(stages, "index:"+c.index) {
				t.Errorf("should be answered from index %s, plan is %v", c.index, stages)
			}
			for _, forbidden := range []string{"COLLSCAN", "SORT", "SORT_KEY_GENERATOR"} {
				if slices.Contains(stages, forbidden) {
					t.Errorf("uses %s, which grows with the data: %v", forbidden, stages)
				}
			}
		})
	}
}
