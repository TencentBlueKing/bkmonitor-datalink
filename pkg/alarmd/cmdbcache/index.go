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
	hostCacheSuffix            = "cache.cmdb.host"
	serviceInstanceCacheSuffix = "cache.cmdb.service_instance"
	topoCacheSuffix            = "cache.cmdb.topo"
	hostTopoRefreshedField     = "cache.cmdb_last_refresh_all_time.host_topo"
	scanBatch                  = int64(1000)
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
	// Attributes are the scalar top-level fields of the cache record as text,
	// by field name: bk_state, bk_os_type, bk_host_name and whatever else the
	// writer put there. They are decoded once here so a target on a host
	// attribute costs the filter a lookup, not a decode; no target reads
	// them yet.
	Attributes map[string]string
	// ModelID and ModelInstID are the canonical instance identity the writer
	// adds beside the host id. Empty on a record written before it did.
	ModelID     string
	ModelInstID string
}

// ServiceInstanceFacts is what enrichment knows about one service instance:
// the host it runs on and the topology of its module. An instance sits in
// exactly one module, so unlike a host it has one chain, and a target naming
// any node of that chain includes it.
type ServiceInstanceFacts struct {
	ID        string
	HostID    string
	IP        string
	CloudID   string
	TopoNodes []string
}

// Index is an immutable snapshot. Refreshes publish a new one; readers keep
// using the one they hold, so a refresh never leaves a half-built view visible.
type Index struct {
	byIdentity       map[string]*HostFacts
	hosts            int
	serviceInstances map[string]*ServiceInstanceFacts
	// byNode is the reverse of every host's topology links, keyed by
	// "biz|obj|inst": the hosts a dynamic topology reference resolves to.
	// Built once per load so a resolution is a lookup, never a scan.
	byNode map[string][]*HostFacts
	// hostedNodes are the "obj|inst" nodes that hold a host under any
	// business, so a reference under the wrong business can be told from a
	// node that holds no host anywhere.
	hostedNodes map[string]struct{}
	// topoNodes are the "obj|inst" fields of the topology cache, read so a
	// reference to a node that does not exist can be told apart from a node
	// that exists and holds no host. Nil when the topology cache was not
	// read; empty when it was read and holds nothing.
	topoNodes map[string]struct{}
	// byModelInstance is every host that carries the canonical (model,
	// instance) identity, keyed "model|instance": how a model_inst_id target
	// read by host identity finds the host a static member names.
	// modelledHosts counts them, so a cache the writer has not put the
	// identity on can be told from a model the cache knows no host of.
	byModelInstance   map[string]*HostFacts
	modelledHosts     int
	builtAt           time.Time
	sourceRefreshedAt time.Time
}

// LookupModelInstance finds the host carrying the canonical (model,
// instance) identity, and false when the cache lists none.
func (index *Index) LookupModelInstance(model, instance string) (*HostFacts, bool) {
	if index == nil || model == "" || instance == "" {
		return nil, false
	}
	facts, found := index.byModelInstance[model+"|"+instance]
	return facts, found
}

// ModelledHosts is how many hosts carry the canonical (model, instance)
// identity. Zero on a cache whose writer does not put it on hosts.
func (index *Index) ModelledHosts() int {
	if index == nil {
		return 0
	}
	return index.modelledHosts
}

// TopologyAnswer is what the index says about one dynamic topology
// reference.
type TopologyAnswer struct {
	// Resolved says the index can answer at all: it holds hosts and it read
	// the topology cache. Without it Hosts being empty means nothing.
	Resolved bool
	// NodeKnown says the topology cache lists the node. A reference to a
	// node the cache does not list is a dangling configuration, not an
	// empty node.
	NodeKnown bool
	// HostedElsewhere says the node holds hosts, but under another business
	// than the reference names: a node belongs to exactly one business, so
	// this is a reference written against the wrong business, not an empty
	// node either.
	HostedElsewhere bool
	Hosts           []*HostFacts
}

// Topology answers a dynamic topology reference from the reverse index. The
// hosts are read under the reference's business: the same node id under two
// businesses holds two host sets, and the reference names which. Whether
// the node exists is answered without the business, because the topology
// cache's field is "obj|inst" alone; a reference to a node that exists in
// another business therefore reads as a known node with no host here, not
// as a dangling one.
func (index *Index) Topology(businessID, objectID, instanceID string) TopologyAnswer {
	if index == nil || index.hosts == 0 || index.topoNodes == nil {
		return TopologyAnswer{}
	}
	node := objectID + "|" + instanceID
	_, known := index.topoNodes[node]
	hosts := index.byNode[businessID+"|"+node]
	_, hosted := index.hostedNodes[node]
	return TopologyAnswer{Resolved: true, NodeKnown: known, HostedElsewhere: len(hosts) == 0 && hosted, Hosts: hosts}
}

