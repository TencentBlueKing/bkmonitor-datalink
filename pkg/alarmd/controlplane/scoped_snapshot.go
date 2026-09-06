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
		scope.entry = scopedSnapshotPayload{}
	}
}

func snapshotScope(ctx context.Context) *snapshotReadScope {
	scope, _ := ctx.Value(snapshotReadScopeKey{}).(*snapshotReadScope)
	return scope
}

func clearSnapshotScope(ctx context.Context) {
	if scope := snapshotScope(ctx); scope != nil {
		scope.mu.Lock()
		scope.entry = scopedSnapshotPayload{}
		scope.mu.Unlock()
	}
}

// Called only after payload, QG and applicable publication checks all succeed.
func (repository *RedisCatalogRepository) retainScopedSnapshot(ctx context.Context, revision execution.SnapshotRevision, group execution.QueryGroupIdentity, payload string, epoch uint64) {
	if scope := snapshotScope(ctx); scope != nil {
		scope.mu.Lock()
		defer scope.mu.Unlock()
		if !scope.closed && ctx.Err() == nil {
			scope.entry = scopedSnapshotPayload{repository, revision, group, payload, epoch}
		}
	}
}

func (repository *RedisCatalogRepository) loadScopedSnapshotPayload(ctx context.Context, revision execution.SnapshotRevision, group execution.QueryGroupIdentity) (string, uint64, error) {
	if err := ctx.Err(); err != nil {
		clearSnapshotScope(ctx)
		return "", 0, err
	}
	var entry scopedSnapshotPayload
	if scope := snapshotScope(ctx); scope != nil {
		scope.mu.Lock()
		if !scope.closed {
			entry = scope.entry
		}
		scope.mu.Unlock()
	}
	if entry.repository == repository && entry.revision == revision && entry.queryGroup == group {
		text, err := repository.client.Get(ctx, repository.epochForRevisionKey(revision)).Result()
		if err != nil {
			clearSnapshotScope(ctx)
			if errors.Is(err, redis.Nil) {
				return "", 0, ErrSnapshotUnavailable
			}
			return "", 0, activationDependencyIO(err)
		}
		epoch, err := strconv.ParseUint(text, 10, 64)
		if err != nil || epoch == 0 {
			clearSnapshotScope(ctx)
			return "", 0, &PersistedSnapshotCorruptError{Err: errors.New("invalid publication epoch")}
		}
		if epoch == entry.epoch {
			return entry.payload, epoch, nil
		}
	}
	clearSnapshotScope(ctx)
	return repository.loadSnapshotPayload(ctx, revision)
}
