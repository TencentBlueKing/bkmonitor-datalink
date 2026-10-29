// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// HostTopologyFuller is the CMDB half of enrichment: it turns the host
// identities a series carries into the topology nodes that host belongs to,
// plus the host attributes later filters act on.
//
// It is the counterpart of Python's access fuller, and the reason enrichment
// is a stage rather than part of the filter: the next thing to add - more host
// attributes, service-instance topology, container facts - lands here without
// touching the filters or the call site.
//
// Everything it learns goes into the facts and nothing into the dimensions.
// Python writes bk_topo_node, bk_target_ip and bk_host_id back into the
// record. Here the series' identity and its State stay on the record's own
// dimensions, and for a host placed by bk_host_id or bk_agent_id - or a
// record placed through its service instance (ServiceInstanceTopologyFuller)
// - the facts carry what Python writes (Facts.HostOverlay): the alert
// identity reads the
// record with it written over, so it moves when CMDB moves the host, as
// Python's does.
type HostTopologyFuller struct {
	stores StoreSource
	// acceptStale places on an index past its staleness bound, for a reader
	// that labels rather than decides (attribution); admission does not.
	acceptStale bool
}

// NewHostTopologyFuller places records against the host index of the tenant
// each is read for (Facts.TenantID).
// PrimeFor waits, within ctx, for the tenant's host index to be first read
// (admission.Chain.PrimeFor).
func (fuller *HostTopologyFuller) PrimeFor(ctx context.Context, tenant string) {
	if fuller != nil {
		PrimeTenant(ctx, fuller.stores, tenant)
	}
}

func NewHostTopologyFuller(stores StoreSource) *HostTopologyFuller {
	return &HostTopologyFuller{stores: stores}
}

func (*HostTopologyFuller) Name() string { return "cmdb_host_topology" }

