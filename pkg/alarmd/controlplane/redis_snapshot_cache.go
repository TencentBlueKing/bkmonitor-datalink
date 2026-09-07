package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const (
	verifiedSnapshotCacheMaxEntries = 8
	// Match the existing phase-two compiler cache default rather than adding
	// another operator-tuned memory limit.
	verifiedSnapshotCacheMaxBytes = 64 << 20
)

type verifiedSnapshotCacheEntry struct {
	revision    execution.SnapshotRevision
	payload     string
	queryGroups map[execution.QueryGroupIdentity]json.RawMessage
	allocation  *snapshotAllocation
}

// verifiedSnapshotCache only reuses immutable content after the bytes read by
// the current Redis request exactly match a previously verified Snapshot. It
// does not cache Redis availability or publication/activation authority.
type verifiedSnapshotCache struct {
	mu          sync.Mutex
	maxEntries  int
	maxBytes    int
	bytes       int
	entries     []verifiedSnapshotCacheEntry
	admit       SnapshotMemoryAdmission
	objectBytes func(any) uint64
}

func newVerifiedSnapshotCache(maxEntries, maxBytes int) *verifiedSnapshotCache {
	return &verifiedSnapshotCache{maxEntries: maxEntries, maxBytes: maxBytes}
}

func (cache *verifiedSnapshotCache) loadSnapshot(
	ctx context.Context,
	revision execution.SnapshotRevision,
	payload string,
	allocation ...*snapshotAllocation,
) (PublishedSnapshot, error) {
	entry, content, err := cache.load(ctx, revision, payload, allocation...)
	defer entry.allocation.release()
	if err != nil {
		return PublishedSnapshot{}, err
	}
	if content == nil {
		temporary, reserveErr := reserveSnapshotAllocation(ctx, cache.admit, uint64(len(entry.payload)))
		if reserveErr != nil {
			return PublishedSnapshot{}, reserveErr
		}
		defer temporary.release()
		content, err = decodeSnapshotPayload([]byte(entry.payload))
		if err != nil {
			return PublishedSnapshot{}, err
		}
		if cache.objectBytes != nil {
			if err := temporary.extend(ctx, cache.objectBytes(content)); err != nil {
				return PublishedSnapshot{}, err
			}
		}
	}
	return PublishedSnapshot{SchemaVersion: content.SchemaVersion, QueryGroups: content.QueryGroups}, nil
}

func (cache *verifiedSnapshotCache) loadQueryGroup(
	ctx context.Context,
	revision execution.SnapshotRevision,
	payload string,
	identity execution.QueryGroupIdentity,
) (QueryGroup, error) {
	entry, _, err := cache.load(ctx, revision, payload)
	defer entry.allocation.release()
	if err != nil {
		return QueryGroup{}, err
	}
	return decodeCachedQueryGroup(entry, identity)
}

func decodeCachedQueryGroup(
	entry verifiedSnapshotCacheEntry,
	identity execution.QueryGroupIdentity,
) (QueryGroup, error) {
	encoded, ok := entry.queryGroups[identity]
	if !ok {
		return QueryGroup{}, ErrCatalogObjectUnavailable
	}
	var group QueryGroup
	if err := json.Unmarshal(encoded, &group); err != nil {
		return QueryGroup{}, &PersistedSnapshotCorruptError{Err: fmt.Errorf("decode cached Query Group: %w", err)}
	}
	return group, nil
}

