package controlplane

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

// flightRedis serves the admitted complete read path: STRLEN, then the bounded
// EVAL that returns the body. EVAL blocks until the test opens the gate, so the
// test can line up callers behind one read the way a follower's workers line
// up behind a new revision, and it counts how many complete reads reached the
// server, which is the number the coalescing is about.
type flightRedis struct {
	redis.Cmdable
	mu     sync.Mutex
	values map[string]string
	gate   chan struct{}
	// evalErr fails the next EVAL once, as one overrun read timeout would.
	evalErr        error
	evals, strlens atomic.Int32
	evalCancelled  atomic.Int32
}

func newFlightRedis() *flightRedis {
	return &flightRedis{values: map[string]string{}, gate: make(chan struct{})}
}

func (c *flightRedis) set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = value
}

func (c *flightRedis) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.values[key]
	return value, ok
}

func (c *flightRedis) Get(ctx context.Context, key string) *redis.StringCmd {
	value, ok := c.get(key)
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(value, ctx.Err())
}

func (c *flightRedis) StrLen(ctx context.Context, key string) *redis.IntCmd {
	c.strlens.Add(1)
	value, _ := c.get(key)
	return redis.NewIntResult(int64(len(value)), ctx.Err())
}

func (c *flightRedis) HGet(context.Context, string, string) *redis.StringCmd {
	return redis.NewStringResult("", redis.Nil)
}

