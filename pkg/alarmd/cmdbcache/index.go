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
	"unicode/utf8"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const (
	hostCacheSuffix            = "cache.cmdb.host"
	serviceInstanceCacheSuffix = "cache.cmdb.service_instance"
	topoCacheSuffix            = "cache.cmdb.topo"
	hostTopoRefreshedField     = "cache.cmdb_last_refresh_all_time.host_topo"
	scanBatch                  = int64(1000)
	// clusterBusinessCacheSuffix is the BCS cluster -> business hash the
	// platform's CMDB cache writer publishes beside the host hash, in the
	// same round: field the cluster id, value the business id in decimal.
	clusterBusinessCacheSuffix = "cache.cmdb.bcs_cluster_business"
	// MaxClusterBusinesses bounds the clusters one load keeps. A tenant has
	// clusters by the hundreds, a large one by the thousands; a hash past
	// this is not a cluster list, and loading it whole would put an
	// unbounded writer mistake into every replica's memory. Clusters past
	// the bound are counted as truncated and attribute as unmapped.
	MaxClusterBusinesses = 1 << 16
	// namespaceBusinessCacheSuffix is the BCS cluster + namespace ->
	// business hash published beside it: field "cluster|namespace", value
	// the business id in decimal.
	namespaceBusinessCacheSuffix = "cache.cmdb.bcs_namespace_business"
	// MaxNamespaceBusinesses bounds the namespaces one load keeps, on the
	// same reasoning as MaxClusterBusinesses at a namespace's scale.
	MaxNamespaceBusinesses = 1 << 18
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
	byIdentity map[string]*HostFacts
	// byHostID is the canonical host presence table already used to deduplicate
	// a load. An absent address is not evidence that the host was deleted.
	byHostID          map[string]*HostFacts
	hostIDsIncomplete bool
	hosts             int
	serviceInstances  map[string]*ServiceInstanceFacts
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
	byModelInstance map[string]*HostFacts
	modelledHosts   int
	// addressOf is, by host id, the tenant and ip_cloud key of every host
	// the writer put a whole target address on: how an ip_cloud target maps
	// its hosts to the keys records are read by. hostsAt is, by
	// "tenant|ip|cloud", the hosts at that address and how many: an address
	// two hosts of one tenant share names neither of them. Both hold only
	// such hosts, so a cache whose writer puts no target address on its
	// hosts costs nothing here.
	addressOf         map[string]hostAddress
	hostsAt           map[string]addressHosts
	builtAt           time.Time
	sourceRefreshedAt time.Time
	// clusterBusiness is the business of each BCS cluster the writer
	// published, by cluster id: how a global business Plan's event on
	// Kubernetes data that names no business finds the one it belongs to.
	// Read in the same load as the hosts, and optional to it: a mapping
	// that cannot be read does not stop the hosts from refreshing.
	clusterBusiness businessMapping
	// namespaceBusiness is the business of each namespace of a cluster, by
	// "cluster|namespace", consulted before the cluster's own business: a
	// namespace of a cluster shared across businesses belongs to the
	// business using it. Optional in the same way.
	namespaceBusiness businessMapping
	// refused is what this load read of the host and service instance
	// hashes and could not use.
	refused RefusedRecords
}

// RefusedRecords is what one load read of the host and service instance
// hashes and could not use: fields whose record does not decode, and the
// topology nodes of decoded records that do not decode to an object and a
// numeric instance. Each is taken as absent, as it always was - a record
// the writer got wrong is the same as one it deleted - and a node lost
// this way is one a topology target silently does not match. These say how
// many, and name the first of each by the hash field it was read under, so
// the record can be read back from the cache.
//
// Hosts counts fields, not hosts: the writer publishes every host under
// its "ip|cloud" field and its host id field, so one bad host record is
// usually two, and a record that does not decode cannot say which host it
// is. A topology node is counted once per record, and a host's once, from
// the first of its two fields that is read.
type RefusedRecords struct {
	Hosts                int
	ServiceInstances     int
	TopoNodes            int
	FirstHost            string
	FirstServiceInstance string
	// FirstTopoNode names the record the first refused node was in, by its
	// hash and field: "host:<field>" or "service_instance:<field>".
	FirstTopoNode string
}

// maxRefusedFieldBytes bounds a field name kept as the first refused of
// its kind. The name comes from the writer and goes on to a log line and to
// every replica's fleet snapshot; the writer's own names are tens of bytes.
const maxRefusedFieldBytes = 256

