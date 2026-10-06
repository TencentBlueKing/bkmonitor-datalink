// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"fmt"
	"sort"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Host dimension names, as the backend's host scenario writes them into the
// groups it expects.
const (
	HostIPDimension    = "bk_target_ip"
	HostCloudDimension = "bk_target_cloud_id"
)

// HostIdentity is one host a target resolved to. Both halves are text because
// that is what the group holds: the backend runs format_dicts_value_to_str over
// its instances before anything compares or hashes them.
type HostIdentity struct {
	IP      string
	CloudID string
}

// RosterClass is what the expected set for one item is derived from: which of
// the five combinations of target shape and no-data dimensions this item is,
// and, for the one that names hosts, which hosts the target declares.
//
// It is one derivation. The compiler refuses a Plan it cannot build a roster
// for, and the Slot builds the roster; those two have to agree about every
// combination or the second lock is not one. A compiler that lets through what
// the derivation refuses produces a Plan that errors every round, and one that
// refuses what the derivation would have built loses detection silently. Two
// predicates cannot be made to agree by testing each; one has nothing to
// disagree with.
type RosterClass struct {
	Source RosterSource
	// Hosts is the target's declared host list, and only TARGET_STATIC has one.
	// It is what the target says rather than what exists: the caller intersects
	// it with the hosts the CMDB index holds, as the backend intersects its
	// target values with the business's hosts.
	Hosts []HostIdentity
	// Plan is the frozen target plan, and only TARGET_PLAN has one. Its
	// identity is what turns a resolved member key back into the group the
	// data reports under.
	Plan *contract.TargetPlanV1
}

// ClassifyTarget decides the roster class of an item from whichever frozen
// target form its Plan carries. The target plan is read first and alone:
// a Plan carrying one has no TargetScope, and letting the old classification
// see a nil scope would declare a history roster for a target the strategy
// stated - the expected set would grow out of the memory and the target
// would decide nothing.
//
// A target plan can express a roster exactly when the item's no-data
// dimensions are the dimensions its record key is read from - the member
// keys then split back into groups the data reports under. Any other
// dimension set is refused by name rather than approximated as history or
// as the whole item: a superset would need a cross product nobody can
// enumerate, and the whole item is a semantics this build does not claim
// for the new form.
func ClassifyTarget(scope *contract.TargetScopeV2, plan *contract.TargetPlanV1, aggDimension []string) (RosterClass, error) {
	if plan == nil {
		return ClassifyRoster(scope, aggDimension)
	}
	if !sameDimensionSet(plan.Identity.RosterDimensions(), aggDimension) {
		return RosterClass{}, &RosterUnsupportedError{
			Reason: "the no-data dimensions are not the target plan's key dimensions " +
				strings.Join(plan.Identity.RosterDimensions(), ",") + ", so the expected set cannot be enumerated from the target",
		}
	}
	return RosterClass{Source: RosterTargetPlan, Plan: plan}, nil
}

// sameDimensionSet compares two dimension lists as sets of non-empty names.
func sameDimensionSet(expected, actual []string) bool {
	want := make(map[string]struct{}, len(expected))
	for _, name := range expected {
		want[name] = struct{}{}
	}
	got := make(map[string]struct{}, len(actual))
	for _, name := range actual {
		if name == "" {
			return false
		}
		got[name] = struct{}{}
	}
	if len(want) != len(got) {
		return false
	}
	for name := range want {
		if _, found := got[name]; !found {
			return false
		}
	}
	return true
}

// ClassifyRoster decides which of the five combinations an item is.
//
// The backend's target read is narrower than this package's target contract,
// and the difference is not cosmetic. It reads target[0][0] - the first group's
// first condition - takes that condition's value list, and never looks at the
// method. A target with an excluded host list, with two host conditions, or
// with two alternative groups is not a list it enumerates; treating one as
// though it were would build an expected set out of the hosts a strategy asked
// to leave out, and then alert because they have no data.
//
// So the first cut recognises exactly one host shape: one group, one condition,
// HOST, EQ. Every other host shape is refused by name rather than approximated.
func ClassifyRoster(scope *contract.TargetScopeV2, aggDimension []string) (RosterClass, error) {
	if scope == nil {
		return RosterClass{Source: RosterHistory}, nil
	}
	if !namesTheHostDimension(aggDimension) {
		// The backend's first step: no-data dimensions that do not name
		// bk_target_ip make it return nothing at all rather than fall back to
		// history, and its whole-item rule then reports the item as a whole
		// when no data arrived. The expected set is empty and the reading is
		// the whole item, not a target that resolved to nothing.
		return RosterClass{Source: RosterWhole}, nil
	}
	condition, ok := soleHostCondition(scope)
	if !ok {
		return RosterClass{}, &RosterUnsupportedError{
			Reason: "the target is not one included host list, and the backend enumerates only the " +
				"first condition of the first group",
		}
	}
	if !hostPairIsTheWholeDimensionSet(aggDimension) {
		return RosterClass{}, &RosterUnsupportedError{
			Reason: "the no-data dimensions name the host without being the host pair, so the expected " +
				"set is history filtered by the target, which is not in this build",
		}
	}
	hosts, ok := hostIdentities(condition.Keys)
	if !ok {
		return RosterClass{}, &RosterUnsupportedError{
			Reason: "the target names hosts by identifier only, and a no-data group is addressed by " +
				"address and cloud",
		}
	}
	return RosterClass{Source: RosterTargetStatic, Hosts: hosts}, nil
}

