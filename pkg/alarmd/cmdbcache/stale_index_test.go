// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

// decision-013 section 2 #12 and section 5.1 item 4: a whole CMDB index that is
// unavailable or stale is admitted and named host_facts_unavailable, and a
// rejection is not closed on facts past the staleness bound (decision-024).

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
)

func TestAnIndexPastItsBoundIsAdmittedByNameNotDecidedOn(t *testing.T) {
	builtAt := time.Unix(1700000000, 0).UTC()
	builder := newIndexBuilder(builtAt)
	builder.addFields([]string{"192.0.2.148|0", monitoredByIDHost, "700002", monitoredByIDHost})
	now := builtAt.Add(10 * time.Minute)
	store := &Store{index: builder.index, now: func() time.Time { return now }, maxAge: 10 * time.Minute}
	chain := instanceChain(t, store, "备用机")
	module85 := scopeOf(admission.TargetScopeTopoNode, admission.TargetScopeInclude, "module|85")
	record := dims("bk_host_id", `"800001"`)

	// At the bound: the index decides. 800001 is unknown to it, out of scope.
	facts := chain.Enrich(record)
	if admit, _, reason := chain.Admit(module85, &facts); admit {
		t.Fatalf("within the bound: admitted (%s), want the index to decide", reason)
	}
	// One second past it: admitted by name, and no verdict to close on.
	now = builtAt.Add(10*time.Minute + time.Second)
	facts = chain.Enrich(record)
	admit, filter, reason := chain.Admit(module85, &facts)
	if !admit || reason != admission.FactsUnavailableHostIndex {
		t.Fatalf("past the bound: admit=%v by %s/%s, want admitted under %s", admit, filter, reason, admission.FactsUnavailableHostIndex)
	}
	if admission.DefinitelyOutside(module85, &facts, admission.TargetScopeFilter{}.Name(), "out_of_scope") {
		t.Fatal("past the bound: a rejection would be a verdict on facts past the staleness bound")
	}
}
