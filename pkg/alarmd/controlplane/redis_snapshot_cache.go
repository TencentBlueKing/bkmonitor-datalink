package controlplane

import (
	"bytes"
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
	payload     []byte
	queryGroups map[execution.QueryGroupIdentity]json.RawMessage
}

// verifiedSnapshotCache only reuses immutable content after the bytes read by
// the current Redis request exactly match a previously verified Snapshot. It
// does not cache Redis availability or publication/activation authority.
type verifiedSnapshotCache struct {
	mu         sync.Mutex
	maxEntries int
	maxBytes   int
	bytes      int
	entries    []verifiedSnapshotCacheEntry
}

func newVerifiedSnapshotCache(maxEntries, maxBytes int) *verifiedSnapshotCache {
	return &verifiedSnapshotCache{maxEntries: maxEntries, maxBytes: maxBytes}
}

func (cache *verifiedSnapshotCache) loadSnapshot(
	ctx context.Context,
	revision execution.SnapshotRevision,
	payload []byte,
) (PublishedSnapshot, error) {
	entry, content, err := cache.load(ctx, revision, payload)
	if err != nil {
		return PublishedSnapshot{}, err
	}
	if content == nil {
		content, err = decodeSnapshotPayload(entry.payload)
		if err != nil {
			return PublishedSnapshot{}, err
		}
	}
	return PublishedSnapshot{SchemaVersion: content.SchemaVersion, QueryGroups: content.QueryGroups}, nil
}

func (cache *verifiedSnapshotCache) loadQueryGroup(
	ctx context.Context,
	revision execution.SnapshotRevision,
	payload []byte,
	identity execution.QueryGroupIdentity,
) (QueryGroup, error) {
	entry, _, err := cache.load(ctx, revision, payload)
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
	payload []byte,
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
		if cache.entries[index].revision != revision || !bytes.Equal(cache.entries[index].payload, payload) {
			continue
		}
		entry := cache.entries[index]
		cache.touch(index)
		return entry, nil, nil
	}
	content, err := decodeAndVerifySnapshotPayload(revision, payload)
	if err != nil {
		return verifiedSnapshotCacheEntry{}, nil, err
	}
	entry := verifiedSnapshotCacheEntry{
		revision:    revision,
		payload:     bytes.Clone(payload),
		queryGroups: make(map[execution.QueryGroupIdentity]json.RawMessage, len(content.QueryGroups)),
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
	cache.insert(entry)
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
	cache.entries = append([]verifiedSnapshotCacheEntry{entry}, cache.entries...)
	cache.bytes += entryBytes
	for len(cache.entries) > cache.maxEntries || cache.bytes > cache.maxBytes {
		last := len(cache.entries) - 1
		cache.bytes -= verifiedSnapshotCacheEntryBytes(cache.entries[last])
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
