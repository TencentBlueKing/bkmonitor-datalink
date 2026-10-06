package legacyoutput

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-redis/redis/v8"
)

const maxPodCacheBytes = 64 * 1024

type PodCacheGetter interface {
	Get(context.Context, string) *redis.StringCmd
}
type PodCacheConfig struct {
	KeyPrefix string
	Version   int
	// Observe reports hit/miss/error for counters without including cache keys.
	Observe func(string)
	// OnFallback receives a bounded reason only, at most once per minute.
	OnFallback func(string)
}
type DjangoPodResolver struct {
	client      PodCacheGetter
	config      PodCacheConfig
	mu          sync.Mutex
	lastWarning time.Time
}

type batchPodKey struct {
	Tenant, Cluster, Namespace, Name string
	HasNamespace                     bool
}
type batchPodResolver struct {
	source PodResolver
	values map[batchPodKey]*PodMetadata
}

// NewBatchPodResolver memoizes hits and misses for one sequential conversion
// batch only. It never survives the batch or changes the shared cache's TTL.
func NewBatchPodResolver(source PodResolver) PodResolver {
	if source == nil {
		return nil
	}
	return &batchPodResolver{source: source, values: make(map[batchPodKey]*PodMetadata)}
}
func (r *batchPodResolver) LookupPod(ctx context.Context, lookup PodLookup) (*PodMetadata, error) {
	key := batchPodKey{Tenant: lookup.Scope.TenantID, Cluster: lookup.ClusterID, Name: lookup.Name, HasNamespace: lookup.Namespace != nil}
	if lookup.Namespace != nil {
		key.Namespace = *lookup.Namespace
	}
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	value, err := r.source.LookupPod(ctx, lookup)
	if err == nil {
		r.values[key] = value
	}
	return value, err
}

func NewDjangoPodResolver(client PodCacheGetter, config PodCacheConfig) (*DjangoPodResolver, error) {
	if client == nil || config.Version < 0 {
		return nil, fmt.Errorf("Django Pod cache requires client and nonnegative version")
	}
	return &DjangoPodResolver{client: client, config: config}, nil
}

func (r *DjangoPodResolver) fallback(reason string) {
	if r.config.OnFallback == nil {
		return
	}
	r.mu.Lock()
	now := time.Now()
	report := now.Sub(r.lastWarning) >= time.Minute
	if report {
		r.lastWarning = now
	}
	r.mu.Unlock()
	if report {
		r.config.OnFallback(reason)
	}
}

// LookupPod only GETs the existing Django cache. It does not refresh TTL or
// create a second cache. Miss/corruption/unavailability use Python's no-instance
// target branch; no database or Python process is invoked.
func (r *DjangoPodResolver) LookupPod(ctx context.Context, lookup PodLookup) (*PodMetadata, error) {
	outcome := "error"
	defer func() {
		if r.config.Observe != nil {
			r.config.Observe(outcome)
		}
	}()

	namespace := "None"
	if lookup.Namespace != nil {
		namespace = *lookup.Namespace
	}
	key := fmt.Sprintf("%s:%d:bcs_pod:%s:%s:%s", r.config.KeyPrefix, r.config.Version, lookup.ClusterID, namespace, lookup.Name)
	raw, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != redis.Nil {
			r.fallback("cache_unavailable")
		} else {
			outcome = "miss"
		}
		return nil, nil
	}
	payload, err := decodeDjangoJSONString(raw)
	if err != nil {
		r.fallback("cache_decode_failed")
		return nil, nil
	}
	var record struct {
		PodMetadata
		Name       string `json:"name"`
		ClusterID  string `json:"bcs_cluster_id"`
		BusinessID int64  `json:"bk_biz_id"`
	}
	if json.Unmarshal(payload, &record) != nil || record.Name != lookup.Name || record.ClusterID != lookup.ClusterID || (lookup.Namespace != nil && *lookup.Namespace != "" && record.Namespace != *lookup.Namespace) {
		r.fallback("cache_identity_mismatch")
		return nil, nil
	}
	outcome = "hit"
	return &record.PodMetadata, nil
}

// decodeDjangoJSONString supports only pickle's single Unicode-string encoding
// (protocols 2-5), optionally wrapped by django-redis's ZlibCompressor. It is not
// a pickle VM: object construction, reducers and arbitrary opcodes are rejected.
func decodeDjangoJSONString(raw []byte) ([]byte, error) {
	if len(raw) > maxPodCacheBytes {
		return nil, fmt.Errorf("oversized Pod cache value")
	}
	if reader, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
		decoded, readErr := io.ReadAll(io.LimitReader(reader, maxPodCacheBytes+1))
		reader.Close()
		if readErr != nil || len(decoded) > maxPodCacheBytes {
			return nil, fmt.Errorf("invalid compressed Pod cache")
		}
		raw = decoded
	}
	if len(raw) < 3 || raw[0] != 0x80 || raw[1] < 2 || raw[1] > 5 {
		return nil, fmt.Errorf("unsupported Pod cache pickle protocol")
	}
	i := 2
	if raw[i] == 0x95 {
		if len(raw) < 11 || binary.LittleEndian.Uint64(raw[3:11]) != uint64(len(raw)-11) {
			return nil, fmt.Errorf("invalid pickle frame")
		}
		i = 11
	}
	if i >= len(raw) {
		return nil, fmt.Errorf("missing pickle string")
	}
	var length uint64
	switch raw[i] {
	case 0x8c:
		if len(raw) < i+2 {
			return nil, fmt.Errorf("truncated short unicode")
		}
		length = uint64(raw[i+1])
		i += 2
	case 'X':
		if len(raw) < i+5 {
			return nil, fmt.Errorf("truncated unicode")
		}
		length = uint64(binary.LittleEndian.Uint32(raw[i+1 : i+5]))
		i += 5
	case 0x8d:
		if len(raw) < i+9 {
			return nil, fmt.Errorf("truncated unicode8")
		}
		length = binary.LittleEndian.Uint64(raw[i+1 : i+9])
		i += 9
	default:
		return nil, fmt.Errorf("pickle value is not a string")
	}
	if length > uint64(len(raw)-i) {
		return nil, fmt.Errorf("truncated pickle string")
	}
	payload := raw[i : i+int(length)]
	i += int(length)
	if i < len(raw) {
		switch raw[i] {
		case 0x94:
			i++
		case 'q':
			i += 2
		case 'r':
			i += 5
		}
	}
	if i != len(raw)-1 || raw[i] != '.' || !utf8.Valid(payload) || !json.Valid(payload) {
		return nil, fmt.Errorf("invalid pickle string tail or JSON")
	}
	return payload, nil
}