func (c *flightRedis) Eval(ctx context.Context, _ string, keys []string, _ ...interface{}) *redis.Cmd {
	c.evals.Add(1)
	select {
	case <-c.gate:
	case <-ctx.Done():
		c.evalCancelled.Add(1)
		return redis.NewCmdResult(nil, ctx.Err())
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.evalErr != nil {
		err := c.evalErr
		c.evalErr = nil
		return redis.NewCmdResult(nil, err)
	}
	return redis.NewCmdResult([]interface{}{"ok", c.values[keys[0]], c.values[keys[1]]}, nil)
}

// countingAdmission is the retained-byte account: what is reserved now and the
// most that was ever reserved at once.
type countingAdmission struct {
	mu         sync.Mutex
	used, peak uint64
}

func (account *countingAdmission) reserve(_ context.Context, size uint64) (func(), error) {
	account.mu.Lock()
	defer account.mu.Unlock()
	account.used += size
	if account.used > account.peak {
		account.peak = account.used
	}
	return func() {
		account.mu.Lock()
		defer account.mu.Unlock()
		account.used -= size
	}, nil
}

func (account *countingAdmission) current() (uint64, uint64) {
	account.mu.Lock()
	defer account.mu.Unlock()
	return account.used, account.peak
}

func newFlightRepository(t *testing.T) (*RedisCatalogRepository, *flightRedis, *countingAdmission, string) {
	t.Helper()
	revision, payload := neutralSnapshotPayload(t, "qg-a")
	client := newFlightRedis()
	repo, err := NewRedisCatalogRepository(client, "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	account := &countingAdmission{}
	repo.ConfigureSnapshotMemory(account.reserve, nil)
	client.set(repo.snapshotKey(revision), payload)
	client.set(repo.epochForRevisionKey(revision), "1")
	t.Cleanup(repo.ReleaseSnapshotCache)
	return repo, client, account, payload
}

// waitForMisses blocks until callers have all missed the revision cache, which
// is the last thing a caller does before it reads or joins a read.
func waitForMisses(t *testing.T, repo *RedisCatalogRepository, callers int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for repo.ControlReadCacheStats().Snapshot.Misses < uint64(callers) {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d callers reached the read", repo.ControlReadCacheStats().Snapshot.Misses, callers)
		}
		time.Sleep(time.Millisecond)
	}
	// The miss is counted a few instructions before the caller takes the
	// flight table lock; give the stragglers that gap many times over.
	time.Sleep(100 * time.Millisecond)
}

// A follower replica whose workers all miss the same new revision at once used
// to read the whole body once per worker. Those reads together are what
// saturate the Redis egress, so the number that matters is how many complete
// reads one process issues for one revision while they overlap: one.
func TestConcurrentSlotReadsOfOneRevisionShareOneCompleteRead(t *testing.T) {
	repo, client, account, payload := newFlightRepository(t)
	revision, _ := neutralSnapshotPayload(t, "qg-a")
	const callers = 16
	results := make(chan error, callers)
	var returned sync.WaitGroup
	for caller := 0; caller < callers; caller++ {
		returned.Add(1)
		go func() {
			defer returned.Done()
			ctx, closeScope := WithSnapshotReadScope(context.Background())
			defer closeScope()
			group, err := repo.LoadQueryGroup(ctx, revision, "qg-a")
			if err == nil && group.Identity != "qg-a" {
				err = errors.New("wrong Query Group")
			}
			results <- err
		}()
	}
	waitForMisses(t, repo, callers)
	close(client.gate)
	for caller := 0; caller < callers; caller++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	returned.Wait()
	if evals := client.evals.Load(); evals != 1 {
		t.Fatalf("%d callers issued %d complete reads, want 1", callers, evals)
	}
	if strlens := client.strlens.Load(); strlens != 1 {
		t.Fatalf("%d callers sized the body %d times, want 1", callers, strlens)
	}
	// Waiters must not each reserve a copy of the body, or the memory
	// amplification stays even though the network one is gone. One read
	// reserves the body once and the decode converts it once.
	used, peak := account.current()
	if peak != 2*uint64(len(payload)) {
		t.Fatalf("peak reservation %d bytes, want one body plus one conversion = %d", peak, 2*len(payload))
	}
	if used != uint64(len(payload)) {
		t.Fatalf("retained %d bytes after the callers returned, want the cached body alone = %d", used, len(payload))
	}
	repo.ReleaseSnapshotCache()
	if used, _ := account.current(); used != 0 {
		t.Fatalf("released cache left %d bytes reserved", used)
	}
}

// The shared read coalesces the network round trip, not its outcome. A read
// that fails must fail exactly the callers that were waiting for it and then
// disappear, so the next caller reads afresh rather than inheriting an error
// about a Redis that may have recovered since.
func TestAFailedSharedReadIsNotHandedToLaterCallers(t *testing.T) {
	repo, client, account, _ := newFlightRepository(t)
	revision, _ := neutralSnapshotPayload(t, "qg-a")
	client.evalErr = errors.New("i/o timeout")
	const callers = 8
	results := make(chan error, callers)
	for caller := 0; caller < callers; caller++ {
		go func() {
			_, err := repo.LoadQueryGroup(context.Background(), revision, "qg-a")
			results <- err
		}()
	}
	waitForMisses(t, repo, callers)
	close(client.gate)
	for caller := 0; caller < callers; caller++ {
		var dependency *ActivationDependencyIOError
		if err := <-results; !errors.As(err, &dependency) {
			t.Fatalf("waiter error = %v, want the read's dependency failure", err)
		}
	}
	if evals := client.evals.Load(); evals != 1 {
		t.Fatalf("a failing read was issued %d times by %d callers, want 1", evals, callers)
	}
	if used, _ := account.current(); used != 0 {
		t.Fatalf("failed read left %d bytes reserved", used)
	}
	if repo.snapshotCache.contains(revision) {
		t.Fatal("failed read populated the revision cache")
	}
	if _, err := repo.LoadQueryGroup(context.Background(), revision, "qg-a"); err != nil {
		t.Fatalf("read after the failure: %v", err)
	}
	if evals := client.evals.Load(); evals != 2 {
		t.Fatalf("the caller after the failure issued %d reads in total, want a fresh second read", evals)
	}
}

// Every caller waits under its own context. One leaving early is that
// caller's business alone: the read continues for the others, and a read
// nobody is left waiting for is cancelled instead of finishing for no one.
func TestALeavingCallerNeitherFailsNorKeepsTheSharedRead(t *testing.T) {
	repo, client, _, _ := newFlightRepository(t)
	revision, _ := neutralSnapshotPayload(t, "qg-a")
	const stayers = 4
	results := make(chan error, stayers)
	for caller := 0; caller < stayers; caller++ {
		go func() {
			_, err := repo.LoadQueryGroup(context.Background(), revision, "qg-a")
			results <- err
		}()
	}
	leaving, leave := context.WithCancel(context.Background())
	left := make(chan error, 1)
	go func() {
		_, err := repo.LoadQueryGroup(leaving, revision, "qg-a")
		left <- err
	}()
	waitForMisses(t, repo, stayers+1)
	leave()
	select {
	case err := <-left:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("leaving caller error = %v, want its own cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("leaving caller waited on a read it had abandoned")
	}
	if cancelled := client.evalCancelled.Load(); cancelled != 0 {
		t.Fatalf("one caller leaving cancelled the read %d others were waiting for", stayers)
	}
	close(client.gate)
	for caller := 0; caller < stayers; caller++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if evals := client.evals.Load(); evals != 1 {
		t.Fatalf("%d callers issued %d complete reads, want 1", stayers+1, evals)
	}

	// Now a read whose only caller leaves: nothing should keep it alive.
	other, otherPayload := neutralSnapshotPayload(t, "qg-b")
	client.set(repo.snapshotKey(other), otherPayload)
	client.set(repo.epochForRevisionKey(other), "1")
	client.gate = make(chan struct{})
	alone, abandon := context.WithCancel(context.Background())
	aloneResult := make(chan error, 1)
	go func() {
		_, err := repo.LoadQueryGroup(alone, other, "qg-b")
		aloneResult <- err
	}()
	waitForMisses(t, repo, stayers+2)
	abandon()
	if err := <-aloneResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("abandoning caller error = %v, want its own cancellation", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for client.evalCancelled.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("a read nobody waits for was left running")
		}
		time.Sleep(time.Millisecond)
	}
	// The abandoned read must not be joinable either: the next caller reads
	// for itself instead of inheriting the cancellation.
	close(client.gate)
	if _, err := repo.LoadQueryGroup(context.Background(), other, "qg-b"); err != nil {
		t.Fatalf("read after an abandoned one: %v", err)
	}
	if evals := client.evals.Load(); evals != 3 {
		t.Fatalf("complete reads = %d, want the shared one, the abandoned one and one fresh read", evals)
	}
}