func (fuller *HostTopologyFuller) Fill(dimensions map[string]json.RawMessage, facts *admission.Facts) {
	store := fuller.tenantStore(facts.TenantID)
	agent := admission.TruthyDimension(dimensions, "bk_agent_id")
	if store == nil {
		// No store for the tenant at all: nothing will ever be read, so a
		// host-placed record goes out under its own address, as Python's
		// does when CMDB holds no host for it.
		if facts.HostNaming.IDKey != "" || agent != "" {
			facts.HostOverlaySource = admission.HostIdentityRawNoIndex
		}
		facts.MarkFactsUnavailable(admission.FactsUnavailableHostIndex)
		return
	}
	index, unusable := store.Usable()
	if unusable != "" && !(fuller.acceptStale && unusable == IndexStale) {
		// The alert's identity is not a decision: it is read from the last
		// index that held hosts (Store.IdentityHost), so an index past its
		// bound or an empty one does not file a host-placed record's alert
		// under its raw address for as long as it lasts. With no such index
		// at all the record's identity cannot be read, and it is said so.
		source := admission.HostOverlayFromLastPopulatedIndex
		if unusable == IndexStale {
			source = admission.HostOverlayFromStaleIndex
		}
		identityOverlay(store, dimensions, facts, agent, source)
		// Never loaded, past the staleness bound, or empty (Store.judge). A
		// filter that acts on "CMDB does not know this host" has to tell that
		// apart from "CMDB was not asked": past the bound a host added since
		// is unknown and a host moved since sits in its old modules, and an
		// empty cache is a cache nobody writes, not a fleet without hosts -
		// deciding on either would drop host-named series and put targeted
		// ones out of scope one ordinary-looking rejection at a time. The
		// facts are unavailable, by name, which the filters already hold:
		// keep the alerts, leave the gap in the counter (decision-013 section
		// 5.1 item 4).
		facts.MarkFactsUnavailable(admission.FactsUnavailableHostIndex)
		return
	}
	// Which host the record is about is decided the way Python's
	// TopoNodeFuller decides it (fullers.py:55-110): by bk_host_id when the
	// record carries a true one, else by bk_agent_id, else by service
	// instance, else by address,
	// stopping at the first that resolves. Its host status filter and its
	// target match then read the record as the fuller left it, so the host
	// those judge is the one this order picks - and only that host: the
	// identities of a record are not a union of everything that resolves.
	if id := facts.HostNaming.IDKey; id != "" {
		if host, found := index.Lookup(id); found {
			placeByID(facts, id, host)
			facts.PlacedByHostID = true
			facts.ReportedAddress = reportedAddress(dimensions, host)
			facts.HostOverlay = hostOverlay(dimensions, host, facts.ReportedAddress)
			return
		}
		// An id CMDB does not know is a host CMDB does not know: the status
		// filter looks it up by that id and nothing else, whatever the
		// address below resolves to. Python still takes the topology from
		// the address, and so does this. The alert's identity reads the
		// identity index, which still holds a host this load refused; a
		// host absent from a clean load is gone, and keeps its raw address.
		identityOverlay(store, dimensions, facts, "", admission.HostOverlayFromLastPopulatedIndex)
	} else if agent != "" {
		// No true id: Python looks the host up by agent next
		// (fullers.py:57-74), and treats it as found by id.
		host, found, unreadable := index.LookupAgent(agent)
		if unreadable {
			// The agent hash of this load could not be read: the alert's
			// identity reads the last one that could.
			identityOverlay(store, dimensions, facts, agent, admission.HostOverlayFromLastPopulatedIndex)
			facts.MarkFactsUnavailable(admission.FactsUnavailableHostIndex)
			return
		}
		if found {
			placeByAgent(facts, host)
			facts.PlacedByHostID = true
			facts.ReportedAddress = reportedAddress(dimensions, host)
			facts.HostOverlay = hostOverlay(dimensions, host, facts.ReportedAddress)
			return
		}
		// An agent this load does not place: the alert's identity reads the
		// identity index, which still holds a host this load refused.
		identityOverlay(store, dimensions, facts, agent, admission.HostOverlayFromLastPopulatedIndex)
	}
	// Past this point a true id the record carried is one CMDB does not know:
	// whatever else places the record's topology, the host the status filter
	// judges is that unknown id.
	unknownID := facts.HostNaming.IDKey != ""
	if instanceResolves(index, facts) {
		// Python's service-instance branch comes before the address one and
		// returns when it resolves; the next fuller is that branch.
		facts.HostUnresolved = unknownID
		return
	}
	address, cloud := admission.FullerAddress(dimensions)
	if address != "" {
		if host, found := index.Lookup(address + "|" + cloud); found {
			placeByAddress(dimensions, facts, cloud, host)
			resolveHostState(index, facts)
			facts.HostUnresolved = unknownID
			return
		}
	}
	resolveHostState(index, facts)
	// Named, and placed by nothing: an id, an address or alias, or an
	// instance the cache did not find.
	facts.HostUnresolved = unknownID || agent != "" || address != "" || len(facts.ServiceInstanceKeys()) > 0
}

// identityOverlay sets a record's host overlay from the store's identity
// index (Store.IdentityHost, IdentityAgent) - for its alert's identity only,
// with no placement, so no filter or scope decides on it - by its true
// bk_host_id, else by agent. A record that names either while the store has
// never held an index with hosts is held (Facts.HostIdentityUnavailable)
// only while no read of the cache has succeeded and the store has not been
// trying for longer than its hold bound (Store.IdentityPending): the
// identity may yet be read. After a read that found no host - an empty
// cache, or one never written - or past the bound, the record goes out under
// its own address, as Python's does when CMDB holds no host for it
// (admission.HostIdentityRawNoIndex).
func identityOverlay(store *Store, dimensions map[string]json.RawMessage, facts *admission.Facts, agent, source string) {
	id := facts.HostNaming.IDKey
	if id == "" && agent == "" {
		return
	}
	if !store.IdentityHeld() {
		if !store.IdentityPending() {
			facts.HostOverlaySource = admission.HostIdentityRawNoIndex
			return
		}
		facts.HostIdentityUnavailable = true
		return
	}
	var host *HostFacts
	var found bool
	if id != "" {
		host, found = store.IdentityHost(id)
	} else {
		host, found, _ = store.IdentityAgent(agent)
	}
	if found {
		facts.IdentityHostFound = true
		facts.HostOverlay = hostOverlay(dimensions, host, reportedAddress(dimensions, host))
		if facts.HostOverlay != nil {
			facts.HostOverlaySource = source
		}
	}
}

