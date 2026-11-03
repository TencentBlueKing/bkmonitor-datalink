// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// TargetPlanV1 is the second frozen form of a strategy's monitoring target,
// the one compiled from the strategy cache's target_plan document. It is
// kept apart from TargetScopeV2 on purpose: that form answers "is this
// record inside the target" from facts filled about the record, and it has
// no way to answer "who is the target right now", which is what a no-data
// roster needs. This form freezes the rule, the identity the record key is
// read from, the static member keys and the references to the dynamic
// members; the worker resolves the references once per Slot and both the
// admission filter and the roster read that one resolution.
//
// Members of a dynamic group or a topology node are never in here, so a
// membership change moves no digest and cuts no Segment. A Plan carries
// either this or TargetScopeV2, never both; compilation guarantees it and
// the plan compiler refuses a Plan that carries both.
type TargetPlanV1 struct {
	SchemaVersion int            `json:"schema_version"`
	ModelID       string         `json:"model_id"`
	Rule          TargetPlanRule `json:"target_rule"`
	// Identity is how a record's key is read, decided at compile time from
	// the rule and, for model_inst_id, from the representation the writer
	// named; the worker never looks at query configurations again.
	Identity TargetPlanIdentityV1 `json:"identity"`
	// StaticKeys are the member keys of the static targets, already in the
	// key form the rule defines, sorted and unique.
	StaticKeys []string `json:"static_keys"`
	// StaticBusinesses is the business the writer configured on a
	// Kubernetes static target, by that target's key. It is what a global
	// business Plan attributes an event on that target to. Only the static
	// targets that carry a business are here, and a key two targets give
	// different businesses is left out: neither is the target's business,
	// and the event falls through to the next source instead of taking one
	// of them by document order. Absent on every other plan, so their bytes
	// are what they were.
	StaticBusinesses map[string]string `json:"static_businesses,omitempty"`
	// StaticMembers are the static targets of a model_inst_id plan read by
	// host identity: the (model, instance) pairs as the writer spelled them,
	// sorted and unique, which the worker maps to host ids through the host
	// cache once per Slot. They are not keys - the key is the host id, and
	// only the cache knows it - so they live apart from StaticKeys, which is
	// empty on such a plan.
	StaticMembers []TargetPlanMemberV1 `json:"static_members,omitempty"`
	// ExcludeKeys and ExcludeMembers use the same identity as the included
	// static targets. Exclusions apply after all sources are resolved.
	ExcludeKeys    []string             `json:"exclude_keys,omitempty"`
	ExcludeMembers []TargetPlanMemberV1 `json:"exclude_members,omitempty"`
	// ExcludeHosts are ip_cloud exclusions by host id, resolved to addresses
	// from the same snapshot as the included hosts.
	ExcludeHosts []string `json:"exclude_hosts,omitempty"`
	// DynamicGroups are the dynamic group ids referenced, sorted and unique.
	DynamicGroups []string `json:"dynamic_groups,omitempty"`
	// DynamicTopologies are the topology node references, sorted and unique.
	DynamicTopologies []TargetPlanTopologyV1 `json:"dynamic_topologies,omitempty"`
	// TenantID is the tenant an ip_cloud plan's hosts and groups belong to,
	// the strategy's own: an address is a host only inside one tenant, so
	// the worker reads addresses, and checks hosts and groups, under it.
	// Absent on every other rule, so their bytes are what they were.
	TenantID string `json:"bk_tenant_id,omitempty"`
	// StaticHosts are the static targets of an ip_cloud plan: host ids, as
	// the writer names them, sorted and unique. The key is the host's
	// address, which only the host cache knows and which moves when the
	// host is readdressed, so the worker maps them once per Slot and they
	// live apart from StaticKeys, which is empty on such a plan.
	StaticHosts []string `json:"static_hosts,omitempty"`
}

