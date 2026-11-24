// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"encoding/json"
	"reflect"
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

// A global business Plan's PromQL is admitted when every vector selector
// names a data label or a table in its metric name, the only thing the
// provider routes a PromQL selector by once the space is skipped; one bare
// name anywhere refuses the whole expression and is named in the detail.
// The expressions are the shapes strategies write: the global APM latency
// strategy's histogram_quantile, functions, aggregations with their
// grouping first or last, binary operators with vector matching, subqueries,
// offsets, strings and comments that mention metric-like words.
func TestAGlobalBusinessPromQLQueryIsAdmittedOnlyWhenEverySelectorRoutes(t *testing.T) {
	admitted := map[string]string{
		"the global APM latency strategy": "(histogram_quantile(0.95,sum by (le,app_name,bk_instance_id,service_name,span_name) " +
			"(increase(bkmonitor:apm:bk_apm_duration_bucket[60s])))) / 1000000",
		"a table":                            "sum(rate(bkmonitor:2_bkmonitor_time_series_524307:__default__:alarmd_query_failure_total[5m]))",
		"a data label without the source":    "rate(k8s:container_cpu_usage_seconds_total[2m])",
		"a custom data label":                "custom:app_metrics:requests_total > 0",
		"two selectors with vector matching": "k8s:container_memory_working_set_bytes / on (pod) group_left (node) k8s:kube_pod_info",
		"a name matcher":                     `{__name__="bkmonitor:k8s:up", pod="a"}`,
		"a name regexp":                      `count({__name__=~"bkmonitor:k8s:kube_pod_.*"})`,
		"grouping written first":             "sum without (instance) (k8s:node_load1)",
		"set operators":                      "k8s:up and k8s:ready unless k8s:cordoned",
		"a subquery and an offset":           "max_over_time(k8s:node_load1[10m:1m] offset 5m)",
		"strings that look like metrics": `label_replace(k8s:up, "dst", "bkmonitor:up", "src", "(.*)") ` +
			`+ on () group_left count_values("up", k8s:ready)`,
		"topk and scalars":              "topk(5, k8s:node_load1) * 2 + 1e3 - .5",
		"a comment":                     "k8s:up # up is the bare name this comment mentions\n",
		"single quotes":                 `{__name__='bkmonitor:k8s:up', job='a\'b'}`,
		"a pattern only for the metric": `{__name__=~"bkmonitor:k8s:.*"}`,
		"the bool modifier":             "k8s:up == bool 1",
		"an aggregation tag":            "sum(k8s:up) by (pod)",
	}
	for name, expression := range admitted {
		t.Run(name, func(t *testing.T) {
			facts := execution.QueryPlanFacts{PromQL: &execution.PromQLQuery{Expression: expression}}
			if refusal, word := globalBusinessRefusal("1001", SourceIdentity{GlobalBusiness: true}, nil, facts, nil); refusal != nil || word != "" {
				t.Fatalf("%q refused: %+v %q", expression, refusal, word)
			}
		})
	}
	refused := map[string]struct{ expression, reason, metric string }{
		"a bare name":                   {"sum(up)", GlobalBusinessQueryTable, "up"},
		"one bare among labelled":       {"k8s:container_memory_working_set_bytes / on (pod) group_left up", GlobalBusinessQueryTable, "up"},
		"a bare name with its source":   {"bkmonitor:up", GlobalBusinessQueryTable, "bkmonitor:up"},
		"a name with too many parts":    {"rate(a:b:c:d:e[1m])", GlobalBusinessQueryTable, "a:b:c:d:e"},
		"an empty metric part":          {"k8s:", GlobalBusinessQueryTable, "k8s:"},
		"an empty data label":           {"bkmonitor::up", GlobalBusinessQueryTable, "bkmonitor::up"},
		"a bare name matcher":           {`{__name__="up"}`, GlobalBusinessQueryTable, "up"},
		"a negative name matcher alone": {`{__name__!="bkmonitor:k8s:up"}`, GlobalBusinessQueryTable, ""},
		"a selector with no name":       {`sum({pod="a"})`, GlobalBusinessQueryTable, ""},
		"no selector at all":            {"1 + 1", GlobalBusinessQueryTable, ""},
		"a log source":                  {"count(bklog:index_set:m)", GlobalBusinessQueryKind, "bklog:index_set:m"},
		"a computing platform table":    {"bkdata:db:table:m", GlobalBusinessQueryKind, "bkdata:db:table:m"},
		"an unterminated matcher":       {`k8s:up{pod="a`, GlobalBusinessQueryKind, "unreadable"},
		"an unterminated range":         {`rate(k8s:up[5m)`, GlobalBusinessQueryKind, "unreadable"},
		// The provider looks the database and table up as written; only the
		// metric part is a pattern.
		"a pattern for the data label": {`{__name__=~"bkmonitor:.*:cpu"}`, GlobalBusinessQueryTable, "bkmonitor:.*:cpu"},
		"an alternation for the label": {`{__name__=~"bkmonitor:(a|b):cpu"}`, GlobalBusinessQueryTable, "bkmonitor:(a|b):cpu"},
		"a pattern for the database":   {`{__name__=~"bkmonitor:sys.*:cpu_summary:usage"}`, GlobalBusinessQueryTable, "bkmonitor:sys.*:cpu_summary:usage"},
		"a pattern for the table":      {`{__name__=~"bkmonitor:system:cpu_.+:usage"}`, GlobalBusinessQueryTable, "bkmonitor:system:cpu_.+:usage"},
	}
	for name, test := range refused {
		t.Run(name, func(t *testing.T) {
			facts := execution.QueryPlanFacts{PromQL: &execution.PromQLQuery{Expression: test.expression}}
			refusal, word := globalBusinessRefusal("1001", SourceIdentity{GlobalBusiness: true}, nil, facts, nil)
			want := "reason=" + test.reason + " source=" + GlobalQuerySourcePromQL + " metric=" + test.metric
			if refusal == nil || refusal.Detail != want || word != test.reason || refusal.Reason != ReasonGlobalStrategyUnsupported {
				t.Fatalf("%q: refusal = %+v word %q, want detail %q and word %s", test.expression, refusal, word, want, test.reason)
			}
			// An ordinary strategy's PromQL is scoped by its space and is
			// none of this admission's business.
			if refusal, word := globalBusinessRefusal("1001", SourceIdentity{}, nil, facts, nil); refusal != nil || word != "" {
				t.Fatalf("an ordinary PromQL strategy was refused: %+v %q", refusal, word)
			}
		})
	}
}

