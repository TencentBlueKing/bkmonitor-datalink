// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import (
	"encoding/json"
	"testing"
)

func hostStatusFilter(t *testing.T, states ...string) *HostStatusFilter {
	t.Helper()
	filter, installed := NewHostStatusFilter(states)
	if !installed {
		t.Fatalf("NewHostStatusFilter(%v) declined to install", states)
	}
	return filter
}

func factsFor(dimensions map[string]json.RawMessage, apply func(*Facts)) *Facts {
	facts := &Facts{}
	IdentityFuller{}.Fill(dimensions, facts)
	if apply != nil {
		apply(facts)
	}
	return facts
}

func raw(value string) json.RawMessage { return json.RawMessage(value) }

// The production case: a host the platform marks as not alerting must not
// produce alerts. Matching is substring, because the platform's own check is
// `state in host.bk_state`.
func TestAHostInADisabledStateIsRejected(t *testing.T) {
	filter := hostStatusFilter(t, "运营中[无告警]", "备用机")
	facts := factsFor(map[string]json.RawMessage{
		"bk_target_ip":       raw(`"192.0.2.10"`),
		"bk_target_cloud_id": raw(`0`),
	}, func(f *Facts) {
		f.HostResolved = true
		f.HostState = "运营中[无告警]"
	})
	decision := filter.Admit(PlanContext{}, facts)
	if decision.Admit || decision.Reason != "monitoring_disabled" {
		t.Fatalf("decision = %+v, want a rejection naming the disabled state", decision)
	}
}

func TestAHostInAMonitoredStateIsAdmitted(t *testing.T) {
	filter := hostStatusFilter(t, "运营中[无告警]")
	facts := factsFor(map[string]json.RawMessage{
		"bk_target_ip":       raw(`"192.0.2.10"`),
		"bk_target_cloud_id": raw(`0`),
	}, func(f *Facts) {
		f.HostResolved = true
		f.HostState = "运营中[需告警]"
	})
	if decision := filter.Admit(PlanContext{}, facts); !decision.Admit {
		t.Fatalf("decision = %+v, want the record admitted", decision)
	}
}

// A record that names no host at all is not host data - container and custom
// report series reach the same filter - and Python leaves it alone.
func TestASeriesThatNamesNoHostIsAdmitted(t *testing.T) {
	filter := hostStatusFilter(t, "备用机")
	facts := factsFor(map[string]json.RawMessage{
		"bcs_cluster_id": raw(`"BCS-K8S-00000"`),
		"namespace":      raw(`"default"`),
	}, nil)
	if decision := filter.Admit(PlanContext{}, facts); !decision.Admit {
		t.Fatalf("decision = %+v, want a non-host series admitted", decision)
	}
}

// The opposite branch, and the reason the two cannot be collapsed: a record
// that names a host and gives nothing usable is invalid, and Python drops it.
func TestASeriesThatNamesAHostWithNoUsableIdentityIsRejected(t *testing.T) {
	filter := hostStatusFilter(t, "备用机")
	facts := factsFor(map[string]json.RawMessage{
		"bk_target_ip":       raw(`""`),
		"bk_target_cloud_id": raw(`0`),
	}, nil)
	decision := filter.Admit(PlanContext{}, facts)
	if decision.Admit || decision.Reason != "host_identity_invalid" {
		t.Fatalf("decision = %+v, want the invalid host record rejected", decision)
	}
}

// Python looks a host up by address only when the cloud came with it. Without
// one it never consults CMDB, so the record survives even though the same
// address in cloud 0 is a disabled host.
func TestAnAddressWithoutItsCloudIsNotLookedUp(t *testing.T) {
	filter := hostStatusFilter(t, "备用机")
	facts := factsFor(map[string]json.RawMessage{
		"bk_target_ip": raw(`"192.0.2.10"`),
	}, func(f *Facts) {
		f.HostResolved = true
		f.HostState = "备用机"
	})
	if decision := filter.Admit(PlanContext{}, facts); !decision.Admit {
		t.Fatalf("decision = %+v, want the record admitted without a cloud", decision)
	}
}

// A host id alone is enough to look the host up; no cloud is involved.
func TestAHostIDAloneIsLookedUp(t *testing.T) {
	filter := hostStatusFilter(t, "备用机")
	facts := factsFor(map[string]json.RawMessage{
		"bk_host_id": raw(`4210`),
	}, func(f *Facts) {
		f.HostResolved = true
		f.HostState = "备用机"
	})
	decision := filter.Admit(PlanContext{}, facts)
	if decision.Admit || decision.Reason != "monitoring_disabled" {
		t.Fatalf("decision = %+v, want the disabled host rejected by id alone", decision)
	}
}