// TargetPlanRule names which dimensions a result table identifies its
// object by. The five are a closed list from the protocol; a sixth is a row
// in targetPlanRules and a reading in the compiler, never a fallthrough.
type TargetPlanRule string

const (
	TargetPlanRuleHostID      TargetPlanRule = "host_id"
	TargetPlanRuleModelInstID TargetPlanRule = "model_inst_id"
	TargetPlanRuleK8sCluster  TargetPlanRule = "k8s_cluster"
	TargetPlanRuleK8sNode     TargetPlanRule = "k8s_node"
	TargetPlanRuleK8sWorkload TargetPlanRule = "k8s_workload"
	// TargetPlanRuleIPCloud reads a host by its IPv4 address and cloud area:
	// the rule of a host query that carries neither a host id nor an object
	// identity. Its members are hosts named by id, and the worker maps them
	// to addresses under the plan's tenant.
	TargetPlanRuleIPCloud          TargetPlanRule = "ip_cloud"
	TargetPlanFailurePolicyNoMatch                = "no_match"
)

// The record dimensions an ip_cloud key is read from, and the aliases the
// protocol takes as the same inputs. HostModelID is the one model an
// ip_cloud plan names.
const (
	IPCloudIPDimension         = "bk_target_ip"
	IPCloudCloudDimension      = "bk_target_cloud_id"
	IPCloudIPAliasDimension    = "ip"
	IPCloudCloudAliasDimension = "bk_cloud_id"
	HostModelID                = "cw-Host"
)

// TargetPlanKeySeparator joins the parts of a member key and of a record
// key. It is the platform's own separator: the existing frozen keys use it
// ("obj|inst", "model|inst") and the fork builds its Kubernetes instance ids
// with it.
const TargetPlanKeySeparator = "|"

// TargetPlanIdentityV1 says which dimensions a record's key is built from.
//
// Dimensions are read in order and joined with TargetPlanKeySeparator; a
// record missing any of them has no key. When ModelDimension is set the
// record must also carry that dimension equal to ModelValue, or it has no
// key: that is the model_inst_id rule under a writer that named the data's
// representation of the plan's model (model_match), where the model half is
// a gate and the key is the instance alone.
type TargetPlanIdentityV1 struct {
	Dimensions     []string `json:"dimensions"`
	ModelDimension string   `json:"model_dimension,omitempty"`
	ModelValue     string   `json:"model_value,omitempty"`
	// HostIdentity says the key is the record's host id, read from the
	// host identities the admission facts carry (the bk_host_id dimension
	// when the record has one, and otherwise the id the host cache teaches
	// the record from its address) rather than from a dimension by name.
	// It is the identity of the host_id rule, and of a model_inst_id plan
	// whose members are hosts: the writer names those by (model, instance)
	// and the host cache maps them to host ids. Dimensions is bk_host_id on
	// such an identity, which is how a no-data group of it is addressed.
	HostIdentity bool `json:"host_identity,omitempty"`
	// Address says the key is the record's IPv4 address and cloud area,
	// read by ReadIPCloudKey: from the protocol's dimensions or their
	// aliases, strictly, rather than as text by name. It is the identity of
	// the ip_cloud rule; Dimensions is bk_target_ip, bk_target_cloud_id,
	// which is how a no-data group of it is addressed.
	Address bool `json:"address,omitempty"`
}

// TargetPlanMemberV1 is one static member of a model_inst_id plan as the
// writer spelled it: the model code and the instance id.
type TargetPlanMemberV1 struct {
	ModelID     string `json:"model_id"`
	ModelInstID string `json:"model_inst_id"`
}

// HostIdentityDimension is the dimension a host-identity target's no-data
// group is addressed by, and the one dimension a record carries its host id
// under when it carries one at all.
const HostIdentityDimension = "bk_host_id"