// instanceOverlay is what Python's TopoNodeFuller writes into a record whose
// service instance it placed (fullers.py:76-89): the instance's address as
// bk_target_ip and its cloud as bk_target_cloud_id, as the platform holds it
// - a number, copied from the host when the cache was built, where the host
// branch writes str(); count_md5 hashes the two alike, so the fingerprint
// does not depend on it. No bk_host_id, which that branch never writes, and
// no bk_topo_node: the instance's chain is in the facts, which the readers of
// the topology take over the record's own. Nil when the record already names
// the instance's address and cloud, as the host overlay is, and for an
// instance without an address - one whose host the cache writer did not hold
// when it built the instance, where Python's fuller raises - which keeps the
// record's own address and is named (admission.HostIdentityInstanceWithoutAddress).
func instanceOverlay(dimensions map[string]json.RawMessage, instance *ServiceInstanceFacts) map[string]json.RawMessage {
	if instance.IP == "" {
		return nil
	}
	cloud := instance.CloudID
	if cloud == "" {
		cloud = "0"
	}
	recordCloud := admission.TruthyDimension(dimensions, "bk_target_cloud_id")
	if recordCloud == "" {
		recordCloud = "0"
	}
	if admission.TruthyDimension(dimensions, "bk_target_ip") == instance.IP && recordCloud == cloud {
		return nil
	}
	address, _ := json.Marshal(instance.IP)
	cloudValue := json.RawMessage(cloud)
	if !numericText(cloud) {
		cloudValue, _ = json.Marshal(cloud)
	}
	return map[string]json.RawMessage{"bk_target_ip": address, "bk_target_cloud_id": cloudValue}
}

