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