// TargetPlanTopologyV1 is one dynamic topology reference: the business the
// node is read under and the node itself. All three are text because that
// is what the cache carries and what the key is built from.
type TargetPlanTopologyV1 struct {
	BusinessID string `json:"bk_biz_id"`
	ObjectID   string `json:"bk_obj_id"`
	InstanceID string `json:"bk_inst_id"`
}

// targetPlanRuleFacts is what the protocol fixes about each rule.
type targetPlanRuleFacts struct {
	// Dimensions are the record dimensions the rule reads, in key order, for
	// the rules whose dimensions are fixed. model_inst_id's are decided per
	// plan from the writer's representation and are empty here.
	Dimensions []string
	// Dynamic says whether the rule may carry dynamic references.
	Dynamic bool
}

var targetPlanRules = map[TargetPlanRule]targetPlanRuleFacts{
	TargetPlanRuleHostID:      {Dimensions: []string{HostIdentityDimension}, Dynamic: true},
	TargetPlanRuleModelInstID: {Dynamic: true},
	TargetPlanRuleK8sCluster:  {Dimensions: []string{"bcs_cluster_id"}},
	TargetPlanRuleK8sNode:     {Dimensions: []string{"bcs_cluster_id", "node"}},
	TargetPlanRuleK8sWorkload: {Dimensions: []string{"bcs_cluster_id", "namespace", "workload_kind", "workload_name"}},
	TargetPlanRuleIPCloud:     {Dimensions: []string{IPCloudIPDimension, IPCloudCloudDimension}, Dynamic: true},
}

// TargetPlanRules lists the rules in a stable order, for tests and reports.
func TargetPlanRules() []TargetPlanRule {
	rules := make([]TargetPlanRule, 0, len(targetPlanRules))
	for rule := range targetPlanRules {
		rules = append(rules, rule)
	}
	sort.Slice(rules, func(left, right int) bool { return rules[left] < rules[right] })
	return rules
}

// TargetPlanRuleDimensions returns the fixed record dimensions of a rule and
// false for a rule the table does not know. model_inst_id is known and has
// none fixed.
func TargetPlanRuleDimensions(rule TargetPlanRule) ([]string, bool) {
	facts, known := targetPlanRules[rule]
	if !known {
		return nil, false
	}
	return append([]string(nil), facts.Dimensions...), true
}

// TargetPlanRuleAllowsDynamic reports whether the rule may reference dynamic
// groups or topology nodes. The Kubernetes rules are static by protocol.
func TargetPlanRuleAllowsDynamic(rule TargetPlanRule) bool {
	return targetPlanRules[rule].Dynamic
}

// TargetPlanMemberKey builds a member key from its parts in rule order. It
// is the one place a key is spelled, so a static target, a group member, a
// topology host and a record all agree on the spelling.
func TargetPlanMemberKey(parts ...string) string {
	return strings.Join(parts, TargetPlanKeySeparator)
}

// Key reads a record's key from its dimensions as text, and false when the
// record cannot be placed: a dimension the identity reads is missing or
// empty, or the model gate does not hold. A record with no key is never in
// the target; the filter names that apart from an ordinary miss.
func (identity TargetPlanIdentityV1) Key(dimension func(name string) string) (string, bool) {
	if identity.ModelDimension != "" {
		if dimension(identity.ModelDimension) != identity.ModelValue {
			return "", false
		}
	}
	parts := make([]string, 0, len(identity.Dimensions))
	for _, name := range identity.Dimensions {
		value := dimension(name)
		if value == "" {
			return "", false
		}
		parts = append(parts, value)
	}
	if len(parts) == 0 {
		return "", false
	}
	return TargetPlanMemberKey(parts...), true
}

// MemberKey is the key of an object-model member under this identity: the
// instance alone behind a model gate, "model|instance" when the record
// carries the model code itself. A static target, a group member and a
// topology host all go through here, so the three sources and the record
// cannot spell the key differently.
func (identity TargetPlanIdentityV1) MemberKey(model, instance string) string {
	if identity.ModelDimension != "" {
		return TargetPlanMemberKey(instance)
	}
	return TargetPlanMemberKey(model, instance)
}

