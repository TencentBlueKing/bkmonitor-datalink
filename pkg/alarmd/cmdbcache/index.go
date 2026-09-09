// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package cmdbcache reads the CMDB caches that bk-monitor-worker maintains, so
// alarmd can enrich a series with the facts its strategy filters on.
//
// The caches are a bypass around the Python alert chain: bmw refreshes them
// from CMDB by resource watch plus a periodic full pass, in this same
// repository. Reading them therefore does not tie the Go chain's completeness
// to Python being alive.
//
// The whole host cache is held in memory and replaced wholesale on refresh.
// A per-series Redis lookup would add roughly one round trip per evaluation on
// top of the runtime traffic; the cache is a few thousand hosts, so a snapshot
// costs a few megabytes and turns matching into a map lookup.
package cmdbcache

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

const (
	hostCacheSuffix        = "cache.cmdb.host"
	hostTopoRefreshedField = "cache.cmdb_last_refresh_all_time.host_topo"
	scanBatch              = int64(1000)
)

// HostFacts is what enrichment knows about one host.
//
// It is a record of facts, not a projection for one filter: new fullers add
// fields here and nothing downstream reads it positionally. TopoNodes is a set
// because a host belongs to every node on every topology link it has - a host
// in three modules carries three chains of business, set and module, and a
// target naming any one of them includes it.
type HostFacts struct {
	HostID      string
	IP          string
	CloudID     string
	BusinessID  string
	TopoNodes   []string
	State       string
	DisplayName string
}

// Index is an immutable snapshot. Refreshes publish a new one; readers keep
// using the one they hold, so a refresh never leaves a half-built view visible.
type Index struct {
	byIdentity        map[string]*HostFacts
	hosts             int
	builtAt           time.Time
	sourceRefreshedAt time.Time
}

func (index *Index) Hosts() int {
	if index == nil {
		return 0
	}
	return index.hosts
}

func (index *Index) BuiltAt() time.Time {
	if index == nil {
		return time.Time{}
	}
	return index.builtAt
}

// SourceRefreshedAt is when bmw last completed a full host-topology pass. It
// bounds how stale the facts can be independently of how recently alarmd read
// them: a fresh read of a stale cache is still stale.
func (index *Index) SourceRefreshedAt() time.Time {
	if index == nil {
		return time.Time{}
	}
	return index.sourceRefreshedAt
}

// Lookup resolves one identity key: either "ip|cloud" or a bare host id, the
// two shapes bmw writes into the same hash.
func (index *Index) Lookup(key string) (*HostFacts, bool) {
	if index == nil || key == "" {
		return nil, false
	}
	facts, found := index.byIdentity[key]
	return facts, found
}

type Reader struct {
	client redis.Cmdable
	prefix string
}

// NewReader builds a reader over the platform's CMDB cache. The prefix is the
// platform key prefix - the same one the strategy snapshot uses - and the
// cache key is derived from it, never configured separately: two spellings of
// one location is how a reader ends up silently pointed at nothing.
func NewReader(client redis.Cmdable, prefix string) (*Reader, error) {
	if client == nil {
		return nil, fmt.Errorf("alarmd cmdbcache: a redis client is required")
	}
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), ".")
	if prefix == "" {
		return nil, fmt.Errorf("alarmd cmdbcache: a non-empty platform key prefix is required")
	}
	return &Reader{client: client, prefix: prefix}, nil
}

func (reader *Reader) hostKey() string {
	return reader.prefix + "." + hostCacheSuffix
}

func (reader *Reader) refreshedKey() string {
	return reader.prefix + "." + hostTopoRefreshedField
}