// refusedField is a field name as kept for naming a refused record: cut to
// maxRefusedFieldBytes on a character boundary.
func refusedField(field string) string {
	if len(field) <= maxRefusedFieldBytes {
		return field
	}
	cut := maxRefusedFieldBytes
	for cut > 0 && !utf8.RuneStart(field[cut]) {
		cut--
	}
	return field[:cut]
}

// SameCounts says whether two loads refused as many of each.
func (refused RefusedRecords) SameCounts(other RefusedRecords) bool {
	return refused.Hosts == other.Hosts && refused.ServiceInstances == other.ServiceInstances &&
		refused.TopoNodes == other.TopoNodes
}

func (refused *RefusedRecords) host(field string) {
	if refused.Hosts == 0 {
		refused.FirstHost = refusedField(field)
	}
	refused.Hosts++
}

func (refused *RefusedRecords) serviceInstance(field string) {
	if refused.ServiceInstances == 0 {
		refused.FirstServiceInstance = refusedField(field)
	}
	refused.ServiceInstances++
}

func (refused *RefusedRecords) topoNodes(record, field string, nodes int) {
	if nodes == 0 {
		return
	}
	if refused.TopoNodes == 0 {
		refused.FirstTopoNode = record + ":" + refusedField(field)
	}
	refused.TopoNodes += nodes
}

// MappingStats describes one published business mapping the index read:
// the entries held, the fields the latest load that read the hash left out
// as not a positive business or past the bound, and whether the latest load
// could not read it or read it empty after one that held entries (the
// entries are then an earlier load's).
type MappingStats struct {
	Held       int
	Refused    int
	Truncated  int
	ReadFailed bool
	Emptied    bool
}

func (mapping businessMapping) stats() MappingStats {
	return MappingStats{Held: len(mapping.entries), Refused: mapping.refused, Truncated: mapping.truncated,
		ReadFailed: mapping.readFailed, Emptied: mapping.emptied}
}

// businessMapping is one published "key -> business" hash read into the
// index: the entries held, and the fields the load left out - refused for a
// business that is not a positive integer or an empty key, truncated past
// the bound.
//
// readFailed says this load could not read the hash at all. The mapping is
// optional beside the hosts it is published with, so a read that fails -
// a hash the writer wrote as another type, one scan that timed out - must
// not hold back the host index every strategy filters on. The load goes on
// without it and says so; the store then carries the entries of the index
// it replaces, so one failed read does not turn every mapped event into an
// unmapped one, and the flag stays up until a read succeeds.
//
// emptied says this load read the hash empty while the index it replaces
// held entries. The writer publishes an empty mapping by deleting the hash,
// which is also what a source that answered nothing looks like, and read as
// fact it would file every global business event under its strategy's own
// business at once. The store carries the previous entries as it does for a
// failed read, and the flag stays up until a load holds entries again. A
// mapping never held stays empty: that is a writer not publishing it yet.
type businessMapping struct {
	entries    map[string]string
	refused    int
	truncated  int
	readFailed bool
	emptied    bool
}

// add records one page of field, value pairs, up to bound entries.
func (mapping *businessMapping) add(fields []string, bound int) {
	if mapping.entries == nil {
		mapping.entries = make(map[string]string)
	}
	for index := 0; index+1 < len(fields); index += 2 {
		key := strings.TrimSpace(fields[index])
		if key == "" {
			mapping.refused++
			continue
		}
		if _, seen := mapping.entries[key]; seen {
			continue
		}
		business, err := strconv.ParseInt(strings.TrimSpace(fields[index+1]), 10, 64)
		if err != nil || business <= 0 {
			mapping.refused++
			continue
		}
		if len(mapping.entries) >= bound {
			mapping.truncated++
			continue
		}
		mapping.entries[key] = strconv.FormatInt(business, 10)
	}
}