// soleHostCondition returns the one included host condition the backend would
// read, and false for every other shape: more than one group, more than one
// condition, a condition on anything but hosts, and an excluded list, which
// names what is not expected rather than what is.
func soleHostCondition(scope *contract.TargetScopeV2) (contract.TargetScopeConditionV2, bool) {
	if len(scope.Groups) != 1 || len(scope.Groups[0].Conditions) != 1 {
		return contract.TargetScopeConditionV2{}, false
	}
	condition := scope.Groups[0].Conditions[0]
	if condition.Field != contract.TargetScopeHost || condition.Method != contract.TargetScopeInclude {
		return contract.TargetScopeConditionV2{}, false
	}
	return condition, true
}

// hostIdentities reads the address-and-cloud keys out of a host condition. The
// compiler puts both forms in - a bare host identifier and "address|cloud" for
// the same value - and a no-data group is addressed by the second, so the first
// is skipped rather than parsed into a group whose address is a row of digits.
func hostIdentities(keys []string) ([]HostIdentity, bool) {
	hosts := make([]HostIdentity, 0, len(keys))
	for _, key := range keys {
		separator := strings.LastIndex(key, "|")
		if separator <= 0 || separator == len(key)-1 {
			continue
		}
		hosts = append(hosts, HostIdentity{IP: key[:separator], CloudID: key[separator+1:]})
	}
	return hosts, len(hosts) > 0
}

// RosterRequest is what the expected set is derived from. Everything in it is
// already resolved: this package reads no CMDB index and no store, so that the
// derivation can be tested against the backend's branch by branch.
type RosterRequest struct {
	AggDimension []string
	// Scope is the strategy's target, frozen in the Plan. Nil means it names
	// none.
	Scope *contract.TargetScopeV2
	// Plan is the target's second frozen form; nil for every other Plan.
	Plan *contract.TargetPlanV1
	// TargetMembers are the member keys the worker resolved Plan to in this
	// Slot. Read for TARGET_PLAN only, and only once the caller has decided
	// the resolution is complete: an incomplete or unavailable resolution
	// never reaches the roster.
	TargetMembers []string
	// KnownHosts is the set of "address|cloud" keys the CMDB index holds for
	// this business. A declared host missing from it is not expected, which is
	// the backend intersecting its target values with the business's hosts.
	KnownHosts map[string]struct{}
	Memory     map[string]GroupMemory
}

// RosterUnsupportedError says this build cannot derive an expected set for the
// combination of target shape and no-data dimensions the Plan carries.
//
// It is an error rather than an empty roster because the two are different
// facts and only one of them is a judgement. An empty roster is a real answer -
// a static target that currently matches no host has an empty expected set, and
// the backend agrees - while this is the absence of an answer. Returning empty
// for it would expect nothing where the backend expects a set, and would do it
// silently, every round, for as long as the strategy exists.
//
// Nothing about this decision changes between rounds: the target shape and the
// dimensions are both frozen in the Plan, so a Slot learns nothing a compiler
// did not already know. The Plan is therefore refused when it is built, and
// this exists to make the derivation refuse it too rather than trust that the
// compiler caught it.
type RosterUnsupportedError struct{ Reason string }

func (err *RosterUnsupportedError) Error() string {
	return "alarmd nodata: no expected set can be derived: " + err.Reason
}