// RosterDimensions is the set of dimensions a no-data group of this target
// is addressed by: the key dimensions plus the model gate's dimension when
// there is one. A no-data roster can be built for the target exactly when
// the item's no-data dimensions are this set.
func (identity TargetPlanIdentityV1) RosterDimensions() []string {
	dimensions := append([]string(nil), identity.Dimensions...)
	if identity.ModelDimension != "" {
		dimensions = append(dimensions, identity.ModelDimension)
	}
	sort.Strings(dimensions)
	return dimensions
}

// Group turns a member key back into the dimension values a no-data group
// of this target carries, by dimension name. The last dimension takes
// whatever remains, so a separator inside the final value does not shift
// the split.
func (identity TargetPlanIdentityV1) Group(key string) (map[string]string, bool) {
	parts := strings.SplitN(key, TargetPlanKeySeparator, len(identity.Dimensions))
	if len(parts) != len(identity.Dimensions) {
		return nil, false
	}
	group := make(map[string]string, len(parts)+1)
	for index, name := range identity.Dimensions {
		if parts[index] == "" {
			return nil, false
		}
		group[name] = parts[index]
	}
	if identity.ModelDimension != "" {
		group[identity.ModelDimension] = identity.ModelValue
	}
	return group, true
}

// Validate checks the frozen form: a known rule, an identity that reads at
// least one dimension, canonical key and reference lists, and no dynamic
// reference on a static rule. Everything it refuses is a compiler defect,
// not a strategy the writer got wrong; the compiler refuses those by name
// before anything is frozen.
func (plan *TargetPlanV1) Validate() error {
	if plan == nil {
		return nil
	}
	if plan.SchemaVersion != 1 {
		return fmt.Errorf("alarmd contract: target plan schema version %d is not 1", plan.SchemaVersion)
	}
	if strings.TrimSpace(plan.ModelID) == "" {
		return errors.New("alarmd contract: a target plan names its model")
	}
	facts, known := targetPlanRules[plan.Rule]
	if !known {
		return fmt.Errorf("alarmd contract: unknown target plan rule %q", plan.Rule)
	}
	if len(plan.Identity.Dimensions) == 0 {
		return errors.New("alarmd contract: a target plan identity reads at least one dimension")
	}
	seen := make(map[string]struct{}, len(plan.Identity.Dimensions)+1)
	for _, name := range plan.Identity.Dimensions {
		if strings.TrimSpace(name) == "" {
			return errors.New("alarmd contract: target plan identity dimensions are non-empty names")
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("alarmd contract: target plan identity reads %q twice", name)
		}
		seen[name] = struct{}{}
	}
	if len(facts.Dimensions) > 0 && strings.Join(facts.Dimensions, ",") != strings.Join(plan.Identity.Dimensions, ",") {
		return fmt.Errorf("alarmd contract: rule %s reads %v, not %v", plan.Rule, facts.Dimensions, plan.Identity.Dimensions)
	}
	if (plan.Identity.ModelDimension == "") != (plan.Identity.ModelValue == "") {
		return errors.New("alarmd contract: a target plan model gate names both the dimension and the value")
	}
	if plan.Identity.ModelDimension != "" {
		if plan.Rule != TargetPlanRuleModelInstID {
			return fmt.Errorf("alarmd contract: only %s carries a model gate", TargetPlanRuleModelInstID)
		}
		if _, duplicate := seen[plan.Identity.ModelDimension]; duplicate {
			return errors.New("alarmd contract: the model gate dimension is not also a key dimension")
		}
	}
	if plan.StaticKeys == nil {
		return errors.New("alarmd contract: static keys are a list, empty when there are none")
	}
	if err := canonicalTargetPlanList("static keys", plan.StaticKeys); err != nil {
		return err
	}
	if err := canonicalTargetPlanList("exclude keys", plan.ExcludeKeys); err != nil {
		return err
	}
	if plan.HasExclusions() && !facts.Dynamic {
		return fmt.Errorf("alarmd contract: rule %s carries no exclusions", plan.Rule)
	}
	if plan.Rule == TargetPlanRuleModelInstID && plan.Identity.HostIdentity && len(plan.ExcludeKeys) > 0 {
		return errors.New("alarmd contract: model_inst_id exclusions read by host identity must name model members")
	}
	if (plan.Rule == TargetPlanRuleIPCloud) != plan.Identity.Address {
		return fmt.Errorf("alarmd contract: rule %s and only it reads a record's address", TargetPlanRuleIPCloud)
	}
	if plan.Rule == TargetPlanRuleIPCloud {
		if plan.ModelID != HostModelID || strings.TrimSpace(plan.TenantID) == "" || plan.Identity.HostIdentity {
			return fmt.Errorf("alarmd contract: an %s plan names %s, its tenant, and no host identity", TargetPlanRuleIPCloud, HostModelID)
		}
		if len(plan.StaticKeys) > 0 || len(plan.ExcludeKeys) > 0 {
			return fmt.Errorf("alarmd contract: an %s plan names its static targets by host, not by key", TargetPlanRuleIPCloud)
		}
		if err := canonicalTargetPlanList("static hosts", plan.StaticHosts); err != nil {
			return err
		}
		for _, host := range plan.StaticHosts {
			if !canonicalDecimalPattern.MatchString(host) || host == "0" {
				return errors.New("alarmd contract: a static host is a positive decimal id")
			}
		}
		if err := canonicalTargetPlanList("excluded hosts", plan.ExcludeHosts); err != nil {
			return err
		}
		for _, host := range plan.ExcludeHosts {
			if !canonicalDecimalPattern.MatchString(host) || host == "0" {
				return errors.New("alarmd contract: an excluded host is a positive decimal id")
			}
		}
	} else if plan.TenantID != "" || len(plan.StaticHosts) > 0 || len(plan.ExcludeHosts) > 0 {
		return fmt.Errorf("alarmd contract: only %s carries a tenant and static hosts", TargetPlanRuleIPCloud)
	}
	if plan.Identity.HostIdentity {
		if plan.Rule != TargetPlanRuleHostID && plan.Rule != TargetPlanRuleModelInstID {
			return fmt.Errorf("alarmd contract: rule %s does not read a host identity", plan.Rule)
		}
		if plan.Identity.ModelDimension != "" || strings.Join(plan.Identity.Dimensions, ",") != HostIdentityDimension {
			return fmt.Errorf("alarmd contract: a host identity reads %s and carries no model gate", HostIdentityDimension)
		}
	} else if plan.Rule == TargetPlanRuleHostID {
		// One reading per rule. A host_id plan read by the bk_host_id
		// dimension alone would drop every record that names its host by
		// address, and the two readings would be told apart by nothing on
		// the page.
		return fmt.Errorf("alarmd contract: rule %s reads the record's host identity", TargetPlanRuleHostID)
	}
	if len(plan.StaticMembers) > 0 {
		if plan.Rule != TargetPlanRuleModelInstID || !plan.Identity.HostIdentity {
			return errors.New("alarmd contract: static members belong to a model_inst_id plan read by host identity")
		}
		if len(plan.StaticKeys) > 0 {
			return errors.New("alarmd contract: a plan carries static members or static keys, not both")
		}
		for index, member := range plan.StaticMembers {
			if member.ModelID != plan.ModelID || strings.TrimSpace(member.ModelInstID) == "" {
				return errors.New("alarmd contract: a static member names the plan's model and a non-empty instance")
			}
			if index > 0 && !plan.StaticMembers[index-1].less(member) {
				return errors.New("alarmd contract: static members must be canonically ordered and unique")
			}
		}
	}
	if len(plan.StaticBusinesses) > 0 {
		if facts.Dynamic {
			return fmt.Errorf("alarmd contract: rule %s takes a target's business from the host, not the plan", plan.Rule)
		}
		for key, business := range plan.StaticBusinesses {
			if !containsSortedKey(plan.StaticKeys, key) {
				return errors.New("alarmd contract: a static business names a static key of the plan")
			}
			if !canonicalDecimalPattern.MatchString(business) || business == "0" {
				return errors.New("alarmd contract: a static business is a positive decimal")
			}
		}
	}
	if len(plan.ExcludeMembers) > 0 {
		if plan.Rule != TargetPlanRuleModelInstID || !plan.Identity.HostIdentity || len(plan.ExcludeKeys) > 0 {
			return errors.New("alarmd contract: excluded members belong to a model_inst_id plan read by host identity")
		}
		for index, member := range plan.ExcludeMembers {
			if member.ModelID != plan.ModelID || strings.TrimSpace(member.ModelInstID) == "" {
				return errors.New("alarmd contract: an excluded member names the plan's model and a non-empty instance")
			}
			if index > 0 && !plan.ExcludeMembers[index-1].less(member) {
				return errors.New("alarmd contract: excluded members must be canonically ordered and unique")
			}
		}
	}
	if err := canonicalTargetPlanList("dynamic groups", plan.DynamicGroups); err != nil {
		return err
	}
	if !facts.Dynamic && (len(plan.DynamicGroups) > 0 || len(plan.DynamicTopologies) > 0) {
		return fmt.Errorf("alarmd contract: rule %s is static and carries no dynamic reference", plan.Rule)
	}
	for index, node := range plan.DynamicTopologies {
		if node.BusinessID == "" || node.ObjectID == "" || node.InstanceID == "" {
			return errors.New("alarmd contract: a topology reference names business, object and instance")
		}
		if index > 0 && !plan.DynamicTopologies[index-1].less(node) {
			return errors.New("alarmd contract: topology references must be canonically ordered and unique")
		}
	}
	if len(plan.StaticKeys) == 0 && len(plan.StaticMembers) == 0 && len(plan.StaticHosts) == 0 &&
		len(plan.DynamicGroups) == 0 && len(plan.DynamicTopologies) == 0 {
		return errors.New("alarmd contract: a target plan that names nothing matches nothing and is refused at compile time")
	}
	return nil
}

