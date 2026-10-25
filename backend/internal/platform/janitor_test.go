package platform

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// node starts another service process against the same database, blob store and clock.
func (h *harness) node(workerID string, mutate ...func(*Config)) *Service {
	h.t.Helper()
	cfg := DefaultConfig(testTenant)
	cfg.WorkerID = workerID
	cfg.PollInterval = 50 * time.Millisecond
	for _, change := range mutate {
		change(&cfg)
	}
	svc, err := New(context.Background(), cfg, Dependencies{
		DB: h.db, Blobs: h.blobs, Sandbox: h.runner, Connections: h.conns, Sessions: h.sessions, Audit: recordingAudit{h},
		SigningKey: []byte(strings.Repeat("k", 32)), Clock: h.clock.Now,
	})
	h.must(err)
	return svc
}

func (h *harness) eventually(what string, within time.Duration, condition func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(within)
	for !condition() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *harness) artifactStatus(id string) string {
	h.t.Helper()
	var stored Artifact
	h.must(h.db.Collection(colArtifacts).FindOne(context.Background(), bson.M{"_id": id}).Decode(&stored))
	return stored.Status
}

func TestClusterLocksAdmitOneHolderAtATime(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	other := h.node("test-worker-2")
	const ttl = 5 * time.Minute

	if held, err := h.svc.acquireLock(ctx, "chore", ttl); err != nil || !held {
		t.Fatalf("the first node should take a free lock: %v, %v", held, err)
	}
	if held, err := other.acquireLock(ctx, "chore", ttl); err != nil || held {
		t.Fatalf("a second node must not take a held lock (and that is not an error): %v, %v", held, err)
	}
	if held, _ := h.svc.acquireLock(ctx, "chore", ttl); held {
		t.Fatal("a lock is not re-entrant: the holder must wait for it to lapse like everyone else")
	}
	if held, err := other.acquireLock(ctx, "another-chore", ttl); err != nil || !held {
		t.Fatalf("locks with different names are independent: %v, %v", held, err)
	}

	// A holder that vanished cannot block the chore forever: the lock lapses.
	h.clock.Advance(ttl - time.Second)
	if held, _ := other.acquireLock(ctx, "chore", ttl); held {
		t.Fatal("taken before it lapsed")
	}
	h.clock.Advance(2 * time.Second)
	if held, err := other.acquireLock(ctx, "chore", ttl); err != nil || !held {
		t.Fatalf("a lapsed lock should be takeable: %v, %v", held, err)
	}
	var lock struct {
		Holder string `bson:"holder"`
	}
	h.must(h.db.Collection(colLocks).FindOne(ctx, bson.M{"_id": "chore"}).Decode(&lock))
	if lock.Holder != "test-worker-2" {
		t.Fatalf("holder = %q", lock.Holder)
	}

	// Many nodes racing for a fresh lock produce exactly one winner.
	var winners atomic.Int32
	var race sync.WaitGroup
	for i := 0; i < 8; i++ {
		race.Add(1)
		go func(svc *Service) {
			defer race.Done()
			if held, err := svc.acquireLock(ctx, "contended", ttl); err == nil && held {
				winners.Add(1)
			}
		}([]*Service{h.svc, other}[i%2])
	}
	race.Wait()
	if winners.Load() != 1 {
		t.Fatalf("%d nodes won the same lock", winners.Load())
	}
}

func TestExpiredFilesAreSweptInBatchesUntilNoneAreDue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	project := h.project(1_000_000)
	var files []ArtifactView
	for _, name := range []string{"a.csv", "b.csv", "c.csv", "d.csv", "e.csv", "f.csv", "g.csv"} {
		files = append(files, h.upload(h.alice, project.ID, name, ordersOct))
	}
	if h.blobs.Len() != len(files) {
		t.Fatalf("stored bodies = %d", h.blobs.Len())
	}

	if removed := h.svc.sweepArtifacts(ctx, 3); removed != 0 {
		t.Fatalf("nothing is due yet, but %d were removed", removed)
	}
	h.clock.Advance(40 * 24 * time.Hour)
	// Seven files, batches of three: the sweep must keep going rather than stop after one batch.
	if removed := h.svc.sweepArtifacts(ctx, 3); removed != len(files) {
		t.Fatalf("removed %d of %d in one sweep", removed, len(files))
	}
	for _, file := range files {
		if status := h.artifactStatus(file.ID); status != ArtifactExpired {
			t.Errorf("%s is %s, want expired (the record stays as a tombstone)", file.Name, status)
		}
	}
	if h.blobs.Len() != 0 {
		t.Fatalf("%d bodies were left in storage", h.blobs.Len())
	}
	if removed := h.svc.sweepArtifacts(ctx, 3); removed != 0 {
		t.Fatalf("a second sweep found %d more", removed)
	}
}

func TestOnlyOneNodeSweepsAtATimeAndAnotherTakesOverWhenItLapses(t *testing.T) {
	cycle := func(c *Config) { c.janitorEvery = 15 * time.Millisecond }
	h := newHarness(t, cycle)
	other := h.node("test-worker-2", cycle)
	project := h.project(1_000_000)
	first := h.upload(h.alice, project.ID, "first.csv", ordersOct)
	h.clock.Advance(40 * 24 * time.Hour)

	ctx, stop := context.WithCancel(context.Background())
	var loops sync.WaitGroup
	for _, svc := range []*Service{h.svc, other} {
		loops.Add(1)
		go func() { defer loops.Done(); svc.janitorLoop(ctx) }()
	}
	defer func() { stop(); loops.Wait() }()

	h.eventually("the expired file to be removed", 10*time.Second, func() bool { return h.artifactStatus(first.ID) == ArtifactExpired })
	holder := func() (string, time.Time) {
		var lock struct {
			Holder string    `bson:"holder"`
			Until  time.Time `bson:"until"`
		}
		h.must(h.db.Collection(colLocks).FindOne(context.Background(), bson.M{"_id": "artifact-expiry"}).Decode(&lock))
		return lock.Holder, lock.Until
	}
	winner, until := holder()
	if winner != "test-worker" && winner != "test-worker-2" {
		t.Fatalf("holder = %q", winner)
	}
	// Dozens of cycles later, with the clock held still, nobody else has taken or extended the lock.
	time.Sleep(300 * time.Millisecond)
	if again, laterUntil := holder(); again != winner || !laterUntil.Equal(until) {
		t.Fatalf("the lock moved while it was still held: %s until %v, then %s until %v", winner, until, again, laterUntil)
	}

	// When the holder's lease lapses a node takes over and keeps the cleanup going.
	second := h.upload(h.alice, project.ID, "second.csv", ordersOct)
	h.clock.Advance(40 * 24 * time.Hour)
	h.eventually("a node to take over and remove the next file", 10*time.Second, func() bool { return h.artifactStatus(second.ID) == ArtifactExpired })
}
