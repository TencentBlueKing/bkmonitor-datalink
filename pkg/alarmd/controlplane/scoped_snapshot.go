package controlplane

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/go-redis/redis/v8"
)

type snapshotReadScopeKey struct{}

type scopedSnapshotPayload struct {
	repository *RedisCatalogRepository
	revision   execution.SnapshotRevision
	queryGroup execution.QueryGroupIdentity
	payload    string
	epoch      uint64
	allocation *snapshotAllocation
}

type snapshotReadScope struct {
	mu     sync.Mutex
	closed bool
	entry  scopedSnapshotPayload
}

// WithSnapshotReadScope starts a fresh single-call content lifetime, even if ctx
// already carries a scope. It never caches ownership or publication authority.
func WithSnapshotReadScope(ctx context.Context) (context.Context, func()) {
	scope := &snapshotReadScope{}
	return context.WithValue(ctx, snapshotReadScopeKey{}, scope), func() {
		scope.mu.Lock()
		defer scope.mu.Unlock()
		scope.closed = true
		scope.clearLocked()
	}
}

func snapshotScope(ctx context.Context) *snapshotReadScope {
	scope, _ := ctx.Value(snapshotReadScopeKey{}).(*snapshotReadScope)
	return scope
}

func clearSnapshotScope(ctx context.Context) {
	if scope := snapshotScope(ctx); scope != nil {
		scope.mu.Lock()
		scope.clearLocked()
		scope.mu.Unlock()
	}
}

// Called only after payload, QG and applicable publication checks all succeed.
func (repository *RedisCatalogRepository) retainScopedSnapshot(ctx context.Context, revision execution.SnapshotRevision, group execution.QueryGroupIdentity, payload string, epoch uint64, allocation *snapshotAllocation) {
	if scope := snapshotScope(ctx); scope != nil {
		scope.mu.Lock()
		defer scope.mu.Unlock()
		if !scope.closed && ctx.Err() == nil {
			scope.clearLocked()
			allocation.retain()
			scope.entry = scopedSnapshotPayload{repository, revision, group, payload, epoch, allocation}
		}
	}
}

func (repository *RedisCatalogRepository) loadScopedSnapshotPayload(ctx context.Context, revision execution.SnapshotRevision, group execution.QueryGroupIdentity) (string, uint64, *snapshotAllocation, error) {
	if err := ctx.Err(); err != nil {
		clearSnapshotScope(ctx)
		return "", 0, nil, err
	}
	var entry scopedSnapshotPayload
	if scope := snapshotScope(ctx); scope != nil {
		scope.mu.Lock()
		if !scope.closed {
			entry = scope.entry
			entry.allocation.retain()
		}
		scope.mu.Unlock()
	}
	defer entry.allocation.release()
	if entry.repository == repository && entry.revision == revision && entry.queryGroup == group {
		text, err := repository.client.Get(ctx, repository.epochForRevisionKey(revision)).Result()
		if err != nil {
			clearSnapshotScope(ctx)
			if errors.Is(err, redis.Nil) {
				return "", 0, nil, ErrSnapshotUnavailable
			}
			return "", 0, nil, activationDependencyIO(err)
		}
		epoch, err := strconv.ParseUint(text, 10, 64)
		if err != nil || epoch == 0 {
			clearSnapshotScope(ctx)
			return "", 0, nil, &PersistedSnapshotCorruptError{Err: errors.New("invalid publication epoch")}
		}
		if epoch == entry.epoch {
			entry.allocation.retain()
			return entry.payload, epoch, entry.allocation, nil
		}
	}
	clearSnapshotScope(ctx)
	if payload, epoch, allocation, ok := repository.loadRevisionCachedSnapshotPayload(ctx, revision); ok {
		return payload, epoch, allocation, nil
	}
	return repository.loadSharedSnapshotPayload(ctx, revision)
}

// loadRevisionCachedSnapshotPayload reuses a Snapshot body that a complete read
// already verified for this revision, after two small live reads: the
// publication epoch of the revision and the persisted body length. Both must
// equal what the verified read observed. Any other outcome, including a
// missing key or a Redis error, returns false so the complete read path runs
// and keeps every error classification exactly as before.
func (repository *RedisCatalogRepository) loadRevisionCachedSnapshotPayload(ctx context.Context, revision execution.SnapshotRevision) (string, uint64, *snapshotAllocation, bool) {
	counters := &repository.controlReads.snapshot
	entry, ok := repository.snapshotCache.lookupRevision(revision)
	if !ok {
		counters.misses.Add(1)
		return "", 0, nil, false
	}
	text, err := repository.client.Get(ctx, repository.epochForRevisionKey(revision)).Result()
	if err != nil {
		entry.allocation.release()
		counters.refreshes.Add(1)
		return "", 0, nil, false
	}
	epoch, err := strconv.ParseUint(text, 10, 64)
	if err != nil || epoch == 0 || epoch != entry.epoch {
		entry.allocation.release()
		counters.refreshes.Add(1)
		return "", 0, nil, false
	}
	size, err := repository.client.StrLen(ctx, repository.snapshotKey(revision)).Result()
	if err != nil || size != int64(len(entry.payload)) {
		entry.allocation.release()
		counters.refreshes.Add(1)
		return "", 0, nil, false
	}
	counters.hits.Add(1)
	return entry.payload, epoch, entry.allocation, true
}

// ClearSnapshotReadScope discards only call-local content before a compact
// recovery admission wait. Publication and ownership checks remain live.
func ClearSnapshotReadScope(ctx context.Context) { clearSnapshotScope(ctx) }

func (scope *snapshotReadScope) clearLocked() {
	allocation := scope.entry.allocation
	scope.entry = scopedSnapshotPayload{}
	allocation.release()
}