// load returns a verified immutable entry. content is non-nil only when this
// call performed the first complete decode and canonical revision validation.
func (cache *verifiedSnapshotCache) load(
	ctx context.Context,
	revision execution.SnapshotRevision,
	payload string,
	allocations ...*snapshotAllocation,
) (verifiedSnapshotCacheEntry, *snapshotPayloadContent, error) {
	if err := ctx.Err(); err != nil {
		return verifiedSnapshotCacheEntry{}, nil, err
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return verifiedSnapshotCacheEntry{}, nil, err
	}
	for index := range cache.entries {
		if cache.entries[index].revision != revision || cache.entries[index].payload != payload {
			continue
		}
		entry := cache.entries[index]
		cache.touch(index)
		entry.allocation.retain()
		return entry, nil, nil
	}
	var allocation *snapshotAllocation
	if len(allocations) != 0 {
		allocation = allocations[0]
	}
	if allocation == nil && cache.admit != nil {
		var err error
		allocation, err = reserveSnapshotAllocation(ctx, cache.admit, uint64(len(payload)))
		if err != nil {
			return verifiedSnapshotCacheEntry{}, nil, err
		}
		defer allocation.release()
	}
	// The byte conversion is live alongside the Redis string during decode.
	temporary, err := reserveSnapshotAllocation(ctx, cache.admit, uint64(len(payload)))
	if err != nil {
		return verifiedSnapshotCacheEntry{}, nil, err
	}
	defer temporary.release()
	content, err := decodeAndVerifySnapshotPayload(revision, []byte(payload))
	if err != nil {
		return verifiedSnapshotCacheEntry{}, nil, err
	}
	entry := verifiedSnapshotCacheEntry{
		revision:    revision,
		payload:     payload,
		queryGroups: make(map[execution.QueryGroupIdentity]json.RawMessage, len(content.QueryGroups)),
		allocation:  allocation,
	}
	if cache.objectBytes != nil {
		if err := temporary.extend(ctx, cache.objectBytes(content)); err != nil {
			return verifiedSnapshotCacheEntry{}, nil, err
		}
	}
	for index := range content.QueryGroups {
		group := content.QueryGroups[index]
		if _, exists := entry.queryGroups[group.Identity]; exists {
			continue
		}
		encoded, err := json.Marshal(group)
		if err != nil {
			return verifiedSnapshotCacheEntry{}, nil, &PersistedSnapshotCorruptError{Err: fmt.Errorf("encode cached Query Group: %w", err)}
		}
		entry.queryGroups[group.Identity] = encoded
	}
	if cache.objectBytes != nil {
		if err := allocation.extend(ctx, cache.objectBytes(entry.queryGroups)); err != nil {
			return verifiedSnapshotCacheEntry{}, nil, err
		}
	}
	cache.insert(entry)
	entry.allocation.retain()
	return entry, content, nil
}

func (cache *verifiedSnapshotCache) touch(index int) {
	if index == 0 {
		return
	}
	entry := cache.entries[index]
	copy(cache.entries[1:index+1], cache.entries[:index])
	cache.entries[0] = entry
}

func (cache *verifiedSnapshotCache) insert(entry verifiedSnapshotCacheEntry) {
	entryBytes := verifiedSnapshotCacheEntryBytes(entry)
	if cache.maxEntries <= 0 || cache.maxBytes <= 0 || entryBytes > cache.maxBytes {
		return
	}
	entry.allocation.retain()
	cache.entries = append([]verifiedSnapshotCacheEntry{entry}, cache.entries...)
	cache.bytes += entryBytes
	for len(cache.entries) > cache.maxEntries || cache.bytes > cache.maxBytes {
		last := len(cache.entries) - 1
		cache.bytes -= verifiedSnapshotCacheEntryBytes(cache.entries[last])
		allocation := cache.entries[last].allocation
		cache.entries[last] = verifiedSnapshotCacheEntry{}
		allocation.release()
		cache.entries = cache.entries[:last]
	}
}

func verifiedSnapshotCacheEntryBytes(entry verifiedSnapshotCacheEntry) int {
	result := len(entry.payload)
	for _, group := range entry.queryGroups {
		result += len(group)
	}
	return result
}

func (cache *verifiedSnapshotCache) contains(revision execution.SnapshotRevision) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for index := range cache.entries {
		if cache.entries[index].revision == revision {
			return true
		}
	}
	return false
}

type snapshotPayloadContent struct {
	SchemaVersion    string                     `json:"schema_version"`
	SnapshotRevision execution.SnapshotRevision `json:"snapshot_revision"`
	QueryGroups      []QueryGroup               `json:"query_groups"`
}

func decodeSnapshotPayload(payload []byte) (*snapshotPayloadContent, error) {
	var content snapshotPayloadContent
	if err := json.Unmarshal(payload, &content); err != nil {
		return nil, &PersistedSnapshotCorruptError{Err: fmt.Errorf("decode: %w", err)}
	}
	return &content, nil
}

func decodeAndVerifySnapshotPayload(
	revision execution.SnapshotRevision,
	payload []byte,
) (*snapshotPayloadContent, error) {
	content, err := decodeSnapshotPayload(payload)
	if err != nil {
		return nil, err
	}
	if content.SchemaVersion != snapshotSchemaVersion || content.SnapshotRevision != revision || content.QueryGroups == nil {
		return nil, &PersistedSnapshotCorruptError{Err: errors.New("invalid schema or identity")}
	}
	derivedRevision, err := deriveSnapshotRevision(content.QueryGroups)
	if err != nil || derivedRevision != revision {
		return nil, &PersistedSnapshotCorruptError{Err: errors.New("content does not match revision")}
	}
	return content, nil
}