// HasExclusions reports whether an older object reader would widen this plan.
func (plan *TargetPlanV1) HasExclusions() bool {
	return plan != nil && (len(plan.ExcludeKeys) > 0 || len(plan.ExcludeMembers) > 0 || len(plan.ExcludeHosts) > 0)
}

func (member TargetPlanMemberV1) less(other TargetPlanMemberV1) bool {
	if member.ModelID != other.ModelID {
		return member.ModelID < other.ModelID
	}
	return member.ModelInstID < other.ModelInstID
}

// SortTargetPlanMembers orders members canonically, in place.
func SortTargetPlanMembers(members []TargetPlanMemberV1) {
	sort.Slice(members, func(left, right int) bool { return members[left].less(members[right]) })
}

// HostKey is the key a host-identity target holds a host under, and the key
// such a target reads off a record's host identity.
func (identity TargetPlanIdentityV1) HostKey(hostID string) string {
	return TargetPlanMemberKey(hostID)
}

func (node TargetPlanTopologyV1) less(other TargetPlanTopologyV1) bool {
	if node.BusinessID != other.BusinessID {
		return node.BusinessID < other.BusinessID
	}
	if node.ObjectID != other.ObjectID {
		return node.ObjectID < other.ObjectID
	}
	return node.InstanceID < other.InstanceID
}