// BuildRoster derives the expected set for one item.
//
// It follows the backend's branch structure rather than the summary of it. The
// backend decides in two steps, and the second one is easy to lose: it first
// asks whether the no-data dimensions contain bk_target_ip at all - and returns
// nothing when they do not, rather than falling back to history - and only then
// asks whether the dimension set is exactly the target's, which is what decides
// between expecting the target instances and filtering history by them.
//
// The first cut answers three of the five combinations those two steps produce.
// No target at all is history. A target with dimensions that do not name the
// host is the whole item, because the backend never consults the target and
// then has nothing expected. A static host target with exactly the host pair is
// the target's hosts, empty included - a target that matches no host right now
// is an answer, and the backend gives the same one.
//
// The other two are refused rather than answered emptily. A target this build
// cannot enumerate, and a dimension set that names the host without being the
// pair, both have an expected set in the backend that this cut cannot produce;
// returning empty for either would expect nothing where the backend expects a
// set, silently, every round.
func BuildRoster(request RosterRequest) (Roster, error) {
	class, err := ClassifyTarget(request.Scope, request.Plan, request.AggDimension)
	if err != nil {
		return Roster{}, err
	}
	roster := Roster{Source: class.Source, Groups: map[string]Group{}}
	switch class.Source {
	case RosterHistory:
		roster.Groups = historyGroups(request.Memory)
	case RosterTargetStatic:
		for _, host := range class.Hosts {
			if _, known := request.KnownHosts[host.IP+"|"+host.CloudID]; !known {
				continue
			}
			group := hostTargetGroup(host)
			roster.Groups[group.Key()] = group
		}
	case RosterTargetPlan:
		// The members are expected as resolved: the writer's static list is
		// the writer's to keep current, and a dynamic member that left its
		// group leaves the roster on the next complete resolution, which is
		// what closes its absence once. No CMDB intersection here; the
		// resolution already is the CMDB's answer where one was asked.
		for _, key := range request.TargetMembers {
			group, ok := targetPlanGroup(class.Plan.Identity, key)
			if !ok {
				continue
			}
			roster.Groups[group.Key()] = group
		}
	}
	version, err := rosterVersion(roster)
	if err != nil {
		return Roster{}, err
	}
	roster.Version = version
	return roster, nil
}

// rosterVersion names the expected set this round was decided against.
//
// It is derived from the set rather than passed in, because the roster knows
// what it is the moment it is built and a caller stating it is a caller that
// can state it wrong. It is not the state generation either: that already keys
// the memory record and says the Plan's content changed, so repeating it here
// would be one fact written twice with the second copy saying nothing. What a
// reader of a stored memory needs from this field is the other question - was
// this decided against the same expected set as last time - and membership is
// exactly what moves without the content moving: a host joining or leaving the
// business changes the set and nothing else does.
func rosterVersion(roster Roster) (string, error) {
	keys := make([]string, 0, len(roster.Groups))
	for key := range roster.Groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-no-data-roster-v1", struct {
		Source RosterSource `json:"source"`
		Groups []string     `json:"groups"`
	}{Source: roster.Source, Groups: keys})
	if err != nil {
		return "", fmt.Errorf("alarmd nodata: derive roster version: %w", err)
	}
	return digest, nil
}

// namesTheHostDimension is the backend's first step, which decides whether the
// target is consulted at all: `if "bk_target_ip" not in no_data_dimensions`.
func namesTheHostDimension(aggDimension []string) bool {
	for _, dimension := range aggDimension {
		if dimension == HostIPDimension {
			return true
		}
	}
	return false
}

// hostPairIsTheWholeDimensionSet is the backend's
// set(no_data_dimensions) == set(target_instances[0].keys()) with the keys it
// puts there: the host pair and nothing else.
func hostPairIsTheWholeDimensionSet(aggDimension []string) bool {
	if len(aggDimension) != 2 {
		return false
	}
	seen := map[string]bool{}
	for _, dimension := range aggDimension {
		seen[dimension] = true
	}
	return seen[HostIPDimension] && seen[HostCloudDimension]
}

// targetPlanGroup turns one resolved member key into the group the data
// reports it under, by the plan's identity dimensions.
func targetPlanGroup(identity contract.TargetPlanIdentityV1, key string) (Group, bool) {
	values, ok := identity.Group(key)
	if !ok {
		return Group{}, false
	}
	dimensions := make([]Dimension, 0, len(values))
	for name, value := range values {
		dimensions = append(dimensions, Dimension{Name: name, Value: value})
	}
	sort.Slice(dimensions, func(left, right int) bool { return dimensions[left].Name < dimensions[right].Name })
	return Group{dimensions: dimensions}, true
}

func hostTargetGroup(host HostIdentity) Group {
	dimensions := []Dimension{
		{Name: HostCloudDimension, Value: host.CloudID},
		{Name: HostIPDimension, Value: host.IP},
	}
	sort.Slice(dimensions, func(left, right int) bool { return dimensions[left].Name < dimensions[right].Name })
	return Group{dimensions: dimensions}
}

// historyGroups is the expected set for an item with no target: every group the
// memory has seen.
//
// The whole-item key is excluded. It is in the memory because the absence
// evaluation records when the item as a whole went absent, and it is not a
// series - carrying it into a roster would make the item expect itself, so it
// would be judged as a group whose absence is the very thing that put it there.
func historyGroups(memory map[string]GroupMemory) map[string]Group {
	whole := WholeItemGroup().Key()
	groups := make(map[string]Group, len(memory))
	for key, entry := range memory {
		if key == whole || entry.LastSeen == 0 {
			continue
		}
		group, ok := ParseGroupKey(key)
		if !ok {
			continue
		}
		groups[key] = group
	}
	return groups
}
