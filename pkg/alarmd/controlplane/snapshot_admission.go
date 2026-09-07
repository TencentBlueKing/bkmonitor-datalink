package controlplane

import (
	"context"
	"errors"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// SnapshotMemoryAdmission is wired to the existing process retained-byte
// account. The callback must reject immediately rather than wait for capacity.
type SnapshotMemoryAdmission func(context.Context, uint64) (func(), error)

// Called after execution scopes have joined during Bundle shutdown. Any
// outstanding scope still retains its own reference until that scope closes.
func (repository *RedisCatalogRepository) ReleaseSnapshotCache() {
	cache := repository.snapshotCache
	cache.mu.Lock()
	entries := cache.entries
	cache.entries, cache.bytes = nil, 0
	cache.mu.Unlock()
	for index := range entries {
		allocation := entries[index].allocation
		entries[index] = verifiedSnapshotCacheEntry{}
		allocation.release()
	}
}

func (repository *RedisCatalogRepository) loadAdmittedSnapshotPayload(ctx context.Context, revision execution.SnapshotRevision) (string, uint64, *snapshotAllocation, error) {
	if repository.snapshotAdmission == nil {
		payload, epoch, err := repository.loadSnapshotPayload(ctx, revision)
		return payload, epoch, nil, err
	}
	size, err := repository.client.StrLen(ctx, repository.snapshotKey(revision)).Result()
	if err != nil {
		return "", 0, nil, activationDependencyIO(err)
	}
	allocation, err := reserveSnapshotAllocation(ctx, repository.snapshotAdmission, uint64(size))
	if err != nil {
		return "", 0, nil, err
	}
	values, err := repository.readBoundedSnapshot(ctx, revision, size)
	if err != nil {
		allocation.release()
		return "", 0, nil, err
	}
	payload, epoch, err := decodeSnapshotRead(values)
	if err != nil {
		allocation.release()
		return "", 0, nil, err
	}
	return payload, epoch, allocation, nil
}

// One allocation can be retained by the verified cache and a RunOne scope.
// Reference counts here concern immutable memory only, never authorization.
type snapshotAllocation struct {
	mu       sync.Mutex
	refs     int
	releases []func()
	reserve  SnapshotMemoryAdmission
}

func reserveSnapshotAllocation(ctx context.Context, reserve SnapshotMemoryAdmission, size uint64) (*snapshotAllocation, error) {
	if reserve == nil {
		return nil, nil
	}
	release, err := reserve(ctx, size)
	if err != nil {
		return nil, err
	}
	return &snapshotAllocation{refs: 1, releases: []func(){release}, reserve: reserve}, nil
}

func (allocation *snapshotAllocation) retain() {
	if allocation == nil {
		return
	}
	allocation.mu.Lock()
	defer allocation.mu.Unlock()
	allocation.refs++
}

func (allocation *snapshotAllocation) extend(ctx context.Context, size uint64) error {
	if allocation == nil || size == 0 {
		return nil
	}
	allocation.mu.Lock()
	defer allocation.mu.Unlock()
	release, err := allocation.reserve(ctx, size)
	if err != nil {
		return err
	}
	allocation.releases = append(allocation.releases, release)
	return nil
}

func (allocation *snapshotAllocation) release() {
	if allocation == nil {
		return
	}
	allocation.mu.Lock()
	allocation.refs--
	var releases []func()
	if allocation.refs == 0 {
		releases, allocation.releases = allocation.releases, nil
	}
	allocation.mu.Unlock()
	for _, release := range releases {
		release()
	}
}

// The size query is only an estimate for reservation. The subsequent read
// checks that estimate atomically, before Redis returns any large body.
const boundedSnapshotReadScript = `
local size = redis.call('STRLEN', KEYS[1])
if size > tonumber(ARGV[1]) then return {'grew'} end
if redis.call('STRLEN', KEYS[2]) > 20 then return {'epoch'} end
return {'ok', redis.call('GET', KEYS[1]), redis.call('GET', KEYS[2])}
`

func (repository *RedisCatalogRepository) readBoundedSnapshot(ctx context.Context, revision execution.SnapshotRevision, size int64) ([]interface{}, error) {
	values, err := repository.client.Eval(ctx, boundedSnapshotReadScript,
		[]string{repository.snapshotKey(revision), repository.epochForRevisionKey(revision)}, size).Slice()
	if err != nil {
		return nil, activationDependencyIO(err)
	}
	if len(values) == 1 && values[0] == "grew" {
		return nil, activationDependencyIO(errors.New("Snapshot grew after preparation reservation"))
	}
	if len(values) == 1 && values[0] == "epoch" {
		return nil, &PersistedSnapshotCorruptError{Err: errors.New("invalid publication epoch")}
	}
	if len(values) != 3 || values[0] != "ok" {
		return nil, &PersistedSnapshotCorruptError{Err: errors.New("invalid bounded Snapshot response")}
	}
	return values[1:], nil
}