// Key is the topology reference in the spelling the resolver indexes by.
func (node TargetPlanTopologyV1) Key() string {
	return TargetPlanMemberKey(node.BusinessID, node.ObjectID, node.InstanceID)
}

// SortTargetPlanTopologies orders references canonically, in place.
func SortTargetPlanTopologies(nodes []TargetPlanTopologyV1) {
	sort.Slice(nodes, func(left, right int) bool { return nodes[left].less(nodes[right]) })
}

func containsSortedKey(keys []string, key string) bool {
	index := sort.SearchStrings(keys, key)
	return index < len(keys) && keys[index] == key
}

// StaticBusiness is the business configured on the static target a record
// key names, and false when that target carries none.
func (plan *TargetPlanV1) StaticBusiness(key string) (string, bool) {
	if plan == nil {
		return "", false
	}
	business, found := plan.StaticBusinesses[key]
	return business, found
}

func canonicalTargetPlanList(what string, values []string) error {
	for index, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("alarmd contract: target plan %s are non-empty text", what)
		}
		if index > 0 && values[index-1] >= value {
			return fmt.Errorf("alarmd contract: target plan %s must be canonically ordered and unique", what)
		}
	}
	return nil
}

// IPCloudKey is the member key of an address: the IPv4 address and the
// cloud area, both canonical, in rule order.
func IPCloudKey(ip, cloud string) string {
	return TargetPlanMemberKey(ip, cloud)
}

