// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// queryGroupIdentityFieldOfFact names, for each field of QueryPlanFacts, the
// field of the identity that reads it. QueryRevision maps to nothing: it is
// the digest of every other fact, so the identity cannot read it without
// making every Plan its own group.
//
// The table is written out rather than derived, because the point is that
// adding a fact forces somebody to decide which side of it the new fact
// falls on. Deciding wrong is a compile-and-test failure here; not deciding
// at all used to be a deployment that stopped compiling its Catalog.
var queryGroupIdentityFieldOfFact = map[string]string{
	"QueryRevision":     "",
	"Provider":          "Provider",
	"ProviderRouteRef":  "Route",
	"TenantID":          "Tenant",
	"BusinessID":        "Business",
	"SpaceScope":        "Space",
	"QueryList":         "QueryList",
	"MetricMerge":       "MetricMerge",
	"StepMillis":        "StepMillis",
	"AlignmentMillis":   "Alignment",
	"DownSampleRange":   "DownSample",
	"Timezone":          "Timezone",
	"NotTimeAlign":      "NotTimeAlign",
	"Normalization":     "Normalization",
	"QueryDelaySeconds": "QueryDelay",
	"SourceSemantics":   "SourceSemantics",
	"PromQL":            "PromQL",
	"TSDBMap":           "TSDBMap",
}

// The identity has to read every fact the revision reads. A fact that only
// the revision reads puts two different queries in one Query Group, and the
// Catalog refuses such a group by failing the whole build -- for every
// strategy, not only the two that disagree.
func TestQueryGroupIdentityReadsEveryQueryPlanFact(t *testing.T) {
	factType := reflect.TypeOf(execution.QueryPlanFacts{})
	identityType := reflect.TypeOf(queryGroupIdentityFacts{})

	claimed := map[string]string{}
	for index := 0; index < factType.NumField(); index++ {
		name := factType.Field(index).Name
		target, listed := queryGroupIdentityFieldOfFact[name]
		if !listed {
			t.Fatalf("QueryPlanFacts.%s is not in queryGroupIdentityFieldOfFact: decide whether the "+
				"Query Group identity reads it. A fact the revision reads and the identity does not "+
				"stops every Catalog build as soon as two strategies disagree on it.", name)
		}
		if target == "" {
			continue
		}
		if _, ok := identityType.FieldByName(target); !ok {
			t.Fatalf("queryGroupIdentityFacts has no field %s for QueryPlanFacts.%s", target, name)
		}
		if previous, duplicate := claimed[target]; duplicate {
			t.Fatalf("queryGroupIdentityFacts.%s is claimed by both %s and %s", target, previous, name)
		}
		claimed[target] = name
	}

	for name := range queryGroupIdentityFieldOfFact {
		if _, ok := factType.FieldByName(name); !ok {
			t.Fatalf("queryGroupIdentityFieldOfFact lists %s, which QueryPlanFacts no longer has", name)
		}
	}
	for index := 0; index < identityType.NumField(); index++ {
		name := identityType.Field(index).Name
		if _, ok := claimed[name]; !ok {
			t.Fatalf("queryGroupIdentityFacts.%s reads no fact: the identity would separate Query "+
				"Groups by something the revision does not know about", name)
		}
	}
}

// A Plan that carries none of the facts added after the original identity
// keeps the identity it already had. Identity names a Query Group in
// Ownership, in the schedule and in Runtime State, so a Plan whose query did
// not change must not be renamed by a release: it would retire every group
// and activate the same queries under new names.
//
// The digest below was taken from the deployed derivation before the fields
// were added. It is a golden value on purpose: a change to it is a change to
// every Query Group's name, which is a migration and not an edit.
func TestQueryGroupIdentityIsUnchangedForPlansWithoutTheAddedFacts(t *testing.T) {
	facts := execution.QueryPlanFacts{
		Provider: execution.ProviderUQ, ProviderRouteRef: "uq-primary-v1",
		TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2",
		MetricMerge: "a", StepMillis: 60_000, AlignmentMillis: 60_000,
		DownSampleRange: execution.DownSampleNone, Timezone: "UTC",
	}
	identity, err := deriveQueryGroupIdentity(facts)
	if err != nil {
		t.Fatal(err)
	}
	const before = "4b904bac38ce629390b5c86d7668aed0d5314c310febccc4ef157e262dfd1c87"
	if string(identity) != before {
		t.Fatalf("identity for a Plan without the added facts = %s, want the pre-change %s: "+
			"every Query Group would be renamed by this release", identity, before)
	}
}

// Each fact added to the identity separates Query Groups on its own. Without
// this the fact would be in the revision only, which is the failure that
// started this: one group, two revisions, and the Catalog build stops.
func TestQueryGroupIdentitySeparatesOnEachAddedFact(t *testing.T) {
	base := execution.QueryPlanFacts{
		Provider: execution.ProviderUQ, ProviderRouteRef: "uq-primary-v1",
		TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2",
		MetricMerge: "a", StepMillis: 60_000, AlignmentMillis: 60_000,
		DownSampleRange: execution.DownSampleNone, Timezone: "UTC",
	}
	baseIdentity, err := deriveQueryGroupIdentity(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name  string
		apply func(execution.QueryPlanFacts) execution.QueryPlanFacts
	}{
		{"query delay", func(facts execution.QueryPlanFacts) execution.QueryPlanFacts {
			facts.QueryDelaySeconds = 60
			return facts
		}},
		{"source semantics", func(facts execution.QueryPlanFacts) execution.QueryPlanFacts {
			facts.SourceSemantics = []string{"bk_monitor/log"}
			return facts
		}},
		{"promql", func(facts execution.QueryPlanFacts) execution.QueryPlanFacts {
			facts.PromQL = &execution.PromQLQuery{Expression: "up"}
			return facts
		}},
		{"tsdb map", func(facts execution.QueryPlanFacts) execution.QueryPlanFacts {
			facts.TSDBMap = map[string][]execution.QueryStorage{"a": {{StorageID: "7"}}}
			return facts
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			identity, err := deriveQueryGroupIdentity(testCase.apply(base))
			if err != nil {
				t.Fatal(err)
			}
			if identity == baseIdentity {
				t.Fatalf("%s does not separate the Query Group: both are %s, and the two Plans "+
					"would be one group with two revisions", testCase.name, identity)
			}
		})
	}
}