// A global business PromQL Plan's records carry the labels its outermost
// aggregation groups by; that list is what its business is read from.
func TestAGlobalPromQLsOutputGroupingIsItsOutermostByList(t *testing.T) {
	for expression, want := range map[string][]string{
		"sum by (bk_biz_id) (rate(k8s:m[1m]))":                                                         {"bk_biz_id"},
		"sum(rate(k8s:m[1m])) by (namespace, bcs_cluster_id)":                                          {"bcs_cluster_id", "namespace"},
		"max by (bcs_cluster_id) (sum by (bcs_cluster_id, pod) (k8s:m))":                               {"bcs_cluster_id"},
		"sum by (a, b) (k8s:m) / on (a) group_left sum by (a, c) (k8s:n)":                              {"a"},
		"(histogram_quantile(0.95, sum by (le, app_name) (increase(bkmonitor:apm:d[60s])))) / 1000000": {"app_name", "le"},
		"sum by (a) (sum by (b) (k8s:m))":                                                              {"a"},
		"sum without (pod) (k8s:m)":                                                                    nil,
		"rate(k8s:m[1m])":                                                                              nil,
		`count_values("v", k8s:m{a="sum by (x)"})`:                                                     nil,
		"sum by (bk_biz_id, bk_biz_id) (k8s:m) # by (x)":                                               {"bk_biz_id"},
		`k8s:m{pod="a`: nil,
	} {
		if got := promQLOutputGrouping(expression); !reflect.DeepEqual(got, want) {
			t.Errorf("promQLOutputGrouping(%q) = %v, want %v", expression, got, want)
		}
	}
}

// The condition keys a condition_ignored detail names are bounded, the rest
// counted, and a config whose agg_condition cannot be read is named
// unreadable rather than passed as one without conditions.
func TestTheConditionKeysOfAGlobalPromQLAreBoundedAndNeverGuessed(t *testing.T) {
	for name, test := range map[string]struct {
		configs   []json.RawMessage
		keys      string
		fieldPath string
	}{
		"ten keys": {
			configs: []json.RawMessage{json.RawMessage(`{"agg_condition":[{"key":"k0"},{"key":"k1"},{"key":"k2"},{"key":"k3"},` +
				`{"key":"k4"},{"key":"k5"},{"key":"k6"},{"key":"k7"},{"key":"k8"},{"key":"k9"},{"key":"k0"}]}`)},
			keys:      "k0,k1,k2,k3,k4,k5,k6,k7,+2",
			fieldPath: "items[0].query_configs[0].agg_condition",
		},
		"an unreadable condition": {
			configs:   []json.RawMessage{json.RawMessage(`{"agg_condition":[]}`), json.RawMessage(`{"agg_condition":{"key":"bk_biz_id"}}`)},
			keys:      "unreadable",
			fieldPath: "items[0].query_configs[1].agg_condition",
		},
		"a condition without a key": {
			configs:   []json.RawMessage{json.RawMessage(`{"agg_condition":[{"method":"eq","value":[2]}]}`)},
			keys:      "(empty)",
			fieldPath: "items[0].query_configs[0].agg_condition",
		},
		"none": {
			configs: []json.RawMessage{json.RawMessage(`{"agg_condition":[]}`), json.RawMessage(`{}`)},
		},
	} {
		t.Run(name, func(t *testing.T) {
			keys, fieldPath := promQLConditionKeys(test.configs)
			if keys != test.keys || fieldPath != test.fieldPath {
				t.Fatalf("keys %q at %q, want %q at %q", keys, fieldPath, test.keys, test.fieldPath)
			}
		})
	}
}