// ReadIPCloudKey reads a record's ip_cloud key from its dimensions as raw
// JSON, and false when the record cannot be placed. Each half is read from
// the protocol's dimension or its alias (ip, bk_cloud_id): when both are
// present both must read and agree, and a present value that does not read
// - null, a boolean, a fraction, a negative cloud area, text that is not a
// dotted IPv4 address in canonical form - places nothing, even beside an
// alias that reads. A record missing either half has no key. The cloud
// area 0 is a cloud area.
func ReadIPCloudKey(dimension func(name string) (json.RawMessage, bool)) (string, bool) {
	ip, ok := readIPCloudHalf(dimension, IPCloudIPDimension, IPCloudIPAliasDimension, CanonicalIPv4)
	if !ok {
		return "", false
	}
	cloud, ok := readIPCloudHalf(dimension, IPCloudCloudDimension, IPCloudCloudAliasDimension, CanonicalCloudArea)
	if !ok {
		return "", false
	}
	return IPCloudKey(ip, cloud), true
}

func readIPCloudHalf(dimension func(name string) (json.RawMessage, bool), name, alias string,
	parse func(json.RawMessage) (string, bool),
) (string, bool) {
	raw, present := dimension(name)
	aliasRaw, aliasPresent := dimension(alias)
	switch {
	case present && aliasPresent:
		value, ok := parse(raw)
		aliasValue, aliasOK := parse(aliasRaw)
		if !ok || !aliasOK || value != aliasValue {
			return "", false
		}
		return value, true
	case present:
		return parse(raw)
	case aliasPresent:
		return parse(aliasRaw)
	}
	return "", false
}

// CanonicalIPv4 reads a JSON string holding a dotted IPv4 address in its
// canonical form, and false for anything else: another type, an IPv6
// address, leading zeros, surrounding space.
func CanonicalIPv4(raw json.RawMessage) (string, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", false
	}
	// ParseAddr takes an IPv4 address only in its canonical dotted form: no
	// leading zeros, four octets, nothing around them.
	address, err := netip.ParseAddr(text)
	if err != nil || !address.Is4() {
		return "", false
	}
	return text, true
}

// CanonicalCloudArea reads a cloud area: a non-negative integer, as a JSON
// integer or a JSON string of decimal digits, in canonical decimal. Null, a
// boolean, a fraction, a negative number and text that is not a decimal do
// not read.
func CanonicalCloudArea(raw json.RawMessage) (string, bool) {
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, `"`) {
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", false
		}
	}
	if !canonicalDecimalPattern.MatchString(text) {
		return "", false
	}
	return text, true
}