func TestAHostCMDBDoesNotKnowIsRejected(t *testing.T) {
	filter := hostStatusFilter(t, "备用机")
	facts := factsFor(map[string]json.RawMessage{
		"bk_target_ip":       raw(`"192.0.2.10"`),
		"bk_target_cloud_id": raw(`0`),
	}, nil)
	decision := filter.Admit(PlanContext{}, facts)
	if decision.Admit || decision.Reason != "host_unknown" {
		t.Fatalf("decision = %+v, want an unknown host rejected", decision)
	}
}

// The one place this filter must not follow Python: Python always has the
// cache, alarmd may not. Dropping every unresolved host while the index is
// missing would turn a cache outage into fleet-wide silence.
func TestAnUnreadableIndexDoesNotSilenceEveryHost(t *testing.T) {
	filter := hostStatusFilter(t, "备用机")
	facts := factsFor(map[string]json.RawMessage{
		"bk_target_ip":       raw(`"192.0.2.10"`),
		"bk_target_cloud_id": raw(`0`),
	}, func(f *Facts) { f.HostFactsUnavailable = true })
	decision := filter.Admit(PlanContext{}, facts)
	if !decision.Admit || decision.Reason != "host_facts_unavailable" {
		t.Fatalf("decision = %+v, want the record admitted and the gap named", decision)
	}
}

// No configured states means the platform disables no host. Installing a
// filter that can never reject would spend a decision per series to say yes.
func TestNoConfiguredStatesInstallsNoFilter(t *testing.T) {
	for _, states := range [][]string{nil, {}, {"", "  "}} {
		if _, installed := NewHostStatusFilter(states); installed {
			t.Fatalf("NewHostStatusFilter(%q) installed a filter that cannot reject", states)
		}
	}
}

// The chain names both filters, so a deployment can see which decisions are
// actually installed rather than inferring it from behaviour.
func TestTheChainNamesTheHostStatusFilter(t *testing.T) {
	filter := hostStatusFilter(t, "备用机")
	chain := NewChain([]Fuller{IdentityFuller{}}, []Filter{TargetScopeFilter{}, filter})
	names := chain.FilterNames()
	if len(names) != 2 || names[0] != "target_scope" || names[1] != "host_status" {
		t.Fatalf("filter names = %v", names)
	}
}

// The target scope accepts ip / bk_cloud_id as alternative spellings, and the
// host status filter must not inherit that. Python branches on bk_target_ip
// and bk_host_id only, so a record spelled the other way is not host data to
// it - and looking such a record up would drop a series Python keeps whenever
// the address happens to belong to a disabled host. That is a missed alert.
func TestTheAlternativeAddressSpellingIsNotHostNamingForThisFilter(t *testing.T) {
	filter := hostStatusFilter(t, "备用机")
	facts := factsFor(map[string]json.RawMessage{
		"ip":          raw(`"192.0.2.10"`),
		"bk_cloud_id": raw(`0`),
	}, func(f *Facts) {
		f.HostResolved = true
		f.HostState = "备用机"
	})
	if decision := filter.Admit(PlanContext{}, facts); !decision.Admit {
		t.Fatalf("decision = %+v, want the record admitted: Python does not treat it as host data", decision)
	}
	// The target scope still resolves it, so the two readings really are separate.
	if len(facts.HostKeys) == 0 {
		t.Fatal("the target scope lost the alternative spelling it relies on")
	}
}

// An empty bk_target_ip is invalid host data even when the alternative
// spelling carries an address, because Python checks bk_target_ip itself.
func TestAnEmptyTargetAddressIsInvalidEvenWithTheAlternativeSpelling(t *testing.T) {
	filter := hostStatusFilter(t, "备用机")
	facts := factsFor(map[string]json.RawMessage{
		"bk_target_ip": raw(`""`),
		"ip":           raw(`"192.0.2.10"`),
	}, nil)
	decision := filter.Admit(PlanContext{}, facts)
	if decision.Admit || decision.Reason != "host_identity_invalid" {
		t.Fatalf("decision = %+v, want the record rejected as invalid host data", decision)
	}
}

// Production case that the shadow reconcile caught: a collector config whose
// cloud id placeholder was never rendered ships the literal template text as
// bk_target_cloud_id. Python's host status filter coerces it with safe_int, so
// it looks the host up in the direct area and finds it; taking the text at face
// value builds a key no host can have, reads as "CMDB does not know this host",
// and drops every series of that strategy - 8484 went from 134 matched points
// to zero. The coercion belongs to the lookup that filter performs, which is
// the one Python coerces.
func TestAnUnrenderedCloudPlaceholderIsLookedUpInTheDirectArea(t *testing.T) {
	facts := factsFor(map[string]json.RawMessage{
		"bk_target_ip":       raw(`"192.0.2.10"`),
		"bk_target_cloud_id": raw(`"{{ cmdb_instance.host.bk_cloud_id[0].id }}"`),
	}, nil)
	key, looked := facts.HostNaming.LookupKey()
	if !looked || key != "192.0.2.10|0" {
		t.Fatalf("lookup key = %q/%v, want the address in the direct area", key, looked)
	}
}