// Load builds a fresh index. It streams the hash rather than reading it whole:
// the host cache is a single large hash shared with the platform, and a
// blocking full read of it would stall every other reader.
func (reader *Reader) Load(ctx context.Context, now time.Time) (*Index, error) {
	builder := newIndexBuilder(now)

	var cursor uint64
	for {
		fields, next, err := reader.client.HScan(ctx, reader.hostKey(), cursor, "", scanBatch).Result()
		if err != nil {
			return nil, fmt.Errorf("alarmd cmdbcache: scan host cache: %w", err)
		}
		builder.addFields(fields)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	index := builder.index

	if refreshed, err := reader.client.Get(ctx, reader.refreshedKey()).Result(); err == nil {
		index.sourceRefreshedAt = parseRefreshedAt(refreshed)
	}
	return index, nil
}

// indexBuilder accumulates scanned fields. It exists so the host-count and
// de-duplication rules can be exercised without a Redis.
type indexBuilder struct {
	index *Index
	seen  map[string]*HostFacts
}

func newIndexBuilder(now time.Time) *indexBuilder {
	return &indexBuilder{
		index: &Index{byIdentity: make(map[string]*HostFacts), builtAt: now},
		seen:  make(map[string]*HostFacts),
	}
}

// addFields consumes an HSCAN page: alternating field and value.
func (builder *indexBuilder) addFields(fields []string) {
	for position := 0; position+1 < len(fields); position += 2 {
		identity, payload := fields[position], fields[position+1]
		facts, err := decodeHost(payload)
		if err != nil {
			// One malformed record must not blind the whole filter.
			continue
		}
		// bmw writes every host twice, under its "ip|cloud" key and under its
		// host id. Both identities must resolve, but the host counts once and
		// shares one record - counting fields instead of hosts reports twice
		// the fleet.
		if facts.HostID != "" {
			if existing, found := builder.seen[facts.HostID]; found {
				builder.index.byIdentity[identity] = existing
				continue
			}
			builder.seen[facts.HostID] = facts
		}
		builder.index.hosts++
		builder.index.byIdentity[identity] = facts
	}
}

type wireHost struct {
	HostID      json.Number                  `json:"bk_host_id"`
	InnerIP     string                       `json:"bk_host_innerip"`
	CloudID     json.Number                  `json:"bk_cloud_id"`
	BusinessID  json.Number                  `json:"bk_biz_id"`
	State       string                       `json:"bk_state"`
	DisplayName string                       `json:"display_name"`
	TopoLinks   map[string][]json.RawMessage `json:"topo_link"`
}

type wireTopoNode struct {
	ObjectID   string      `json:"bk_obj_id"`
	InstanceID json.Number `json:"bk_inst_id"`
}

func decodeHost(payload string) (*HostFacts, error) {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	var wire wireHost
	if err := decoder.Decode(&wire); err != nil {
		return nil, err
	}
	facts := &HostFacts{
		HostID:      numberText(wire.HostID),
		IP:          wire.InnerIP,
		CloudID:     numberText(wire.CloudID),
		BusinessID:  numberText(wire.BusinessID),
		State:       wire.State,
		DisplayName: wire.DisplayName,
	}
	if facts.CloudID == "" {
		facts.CloudID = "0"
	}
	// Every link contributes its whole chain: a host in several modules sits
	// under several sets, and a target naming any of those nodes includes it.
	nodes := make(map[string]struct{})
	for _, link := range wire.TopoLinks {
		for _, raw := range link {
			var node wireTopoNode
			if err := json.Unmarshal(raw, &node); err != nil {
				continue
			}
			instance := numberText(node.InstanceID)
			if node.ObjectID == "" || instance == "" {
				continue
			}
			nodes[node.ObjectID+"|"+instance] = struct{}{}
		}
	}
	facts.TopoNodes = make([]string, 0, len(nodes))
	for node := range nodes {
		facts.TopoNodes = append(facts.TopoNodes, node)
	}
	return facts, nil
}

func numberText(value json.Number) string {
	text := strings.TrimSpace(value.String())
	if text == "" {
		return ""
	}
	if parsed, err := strconv.ParseFloat(text, 64); err == nil && parsed == float64(int64(parsed)) {
		return strconv.FormatInt(int64(parsed), 10)
	}
	return text
}

// parseRefreshedAt accepts the shapes bmw has used for the marker: epoch
// seconds, epoch milliseconds, or an RFC3339 stamp. An unreadable marker is
// reported as unknown rather than as "just refreshed".
func parseRefreshedAt(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds > 1e12 {
			return time.UnixMilli(seconds).UTC()
		}
		if seconds > 1e9 {
			return time.Unix(seconds, 0).UTC()
		}
		return time.Time{}
	}
	if stamp, err := time.Parse(time.RFC3339, value); err == nil {
		return stamp.UTC()
	}
	return time.Time{}
}