// carriedFrom is this mapping, or the entries of the previous one when this
// load could not read it or read it empty after one that held entries, with
// the reason flagged.
func (mapping businessMapping) carriedFrom(previous businessMapping) businessMapping {
	switch {
	case mapping.readFailed:
		previous.readFailed = true
		return previous
	case len(mapping.entries) == 0 && len(previous.entries) > 0:
		// This load read the hash: what it left out is its own count, and a
		// hash whose every field was refused reads as refused, not as gone.
		previous.refused, previous.truncated = mapping.refused, mapping.truncated
		previous.readFailed, previous.emptied = false, true
		return previous
	}
	return mapping
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

// LookupClusterBusiness is the business the writer published for one BCS
// cluster, and false for a cluster it did not publish.
func (index *Index) LookupClusterBusiness(clusterID string) (string, bool) {
	if index == nil || clusterID == "" {
		return "", false
	}
	business, found := index.clusterBusiness.entries[clusterID]
	return business, found
}

// LookupNamespaceBusiness is the business the writer published for one
// namespace of one BCS cluster, and false for a pair it did not publish.
func (index *Index) LookupNamespaceBusiness(clusterID, namespace string) (string, bool) {
	if index == nil || clusterID == "" || namespace == "" {
		return "", false
	}
	business, found := index.namespaceBusiness.entries[clusterID+"|"+namespace]
	return business, found
}

// ClusterBusinessStats describes the cluster mapping the index holds.
func (index *Index) ClusterBusinessStats() MappingStats {
	if index == nil {
		return MappingStats{}
	}
	return index.clusterBusiness.stats()
}

// NamespaceBusinessStats describes the namespace mapping the index holds.
func (index *Index) NamespaceBusinessStats() MappingStats {
	if index == nil {
		return MappingStats{}
	}
	return index.namespaceBusiness.stats()
}

// Refused is what the load that built this index read and could not use.
func (index *Index) Refused() RefusedRecords {
	if index == nil {
		return RefusedRecords{}
	}
	return index.refused
}

// carryOptional takes over, from the index this one replaces, the optional
// mappings this load could not read or read empty while that index held
// entries (see carriedFrom).
func (index *Index) carryOptional(previous *Index) {
	if index == nil || previous == nil {
		return
	}
	index.clusterBusiness = index.clusterBusiness.carriedFrom(previous.clusterBusiness)
	index.namespaceBusiness = index.namespaceBusiness.carriedFrom(previous.namespaceBusiness)
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

// HostCacheKey and ServiceInstanceCacheKey are the two hashes this reader
// loads, for a reader that looks up one record in them instead.
func (reader *Reader) HostCacheKey() string { return reader.hostKey() }

func (reader *Reader) ServiceInstanceCacheKey() string { return reader.serviceInstanceKey() }

func (reader *Reader) refreshedKey() string {
	return reader.prefix + "." + hostTopoRefreshedField
}

func (reader *Reader) topoKey() string {
	return reader.prefix + "." + topoCacheSuffix
}

func (reader *Reader) clusterBusinessKey() string {
	return reader.prefix + "." + clusterBusinessCacheSuffix
}

func (reader *Reader) namespaceBusinessKey() string {
	return reader.prefix + "." + namespaceBusinessCacheSuffix
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
	// The cluster mapping is a small hash the same writer publishes in the
	// same round as the hosts, read into the same snapshot so a host and a
	// cluster are never attributed from two refreshes. An absent hash is a
	// writer that does not publish it yet: no cluster is mapped, and every
	// event that would have used one is counted as unmapped - unless the
	// index before held entries, which the store then carries (emptied, see
	// businessMapping). A hash that cannot be read is not a failed load.
	builder.index.clusterBusiness = reader.readMapping(ctx, reader.clusterBusinessKey(), MaxClusterBusinesses)
	builder.index.namespaceBusiness = reader.readMapping(ctx, reader.namespaceBusinessKey(), MaxNamespaceBusinesses)
	index := builder.index

	if refreshed, err := reader.client.Get(ctx, reader.refreshedKey()).Result(); err == nil {
		index.sourceRefreshedAt = parseRefreshedAt(refreshed)
	}
	return index, nil
}

// readMapping reads one optional "key -> business" hash. A read that fails
// part way is dropped whole: half a mapping would map some clusters from
// this round and leave the rest unmapped, which reads like a writer that
// left them out.
func (reader *Reader) readMapping(ctx context.Context, key string, bound int) businessMapping {
	var mapping businessMapping
	if err := reader.scan(ctx, key, func(fields []string) { mapping.add(fields, bound) }); err != nil {
		return businessMapping{readFailed: true}
	}
	return mapping
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
	seen := make(map[string]*HostFacts)
	return &indexBuilder{
		index: &Index{
			byIdentity: make(map[string]*HostFacts), byHostID: seen, serviceInstances: make(map[string]*ServiceInstanceFacts),
			byNode: make(map[string][]*HostFacts), hostedNodes: make(map[string]struct{}), builtAt: now,
			byModelInstance: make(map[string]*HostFacts),
			addressOf:       make(map[string]hostAddress), hostsAt: make(map[string]addressHosts),
		},
		seen: seen,
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
		facts, refusedNodes, err := DecodeServiceInstanceRecord(identity, payload)
		if err != nil {
			builder.index.refused.serviceInstance(identity)
			continue
		}
		builder.index.refused.topoNodes("service_instance", identity, refusedNodes)
		builder.index.serviceInstances[identity] = facts
	}
}

// addFields consumes an HSCAN page: alternating field and value.
func (builder *indexBuilder) addFields(fields []string) {
	for position := 0; position+1 < len(fields); position += 2 {
		identity, payload := fields[position], fields[position+1]
		wire, err := decodeWireHost(payload)
		if err != nil {
			// One malformed record must not blind the whole filter.
			builder.index.refused.host(identity)
			continue
		}
		// bmw writes every host twice, under its "ip|cloud" key and under its
		// host id. Both identities must resolve, but the host counts once and
		// shares one record - counting fields instead of hosts reports twice
		// the fleet. The second is known by its host id before the rest of it
		// is read: its topology and its attributes were built only to be
		// dropped, and the attributes alone were most of a refresh's
		// allocation.
		hostID := numberText(wire.HostID)
		if parsed, err := strconv.ParseInt(hostID, 10, 64); err != nil || parsed <= 0 {
			builder.index.hostIDsIncomplete = true
		}
		if hostID != "" {
			if existing, found := builder.seen[hostID]; found {
				builder.index.byIdentity[identity] = existing
				continue
			}
		}
		facts, refusedNodes := hostFactsOf(wire, payload)
		builder.index.refused.topoNodes("host", identity, refusedNodes)
		if hostID != "" {
			builder.seen[hostID] = facts
		}
		builder.index.hosts++
		builder.index.byIdentity[identity] = facts
		builder.addToNodes(facts)
		if facts.ModelID != "" && facts.ModelInstID != "" && facts.HostID != "" {
			builder.index.modelledHosts++
			builder.index.byModelInstance[facts.ModelID+"|"+facts.ModelInstID] = facts
		}
		builder.addAddress(wire, facts)
	}
}

// addAddress records a host's tenant and target address, when the writer
// put both on it whole: an address that does not read, like one missing
// half, is no address, and the host cannot be an ip_cloud member.
func (builder *indexBuilder) addAddress(wire wireHost, facts *HostFacts) {
	tenant := strings.TrimSpace(wire.TenantID)
	if wire.TargetAddress == nil || facts.HostID == "" || tenant == "" {
		return
	}
	ip, ipRead := contract.CanonicalIPv4(wire.TargetAddress.IP)
	cloud, cloudRead := contract.CanonicalCloudArea(wire.TargetAddress.CloudID)
	if !ipRead || !cloudRead {
		return
	}
	key := contract.IPCloudKey(ip, cloud)
	builder.index.addressOf[facts.HostID] = hostAddress{tenant: tenant, key: key}
	at := builder.index.hostsAt[tenant+"|"+key]
	at.count++
	at.host = facts
	if at.count > 1 {
		at.host = nil
	}
	builder.index.hostsAt[tenant+"|"+key] = at
}

// HostAddress is the tenant and ip_cloud key of the host the id names, and
// false when the writer put no whole target address on it.
func (index *Index) HostAddress(hostID string) (tenant, key string, found bool) {
	if index == nil {
		return "", "", false
	}
	address, found := index.addressOf[hostID]
	return address.tenant, address.key, found
}

// AddressHost is the host at an address of a tenant, and how many hosts of
// the tenant are at it: nil beside a count above one, which is an address
// that names no one host.
func (index *Index) AddressHost(tenant, key string) (*HostFacts, int) {
	if index == nil {
		return nil, 0
	}
	at := index.hostsAt[tenant+"|"+key]
	return at.host, at.count
}

// hostAddress is one host's tenant and the ip_cloud key of its target
// address.
type hostAddress struct {
	tenant, key string
}

// addressHosts is the host at one tenant's address, when there is exactly
// one, and how many there are.
type addressHosts struct {
	host  *HostFacts
	count int
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
	// TenantID and TargetAddress are the host's tenant and its trusted IPv4
	// address and cloud area, which the writer puts on a host only when its
	// source carries both whole. An ip_cloud target reads hosts by them.
	TenantID      string             `json:"bk_tenant_id"`
	TargetAddress *wireTargetAddress `json:"target_address"`
}

type wireTargetAddress struct {
	IP      json.RawMessage `json:"bk_target_ip"`
	CloudID json.RawMessage `json:"bk_target_cloud_id"`
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
	wire, err := decodeWireHost(payload)
	if err != nil {
		return nil, err
	}
	facts, _ := hostFactsOf(wire, payload)
	return facts, nil
}

// decodeWireHost is the one step of reading a host record that can refuse
// it. What follows it -- the topology nodes, the scalar attributes -- reads
// the same payload again and cannot fail, and is the bulk of the cost.
func decodeWireHost(payload string) (wireHost, error) {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	var wire wireHost
	if err := decoder.Decode(&wire); err != nil {
		return wireHost{}, err
	}
	return wire, nil
}

// DecodeHostRecord reads one host record as a load reads it: the facts the
// index would hold for it, or the error that has a load skip it. A load
// takes a record it cannot decode as absent and only counts it, naming the
// first by its field (Index.Refused), so this is how one such record is read
// back and told apart from a host the cache does not have.
func DecodeHostRecord(payload string) (*HostFacts, error) {
	return decodeHost(payload)
}

// DecodeServiceInstanceRecord is DecodeHostRecord for the service-instance
// hash, whose load it is: the record under field, its id the field's when
// the record names none, and how many of its topology nodes were refused.
func DecodeServiceInstanceRecord(field, payload string) (*ServiceInstanceFacts, int, error) {
	facts, refusedNodes, err := decodeServiceInstance(payload)
	if err != nil {
		return nil, 0, err
	}
	if facts.ID == "" {
		facts.ID = field
	}
	return facts, refusedNodes, nil
}

// hostFactsOf is a host record's facts from its decoded fields and its
// payload, and how many of its topology nodes were refused.
func hostFactsOf(wire wireHost, payload string) (*HostFacts, int) {
	nodes, refusedNodes := topoNodes(wire.TopoLinks)
	facts := &HostFacts{
		HostID:      numberText(wire.HostID),
		IP:          wire.InnerIP,
		CloudID:     numberText(wire.CloudID),
		BusinessID:  numberText(wire.BusinessID),
		State:       wire.State,
		DisplayName: wire.DisplayName,
		TopoNodes:   nodes,
		Attributes:  scalarAttributes(payload),
		ModelID:     strings.TrimSpace(wire.ModelID),
		ModelInstID: rawScalarText(wire.ModelInstID),
	}
	if facts.CloudID == "" {
		facts.CloudID = "0"
	}
	return facts, refusedNodes
}

// decodeServiceInstance reads one service instance record, and how many of
// its topology nodes were refused.
func decodeServiceInstance(payload string) (*ServiceInstanceFacts, int, error) {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	var wire wireServiceInstance
	if err := decoder.Decode(&wire); err != nil {
		return nil, 0, err
	}
	nodes, refusedNodes := topoNodes(wire.TopoLinks)
	facts := &ServiceInstanceFacts{
		ID:        numberText(wire.ID),
		HostID:    numberText(wire.HostID),
		IP:        wire.IP,
		CloudID:   numberText(wire.CloudID),
		TopoNodes: nodes,
	}
	if facts.CloudID == "" {
		// Python's fuller writes the instance's cloud as it is; the cache
		// writer takes it from the host, where an absent cloud is the direct
		// area, the same default the host decoder applies.
		facts.CloudID = "0"
	}
	return facts, refusedNodes, nil
}

// topoNodes flattens the links of a record into its node set. Every link
// contributes its whole chain: a host in several modules sits under several
// sets, and a target naming any of those nodes includes it. A node that
// does not decode to an object and a numeric instance is left out and
// counted as refused; the chains of one record share their upper nodes, so
// a refused node is counted once per record by its text, as the nodes kept
// are by their key.
func topoNodes(links map[string][]json.RawMessage) ([]string, int) {
	nodes := make(map[string]struct{})
	var refused map[string]struct{}
	refuse := func(raw json.RawMessage) {
		if refused == nil {
			refused = make(map[string]struct{})
		}
		refused[string(raw)] = struct{}{}
	}
	for _, link := range links {
		for _, raw := range link {
			var node wireTopoNode
			if err := json.Unmarshal(raw, &node); err != nil {
				refuse(raw)
				continue
			}
			instance := numberText(node.InstanceID)
			if node.ObjectID == "" || instance == "" {
				refuse(raw)
				continue
			}
			nodes[node.ObjectID+"|"+instance] = struct{}{}
		}
	}
	flat := make([]string, 0, len(nodes))
	for node := range nodes {
		flat = append(flat, node)
	}
	return flat, len(refused)
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