// numericText is whether a cloud id is written as a JSON number: decimal
// digits, as the cache holds a cloud.
func numericText(value string) bool {
	if value == "" || len(value) > 18 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// overlayInstance sets an instance-placed record's host overlay from the
// instance, and names an instance that has no address; nothing when the
// record's host was placed by id or agent, which Python's fuller returns on
// before it reaches the instance.
func overlayInstance(dimensions map[string]json.RawMessage, facts *admission.Facts, instance *ServiceInstanceFacts, source string) {
	if facts.IdentityHostFound {
		return
	}
	if instance.IP == "" {
		// Named only from the index in force (source ""): a fresh instance
		// cache built without the host. From the identity index the series
		// is already counted as its facts unavailable.
		facts.HostOverlay, facts.HostOverlaySource = nil, ""
		if source == "" {
			facts.HostOverlaySource = admission.HostIdentityInstanceWithoutAddress
		}
		return
	}
	facts.HostOverlay = instanceOverlay(dimensions, instance)
	facts.HostOverlaySource = ""
	if facts.HostOverlay != nil {
		facts.HostOverlaySource = source
	}
}

// tenantStore is the store of the tenant a record is read for, nil when
// there is none to read.
func (fuller *HostTopologyFuller) tenantStore(tenant string) *Store {
	if fuller == nil || fuller.stores == nil {
		return nil
	}
	return fuller.stores.For(tenant)
}

// placeByAgent is Python's host-by-agent branch, which is its host-by-id
// branch reached another way: the host's address and topology replace the
// record's, and its id is written when the record has no bk_host_id
// dimension at all (fullers.py:66-74).
// reportedAddress is how the record's own bk_target_ip compares with the
// address of the host CMDB placed it at by id or agent: the same, another
// one, none (missing or empty), or not a string. Python's fuller overwrites
// the dimension with the host's address on that branch (fullers.py:57-74).
// Exact: the record's string against CMDB's, as Python's dedupe would see
// them. Only an address that is not the same names the host and, for
// another address, what was reported; the same address allocates nothing.
func reportedAddress(dimensions map[string]json.RawMessage, host *HostFacts) admission.ReportedAddress {
	differs := func(cause, reported string) admission.ReportedAddress {
		if len(reported) > admission.MaxReportedAddressBytes {
			reported = strings.ToValidUTF8(reported[:admission.MaxReportedAddressBytes], "")
		}
		return admission.ReportedAddress{Cause: cause, Reported: reported, CMDB: host.IP, HostID: host.HostID, AgentID: host.AgentID}
	}
	raw, present := dimensions["bk_target_ip"]
	if !present {
		return differs(admission.AddressMissing, "")
	}
	// A quoted string without an escape is its own value byte for byte:
	// compared as it is, with nothing decoded or allocated on a path every
	// series placed by id takes. Anything else is decoded.
	if n := len(raw); n >= 2 && raw[0] == '"' && raw[n-1] == '"' && bytes.IndexByte(raw, '\\') < 0 {
		switch value := raw[1 : n-1]; {
		case string(value) == host.IP:
			return admission.ReportedAddress{Cause: admission.AddressSame}
		case len(value) == 0:
			return differs(admission.AddressMissing, "")
		default:
			return differs(admission.AddressDiffers, string(value))
		}
	}
	var reported string
	if err := json.Unmarshal(raw, &reported); err != nil {
		return differs(admission.AddressUndecodable, "")
	}
	switch {
	case reported == host.IP:
		return admission.ReportedAddress{Cause: admission.AddressSame}
	case reported == "":
		return differs(admission.AddressMissing, "")
	default:
		return differs(admission.AddressDiffers, reported)
	}
}

func placeByAgent(facts *admission.Facts, host *HostFacts) {
	naming := &facts.HostNaming
	if !naming.NamedID {
		naming.NamedID, naming.IDKey = true, host.HostID
	}
	keys := make([]string, 0, 2)
	if naming.IDKey != "" {
		keys = append(keys, naming.IDKey)
	}
	if host.IP != "" {
		keys = append(keys, host.IP+"|"+host.CloudID)
	}
	facts.Set(contract.AttributeHostIdentity, keys)
	facts.SetTopoNodes(host.TopoNodes)
	naming.NamedAddress, naming.NamedCloud = true, true
	naming.AddressKey = host.IP + "|" + host.CloudID
	naming.Usable = true
	facts.HostResolved = true
	facts.HostState, facts.HostBusinessID = host.State, host.BusinessID
}

// placeByID is Python's host-by-id branch: the host's own address and cloud
// replace the record's, its topology is the only topology, and nothing else
// is consulted (fullers.py:61-74). The address the record arrived with no
// longer counts for a target - a host that changed its address, or an address
// reused by another host, would otherwise put the record in two hosts' scope.
func placeByID(facts *admission.Facts, id string, host *HostFacts) {
	keys := []string{id}
	if host.IP != "" {
		keys = append(keys, host.IP+"|"+host.CloudID)
	}
	facts.Set(contract.AttributeHostIdentity, keys)
	facts.SetTopoNodes(host.TopoNodes)
	facts.HostNaming.NamedAddress, facts.HostNaming.NamedCloud = true, true
	facts.HostNaming.AddressKey = host.IP + "|" + host.CloudID
	facts.HostResolved = true
	facts.HostState, facts.HostBusinessID = host.State, host.BusinessID
}

// placeByAddress is Python's address branch (fullers.py:92-110): it writes the
// cloud it looked the host up in and the host's topology, and the host's id
// when the record has no bk_host_id dimension at all - one that is there but
// empty is left as it is. bk_target_ip is not written. The status filter then
// finds the host by the written id, which is how a host spelled ip, or given
// without its cloud, is judged by its state.
func placeByAddress(dimensions map[string]json.RawMessage, facts *admission.Facts, cloud string, host *HostFacts) {
	facts.SetTopoNodes(host.TopoNodes)
	naming := &facts.HostNaming
	if !naming.NamedID {
		naming.NamedID, naming.IDKey = true, host.HostID
	}
	naming.NamedCloud = true
	naming.AddressKey = admission.LookupAddressKey(admission.TruthyDimension(dimensions, "bk_target_ip"), cloud)
	naming.Usable = admission.TruthyDimension(dimensions, "bk_target_ip") != "" || naming.IDKey != ""
	keys := make([]string, 0, 2)
	if naming.IDKey != "" {
		keys = append(keys, naming.IDKey)
	}
	if address := admission.TargetAddressKey(dimensions, cloud); address != "" {
		keys = append(keys, address)
	}
	facts.Set(contract.AttributeHostIdentity, keys)
}

// instanceResolves is whether Python's service-instance branch would place the
// record: the instance cache holds instances and one of the record's resolves.
// An empty instance cache resolves nothing there, so Python goes on to the
// address; the instance fuller names that gap separately.
func instanceResolves(index *Index, facts *admission.Facts) bool {
	if index.ServiceInstances() == 0 {
		return false
	}
	for _, key := range facts.ServiceInstanceKeys() {
		if _, found := index.LookupServiceInstance(key); found {
			return true
		}
	}
	return false
}

// resolveHostState answers whether CMDB knows the host the record names and
// what it says about it, by the one identity Python would look up.
func resolveHostState(index *Index, facts *admission.Facts) {
	key, looked := facts.HostNaming.LookupKey()
	if !looked {
		return
	}
	host, found := index.Lookup(key)
	if !found {
		facts.HostResolved, facts.HostState, facts.HostBusinessID = false, "", ""
		return
	}
	facts.HostResolved = true
	facts.HostState, facts.HostBusinessID = host.State, host.BusinessID
}

// ServiceInstanceTopologyFuller resolves a series that names a service
// instance to the instance's module topology and to the host it runs on.
//
// It reproduces the second branch of Python's TopoNodeFuller.full, including
// its precedence: the instance is consulted only when the record did not
// resolve a host by id (Python returns before reaching the instance when a
// bk_host_id lookup succeeds), and when it resolves, the instance's chain
// replaces whatever the record's own address resolved to and the instance's
// host becomes the host the state filter judges - Python overwrites
// bk_target_ip and bk_topo_node, and its host status filter then reads the
// overwritten values. Here those become facts: the topology attribute, the
// host identity attribute and HostNaming change; the dimensions do not, and
// the address and cloud Python writes are what the alert identity reads,
// carried as the record's host overlay (instanceOverlay).
type ServiceInstanceTopologyFuller struct {
	stores      StoreSource
	acceptStale bool
}

// NewServiceInstanceTopologyFuller places instance records against the
// index of the tenant each is read for.
// PrimeFor waits, within ctx, for the tenant's host index to be first read
// (admission.Chain.PrimeFor).
func (fuller *ServiceInstanceTopologyFuller) PrimeFor(ctx context.Context, tenant string) {
	if fuller != nil {
		PrimeTenant(ctx, fuller.stores, tenant)
	}
}

func NewServiceInstanceTopologyFuller(stores StoreSource) *ServiceInstanceTopologyFuller {
	return &ServiceInstanceTopologyFuller{stores: stores}
}

func (*ServiceInstanceTopologyFuller) Name() string { return "cmdb_service_instance_topology" }

func (fuller *ServiceInstanceTopologyFuller) Fill(dimensions map[string]json.RawMessage, facts *admission.Facts) {
	instanceKeys := facts.ServiceInstanceKeys()
	if len(instanceKeys) == 0 {
		// Not instance data; nothing here applies.
		return
	}
	if facts.HostResolved {
		// Python's fuller returns once it has placed a host, and the
		// instance is not asked again: by id or by agent (fullers.py:61-74),
		// the agent also when the record's bk_host_id is there but empty; or
		// by address (fullers.py:92-110), which is reached only when no
		// instance resolved - an empty instance cache among those, which
		// would otherwise be marked unavailable here.
		return
	}
	var store *Store
	if fuller != nil && fuller.stores != nil {
		store = fuller.stores.For(facts.TenantID)
	}
	if store == nil {
		facts.MarkFactsUnavailable(admission.FactsUnavailableServiceInstanceIndex)
		return
	}
	index, unusable := store.Usable()
	if (unusable != "" && !(fuller.acceptStale && unusable == IndexStale)) || index.ServiceInstances() == 0 {
		// The alert's identity is not a decision: it reads the instances of
		// the last load that held any (Store.IdentityServiceInstance), so a
		// stale or empty index does not file an instance-placed record's
		// alert under its own address while it lasts.
		source := admission.HostOverlayFromLastPopulatedIndex
		if unusable == IndexStale {
			source = admission.HostOverlayFromStaleIndex
		}
		fuller.identityInstance(store, dimensions, facts, instanceKeys, source)
		// The same reading as an empty host cache: a series that names an
		// instance while the instance cache holds none is the signature of a
		// cache nobody writes, not of a fleet without instances. Deciding on
		// it would drop every instance-scoped series as unplaceable, one
		// ordinary-looking rejection at a time. The gap is named instead, and
		// separately from the host index, because the two are written by
		// different jobs.
		facts.MarkFactsUnavailable(admission.FactsUnavailableServiceInstanceIndex)
		return
	}
	for _, key := range instanceKeys {
		instance, found := index.LookupServiceInstance(key)
		if !found {
			continue
		}
		// The instance's chain replaces the address-resolved topology rather
		// than joining it: Python assigns bk_topo_node here, and a service
		// target names the instance's module, not every module its host is
		// in.
		facts.SetTopoNodes(instance.TopoNodes)
		// The host identity the record can be matched by is now the
		// instance's host. Python assigns bk_target_ip from the instance, so
		// an address the record arrived with no longer counts; the id the
		// record carried is kept because Python keeps bk_host_id as it was.
		// The instance's own bk_host_id is not added: Python's instance
		// branch never writes it, so is_match sees only the record's id and
		// the instance's address. Adding it would be a superset -- an eq host
		// target would admit more and a neq host target would drop more than
		// Python, and the second is silent.
		hostKeys := make([]string, 0, 2)
		if facts.HostNaming.IDKey != "" {
			hostKeys = append(hostKeys, facts.HostNaming.IDKey)
		}
		if instance.IP != "" {
			hostKeys = append(hostKeys, instance.IP+"|"+instance.CloudID)
		}
		facts.Set(contract.AttributeHostIdentity, hostKeys)
		// Python writes the instance's address and cloud into the record, so
		// its host status filter looks the host up by them - unless the
		// record carried a bk_host_id, which that filter reads first and
		// which stays as it was. HostNaming says the same thing without the
		// write.
		facts.HostNaming.NamedAddress, facts.HostNaming.NamedCloud = true, true
		facts.HostNaming.AddressKey = instance.IP + "|" + instance.CloudID
		facts.HostNaming.Usable = facts.HostNaming.Usable || instance.IP != ""
		resolveHostState(index, facts)
		overlayInstance(dimensions, facts, instance, "")
		return
	}
	// No instance in this load: the identity may still read one a refusing
	// load left out.
	fuller.identityInstance(store, dimensions, facts, instanceKeys, admission.HostOverlayFromLastPopulatedIndex)
}

// identityInstance overlays a record from the instance identity
// (Store.IdentityServiceInstance) by the first of its instance keys the
// identity holds. Before the store's first read has succeeded, a record
// with nothing else to name its host is held, as a host-placed one is; after
// it, an identity without the instance leaves the record as it is.
func (fuller *ServiceInstanceTopologyFuller) identityInstance(store *Store, dimensions map[string]json.RawMessage,
	facts *admission.Facts, keys []string, source string) {
	if facts.IdentityHostFound {
		return
	}
	if !store.IdentityInstancesHeld() {
		if store.IdentityPending() {
			facts.HostIdentityUnavailable = true
		}
		return
	}
	for _, key := range keys {
		if instance, found := store.IdentityServiceInstance(key); found {
			overlayInstance(dimensions, facts, instance, source)
			return
		}
	}
}

// hostOverlay is what Python's TopoNodeFuller writes into a record whose
// host CMDB placed by bk_host_id or bk_agent_id (fullers.py:57-74): the
// host's address as bk_target_ip, its cloud as bk_target_cloud_id - "0"
// for none, as the platform writes the cloud it looked up in - and its id
// as bk_host_id, a string, only when the record has no bk_host_id key at all
// (fullers.py:66-71). Nil when the record's own address is the host's
// (reported, the comparison the record was placed with) and its cloud reads
// as the host's: the alert identity then reads the same either way, and the
// record carries nothing - which is every such record on a deployment whose
// records name their host's own address.
//
// bk_topo_node is not written: the readers of the topology take the placed
// host's chain from the facts, which take precedence over the record's own
// (admission/identity_fuller.go), as Python's written bk_topo_node does.
func hostOverlay(dimensions map[string]json.RawMessage, host *HostFacts, reported admission.ReportedAddress) map[string]json.RawMessage {
	cloud := host.CloudID
	if cloud == "" {
		cloud = "0"
	}
	recordCloud := admission.TruthyDimension(dimensions, "bk_target_cloud_id")
	if recordCloud == "" {
		recordCloud = "0"
	}
	if reported.Cause == admission.AddressSame && recordCloud == cloud {
		return nil
	}
	address, _ := json.Marshal(host.IP)
	cloudValue, _ := json.Marshal(cloud)
	overlay := map[string]json.RawMessage{"bk_target_ip": address, "bk_target_cloud_id": cloudValue}
	if _, named := dimensions["bk_host_id"]; !named && host.HostID != "" {
		overlay["bk_host_id"], _ = json.Marshal(host.HostID)
	}
	return overlay
}
