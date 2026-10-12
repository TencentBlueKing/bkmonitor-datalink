// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The source facts digest hashes the strategy's identity, and it names the
// observation a round confirms. An ordinary strategy's digest is the one
// the identity had before it could say global: a field every strategy
// serialized would move every observation on the upgrade and send every
// strategy through confirmation again for no change. A global strategy's
// digest differs, as its facts do.
func TestAnOrdinaryStrategysSourceDigestIsWhatItWas(t *testing.T) {
	strategy := SourceStrategy{SourceID: "1001", Document: json.RawMessage(`{"id":1001,"bk_biz_id":2}`),
		Identity: SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}
	got, err := sourceFactsDigest(strategy)
	if err != nil {
		t.Fatal(err)
	}
	type identityBefore struct {
		TenantID   string
		BusinessID string
		SpaceScope string
	}
	before, err := contract.DeriveCanonicalDigestV2("alarmd-source-object-v2", struct {
		Document    []byte             `json:"document"`
		Identity    identityBefore     `json:"identity"`
		Disposition *ObjectDisposition `json:"disposition,omitempty"`
	}{Document: strategy.Document, Identity: identityBefore{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}})
	if err != nil {
		t.Fatal(err)
	}
	if got != before {
		t.Fatalf("ordinary source digest = %s, want the pre-change %s", got, before)
	}
	strategy.Identity.GlobalBusiness = true
	global, err := sourceFactsDigest(strategy)
	if err != nil {
		t.Fatal(err)
	}
	if global == got {
		t.Fatal("a global strategy's source digest is the ordinary one's")
	}
}

// A PromQL query is refused for a global business Plan by its shape, not
// only by the source semantics the compiler happens to write beside it: the
// clause checks read the clause list, which a PromQL query does not have,
// so facts without the semantics would otherwise pass every check.
func TestAGlobalBusinessPromQLQueryIsRefusedByItsShape(t *testing.T) {
	facts := execution.QueryPlanFacts{PromQL: &execution.PromQLQuery{Expression: "sum(up)"}}
	refusal, word := globalBusinessRefusal("1001", SourceIdentity{GlobalBusiness: true}, nil, facts)
	want := "reason=" + GlobalBusinessQueryKind + " source=" + GlobalQuerySourcePromQL
	if refusal == nil || refusal.Detail != want || word != GlobalBusinessQueryKind {
		t.Fatalf("refusal = %+v word %q, want detail %q and word %s", refusal, word, want, GlobalBusinessQueryKind)
	}
	if refusal, word := globalBusinessRefusal("1001", SourceIdentity{}, nil, facts); refusal != nil || word != "" {
		t.Fatalf("an ordinary PromQL strategy was refused: %+v %q", refusal, word)
	}
}