// TopologyNodes is how many nodes the topology cache listed, for health.
func (index *Index) TopologyNodes() int {
	if index == nil {
		return 0
	}
	return len(index.topoNodes)
}

func (index *Index) Hosts() int {
	if index == nil {
		return 0
	}
	return index.hosts
}

// ServiceInstances is how many service instances the index holds.
func (index *Index) ServiceInstances() int {
	if index == nil {
		return 0
	}
	return len(index.serviceInstances)
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

// LookupServiceInstance resolves one service-instance id.
func (index *Index) LookupServiceInstance(id string) (*ServiceInstanceFacts, bool) {
	if index == nil || id == "" {
		return nil, false
	}
	facts, found := index.serviceInstances[id]
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

func (reader *Reader) serviceInstanceKey() string {
	return reader.prefix + "." + serviceInstanceCacheSuffix
}

func (reader *Reader) refreshedKey() string {
	return reader.prefix + "." + hostTopoRefreshedField
}

func (reader *Reader) topoKey() string {
	return reader.prefix + "." + topoCacheSuffix
}

// Load builds a fresh index. It streams the hash rather than reading it whole:
// the host cache is a single large hash shared with the platform, and a
// blocking full read of it would stall every other reader.
func (reader *Reader) Load(ctx context.Context, now time.Time) (*Index, error) {
	builder := newIndexBuilder(now)

	if err := reader.scan(ctx, reader.hostKey(), builder.addFields); err != nil {
		return nil, fmt.Errorf("alarmd cmdbcache: scan host cache: %w", err)
	}
	// The service-instance cache is a second hash the same writer maintains
	// beside the host one. It is read into the same snapshot so the two
	// cannot be from different refreshes: an instance resolved against a
	// host index older than itself would place it under a module the host
	// has since left.
	if err := reader.scan(ctx, reader.serviceInstanceKey(), builder.addServiceInstanceFields); err != nil {
		return nil, fmt.Errorf("alarmd cmdbcache: scan service instance cache: %w", err)
	}
	// The topology cache is read for its field names only: the node set a
	// dynamic topology reference is checked against. HKEYS is one round
	// trip over a hash of thousands of nodes, small beside the host scan.
	nodes, err := reader.client.HKeys(ctx, reader.topoKey()).Result()
	if err != nil {
		return nil, fmt.Errorf("alarmd cmdbcache: read topology cache: %w", err)
	}
	builder.addTopologyNodes(nodes)
	index := builder.index

	if refreshed, err := reader.client.Get(ctx, reader.refreshedKey()).Result(); err == nil {
		index.sourceRefreshedAt = parseRefreshedAt(refreshed)
	}
	return index, nil
}

// scan streams one hash through consume, a page at a time.
func (reader *Reader) scan(ctx context.Context, key string, consume func([]string)) error {
	var cursor uint64
	for {
		fields, next, err := reader.client.HScan(ctx, key, cursor, "", scanBatch).Result()
		if err != nil {
			return err
		}
		consume(fields)
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

// indexBuilder accumulates scanned fields. It exists so the host-count and
// de-duplication rules can be exercised without a Redis.
type indexBuilder struct {
	index *Index
	seen  map[string]*HostFacts
}

func newIndexBuilder(now time.Time) *indexBuilder {
	return &indexBuilder{
		index: &Index{
			byIdentity: make(map[string]*HostFacts), serviceInstances: make(map[string]*ServiceInstanceFacts),
			byNode: make(map[string][]*HostFacts), hostedNodes: make(map[string]struct{}), builtAt: now,
			byModelInstance: make(map[string]*HostFacts),
		},
		seen: make(map[string]*HostFacts),
	}
}

// addTopologyNodes records the node set the topology cache lists.
func (builder *indexBuilder) addTopologyNodes(fields []string) {
	builder.index.topoNodes = make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if field == "" {
			continue
		}
		builder.index.topoNodes[field] = struct{}{}
	}
}

// addToNodes files a host under every node it sits on, under its business.
func (builder *indexBuilder) addToNodes(facts *HostFacts) {
	if facts.BusinessID == "" {
		return
	}
	for _, node := range facts.TopoNodes {
		key := facts.BusinessID + "|" + node
		builder.index.byNode[key] = append(builder.index.byNode[key], facts)
		builder.index.hostedNodes[node] = struct{}{}
	}
}

// addServiceInstanceFields consumes an HSCAN page of the service-instance
// hash: alternating instance id and record. The record carries its own id;
// the field is what the writer keyed it by, and a disagreement between the
// two is the writer's defect, so the field wins as the lookup key.
func (builder *indexBuilder) addServiceInstanceFields(fields []string) {
	for position := 0; position+1 < len(fields); position += 2 {
		identity, payload := fields[position], fields[position+1]
		facts, err := decodeServiceInstance(payload)
		if err != nil {
			continue
		}
		if facts.ID == "" {
			facts.ID = identity
		}
		builder.index.serviceInstances[identity] = facts
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
		builder.addToNodes(facts)
		if facts.ModelID != "" && facts.ModelInstID != "" && facts.HostID != "" {
			builder.index.modelledHosts++
			builder.index.byModelInstance[facts.ModelID+"|"+facts.ModelInstID] = facts
		}
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
	// ModelID and ModelInstID are the canonical instance identity the
	// writer adds beside the host id, for a target plan whose rule reads
	// records by model and instance.
	ModelID     string          `json:"model_id"`
	ModelInstID json.RawMessage `json:"model_inst_id"`
}

type wireServiceInstance struct {
	ID        json.Number                  `json:"service_instance_id"`
	HostID    json.Number                  `json:"bk_host_id"`
	IP        string                       `json:"ip"`
	CloudID   json.Number                  `json:"bk_cloud_id"`
	TopoLinks map[string][]json.RawMessage `json:"topo_link"`
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
		TopoNodes:   topoNodes(wire.TopoLinks),
		Attributes:  scalarAttributes(payload),
		ModelID:     wire.ModelID,
		ModelInstID: rawScalarText(wire.ModelInstID),
	}
	if facts.CloudID == "" {
		facts.CloudID = "0"
	}
	return facts, nil
}

func decodeServiceInstance(payload string) (*ServiceInstanceFacts, error) {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	var wire wireServiceInstance
	if err := decoder.Decode(&wire); err != nil {
		return nil, err
	}
	facts := &ServiceInstanceFacts{
		ID:        numberText(wire.ID),
		HostID:    numberText(wire.HostID),
		IP:        wire.IP,
		CloudID:   numberText(wire.CloudID),
		TopoNodes: topoNodes(wire.TopoLinks),
	}
	if facts.CloudID == "" {
		// Python's fuller writes the instance's cloud as it is; the cache
		// writer takes it from the host, where an absent cloud is the direct
		// area, the same default the host decoder applies.
		facts.CloudID = "0"
	}
	return facts, nil
}

// topoNodes flattens the links of a record into its node set. Every link
// contributes its whole chain: a host in several modules sits under several
// sets, and a target naming any of those nodes includes it.
func topoNodes(links map[string][]json.RawMessage) []string {
	nodes := make(map[string]struct{})
	for _, link := range links {
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
	flat := make([]string, 0, len(nodes))
	for node := range nodes {
		flat = append(flat, node)
	}
	return flat
}

// scalarAttributes reads the top-level string, number and boolean fields of
// a cache record as text. Objects and arrays are not attributes a target can
// name a value of, so they are left out rather than flattened by a rule
// nobody asked for.
func scalarAttributes(payload string) map[string]string {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil {
		return nil
	}
	attributes := make(map[string]string, len(fields))
	for name, raw := range fields {
		if len(raw) == 0 {
			continue
		}
		switch raw[0] {
		case '"':
			var text string
			if err := json.Unmarshal(raw, &text); err == nil {
				attributes[name] = text
			}
		case 't', 'f':
			attributes[name] = string(raw)
		case '{', '[', 'n':
			continue
		default:
			attributes[name] = numberText(json.Number(raw))
		}
	}
	if len(attributes) == 0 {
		return nil
	}
	return attributes
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

// rawScalarText reads a JSON string or number as text, without the "zero is
// absent" rule numberText applies and without failing the record: a model
// instance id may be any text, the platform emits it as a string, and a
// shape this reader does not expect is an empty identity, not a lost host.
func rawScalarText(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return ""
	}
	return numberText(number)
}