// The same coercion Python's safe_int does, including the float path.
func TestCloudIdentityIsCoercedTheWayThePlatformDoes(t *testing.T) {
	for _, test := range []struct{ cloud, want string }{
		{`3`, "192.0.2.10|3"},
		{`"3"`, "192.0.2.10|3"},
		{`3.0`, "192.0.2.10|3"},
		{`"3.9"`, "192.0.2.10|3"},
		{`"not a number"`, "192.0.2.10|0"},
		{`""`, "192.0.2.10|0"},
	} {
		facts := factsFor(map[string]json.RawMessage{
			"bk_target_ip":       raw(`"192.0.2.10"`),
			"bk_target_cloud_id": raw(test.cloud),
		}, nil)
		if key, _ := facts.HostNaming.LookupKey(); key != test.want {
			t.Fatalf("cloud %s produced lookup key %q, want %q", test.cloud, key, test.want)
		}
	}
}

// And the other side of that split: the key a target is matched on, and the key
// the topology lookup uses, keep the dimension as it stands. Python coerces in
// exactly one of its three call sites - filters.py - while fullers.py's address
// branch and target.py's is_match both build the key raw. Coercing here would
// resolve topology Python leaves unresolved and match targets Python does not
// match; the error is extra alerts, which is why it survived review.
func TestTheTargetMatchKeyKeepsTheCloudDimensionAsItStands(t *testing.T) {
	facts := factsFor(map[string]json.RawMessage{
		"bk_target_ip":       raw(`"192.0.2.10"`),
		"bk_target_cloud_id": raw(`"{{ cmdb_instance.host.bk_cloud_id[0].id }}"`),
	}, nil)
	want := "192.0.2.10|{{ cmdb_instance.host.bk_cloud_id[0].id }}"
	found := false
	for _, key := range facts.HostKeys {
		if key == want {
			found = true
		}
		if key == "192.0.2.10|0" {
			t.Fatalf("host keys = %v, want no coerced key for target matching", facts.HostKeys)
		}
	}
	if !found {
		t.Fatalf("host keys = %v, want %q", facts.HostKeys, want)
	}
}

// An absent cloud is still the direct area on both sides: Python's enrichment
// falls back to bk_cloud_id and then to "0" before it builds the key, so this
// is not the coercion above but the default underneath it.
func TestAnAbsentCloudIsTheDirectAreaOnBothKeys(t *testing.T) {
	facts := factsFor(map[string]json.RawMessage{
		"bk_target_ip":       raw(`"192.0.2.10"`),
		"bk_target_cloud_id": raw(`""`),
	}, nil)
	found := false
	for _, key := range facts.HostKeys {
		if key == "192.0.2.10|0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("host keys = %v, want the direct area", facts.HostKeys)
	}
}

// Python branches on bk_host_id's value rather than on the dimension being
// present, so an empty one names no id and the record follows the address
// branch - where, with no cloud alongside the address, Python looks nothing up
// and keeps the record. Reading presence instead would send it down the id
// branch and drop it as an unknown host. That is a missed alert.
func TestAnEmptyHostIDLeavesTheRecordOnTheAddressBranch(t *testing.T) {
	filter := hostStatusFilter(t, "备用机")
	facts := factsFor(map[string]json.RawMessage{
		"bk_host_id":   raw(`""`),
		"bk_target_ip": raw(`"192.0.2.10"`),
	}, nil)
	if decision := filter.Admit(PlanContext{}, facts); !decision.Admit {
		t.Fatalf("decision = %+v, want the record admitted: Python looks nothing up", decision)
	}
}

// The lookup the filter branches on is the lookup the enrichment performs, and
// it is built from bk_target_ip and bk_target_cloud_id only. The ip /
// bk_cloud_id spellings build target-scope keys, which Python never looks a
// host up by; a lookup key taken from those would decide this filter on a host
// Python never consulted.
func TestTheLookupKeyIsThePlatformsOwnSpellingsOnly(t *testing.T) {
	facts := factsFor(map[string]json.RawMessage{
		"bk_target_ip":       raw(`"192.0.2.10"`),
		"bk_target_cloud_id": raw(`""`),
		"bk_cloud_id":        raw(`5`),
	}, nil)
	key, looked := facts.HostNaming.LookupKey()
	if !looked || key != "192.0.2.10|0" {
		t.Fatalf("lookup key = %q/%v, want the target cloud coerced to the direct area", key, looked)
	}
	// The alternative spelling still builds a target-scope key, so the two
	// readings really are separate.
	found := false
	for _, candidate := range facts.HostKeys {
		if candidate == "192.0.2.10|5" {
			found = true
		}
	}
	if !found {
		t.Fatalf("host keys = %v, want the alternative spelling kept for target matching", facts.HostKeys)
	}
}
