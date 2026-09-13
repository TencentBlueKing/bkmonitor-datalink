// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The audit's two directions never meet: a kept or freshly read entry that
// differs from the truth is missed, the harmful direction; an entry read
// although the one already held equalled the truth is over_named, the
// wasted read; everything else agreed.
func TestClassifyCatalogIndexAuditSeparatesTheTwoDirections(t *testing.T) {
	plan := func(id string) execution.PlanIdentity {
		return execution.PlanIdentity{TenantID: "t", BusinessID: "b", StrategyID: id}
	}
	truth := catalogIndexEntry{Digest: "d1", Plans: []execution.PlanIdentity{plan("1"), plan("2")}}
	for _, test := range []struct {
		name     string
		entry    catalogIndexEntry
		reread   bool
		previous *catalogIndexEntry
		want     string
	}{
		{name: "kept entry equals the truth", entry: truth, want: catalogIndexAuditAgreed},
		{name: "read entry equals the truth without a previous", entry: truth, reread: true, want: catalogIndexAuditAgreed},
		{name: "read entry equals the truth after a real change", entry: truth, reread: true,
			previous: &catalogIndexEntry{Digest: "d0", Plans: truth.Plans}, want: catalogIndexAuditAgreed},
		{name: "read although the held entry already equalled the truth", entry: truth, reread: true,
			previous: &catalogIndexEntry{Digest: "d1", Plans: truth.Plans}, want: catalogIndexAuditOverNamed},
		{name: "kept entry with another digest", entry: catalogIndexEntry{Digest: "d0", Plans: truth.Plans}, want: catalogIndexAuditMissed},
		{name: "kept entry with other plans", entry: catalogIndexEntry{Digest: "d1", Plans: []execution.PlanIdentity{plan("1")}}, want: catalogIndexAuditMissed},
		{name: "read entry with other plans", entry: catalogIndexEntry{Digest: "d1", Plans: []execution.PlanIdentity{plan("2"), plan("1")}}, reread: true,
			previous: &catalogIndexEntry{Digest: "d0"}, want: catalogIndexAuditMissed},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyCatalogIndexAudit(test.entry, test.reread, test.previous, truth.Digest, truth.Plans); got != test.want {
				t.Fatalf("classification = %s, want %s", got, test.want)
			}
		})
	}
}

func TestIndexFromCatalogRejectsEmptyAndDuplicateQueryGroups(t *testing.T) {
	if _, err := indexFromCatalog(Catalog{QueryGroups: []QueryGroup{{Identity: ""}}}); err == nil {
		t.Fatal("an empty Query Group was indexed")
	}
	if _, err := indexFromCatalog(Catalog{QueryGroups: []QueryGroup{{Identity: "a"}, {Identity: "a"}}}); err == nil {
		t.Fatal("a duplicate Query Group was indexed")
	}
}
