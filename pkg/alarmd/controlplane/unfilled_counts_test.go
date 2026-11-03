// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package controlplane

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Which queries answer counts their provider does not zero-fill: the two log
// count sources, and a log time series aggregated by count or a count-like
// distinct. A log time series aggregated by avg, sum or max is not, and
// neither is an event count (its range is widened instead) or a metric.
func TestUnfilledCountsAreLogCountsAndCountedLogSeries(t *testing.T) {
	clause := func(over string, functions ...string) execution.QueryClause {
		c := execution.QueryClause{TimeAggregation: execution.QueryFunction{Method: over}}
		for _, method := range functions {
			c.Functions = append(c.Functions, execution.QueryFunction{Method: method})
		}
		return c
	}
	query := func(source string, clauses ...execution.QueryClause) execution.QueryPlanFacts {
		return execution.QueryPlanFacts{SourceSemantics: []string{source}, QueryList: clauses}
	}
	for name, test := range map[string]struct {
		query execution.QueryPlanFacts
		want  bool
	}{
		"log search log":           {query("bk_log_search/log", clause("count_over_time", "sum")), true},
		"monitor log":              {query("bk_monitor/log", clause("count_over_time", "sum")), true},
		"log series count":         {query("bk_log_search/time_series", clause("count_over_time", "sum")), true},
		"log series distinct":      {query("bk_log_search/time_series", clause("distinct_over_time", "distinct")), true},
		"log series count no time": {query("bk_log_search/time_series", clause("", "count")), true},
		"log series avg":           {query("bk_log_search/time_series", clause("avg_over_time", "mean")), false},
		"log series sum":           {query("bk_log_search/time_series", clause("sum_over_time", "sum")), false},
		"log series max":           {query("bk_log_search/time_series", clause("max_over_time", "max")), false},
		"log series mixed":         {query("bk_log_search/time_series", clause("count_over_time", "sum"), clause("avg_over_time", "mean")), false},
		"event count":              {query("custom/event", clause("count_over_time", "sum")), false},
		"metric count":             {query("bk_monitor/time_series", clause("count_over_time", "sum")), false},
		"two sources": {execution.QueryPlanFacts{SourceSemantics: []string{"bk_log_search/log", "bk_monitor/log"},
			QueryList: []execution.QueryClause{clause("count_over_time", "sum")}}, false},
	} {
		if got := readsUnfilledCounts(test.query); got != test.want {
			t.Errorf("%s: unfilled = %t, want %t", name, got, test.want)
		}
	}
}

// Which queries' zero is no observation: an event count, grouped or not, and a
// log count - a log source, or a log series counted - whose clauses group by
// nothing. A log count grouped by a dimension, a log series averaged, and a
// metric are observations.
func TestAZeroIsNoObservationForEventCountsAndUngroupedLogCounts(t *testing.T) {
	clause := func(over string, dimensions ...string) execution.QueryClause {
		return execution.QueryClause{TimeAggregation: execution.QueryFunction{Method: over}, Functions: []execution.QueryFunction{{Method: "sum"}}, Dimensions: dimensions}
	}
	query := func(source string, clauses ...execution.QueryClause) execution.QueryPlanFacts {
		return execution.QueryPlanFacts{SourceSemantics: []string{source}, StepMillis: 60000, QueryList: clauses}
	}
	for name, test := range map[string]struct {
		query execution.QueryPlanFacts
		want  bool
	}{
		"event count":                {query("custom/event", clause("count_over_time")), true},
		"event count grouped":        {query("custom/event", clause("count_over_time", "target")), true},
		"log count":                  {query("bk_log_search/log", clause("count_over_time")), true},
		"log count grouped":          {query("bk_log_search/log", clause("count_over_time", "host")), false},
		"monitor log count":          {query("bk_monitor/log", clause("count_over_time")), true},
		"log series counted":         {query("bk_log_search/time_series", clause("count_over_time")), true},
		"log series counted grouped": {query("bk_log_search/time_series", clause("count_over_time", "path")), false},
		"log series averaged":        {query("bk_log_search/time_series", clause("avg_over_time")), false},
		"metric count":               {query("bk_monitor/time_series", clause("count_over_time")), false},
	} {
		if got := readsZeroAsNoObservation(test.query); got != test.want {
			t.Errorf("%s: zero is no observation = %t, want %t", name, got, test.want)
		}
	}
}

// The primary requirement carries the words: unfilled for a log count and
// only for one, zero-is-no-observation for an event count and an ungrouped
// log count and only for those.
func TestThePrimaryRequirementSaysALogCountIsUnfilled(t *testing.T) {
	runtime := &RedisCatalogRuntime{downstreamReserve: time.Second}
	due := []execution.DuePlan{{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "101"},
		ScheduleSpec: execution.ScheduleSpec{EvaluationIntervalSeconds: 60}}}
	type words struct{ unfilled, zero bool }
	for source, want := range map[string]words{"bk_log_search/log": {unfilled: true, zero: true}, "custom/event": {zero: true}, "bk_monitor/time_series": {}} {
		group := QueryGroup{QueryPlan: execution.QueryPlanFacts{SourceSemantics: []string{source}, StepMillis: 60000, AlignmentMillis: 60000,
			QueryList: []execution.QueryClause{{TimeAggregation: execution.QueryFunction{Method: "count_over_time"}}}}}
		requirements, err := runtime.primaryRequirements(group, due, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(requirements) != 1 || requirements[0].UnfilledCounts != want.unfilled || requirements[0].ZeroIsNoObservation != want.zero {
			t.Fatalf("%s: requirements %+v, want one with UnfilledCounts %t and ZeroIsNoObservation %t", source, requirements, want.unfilled, want.zero)
		}
	}
}

// The word is observation only, so a log count's primary requirement keeps
// the identity and the due Plan set digest it had before the word existed:
// labelling the empty answer cuts no log Plan over. The values are the ones
// the build before (d03b7a7b4) derives for this group and Plan.
func TestALogCountsRequirementDigestIsUnchangedByTheWord(t *testing.T) {
	const (
		wantRequirement = "e43f63d66b5fb85ab5d2f82f647b29d6d2995e7380add0df9ec8964119d1699e"
		wantDigest      = "20d9c7a73b097f7bc7c051bf52a6e6f9afe751319974515c28564894ff034f1c"
	)
	runtime := &RedisCatalogRuntime{downstreamReserve: time.Second}
	due := []execution.DuePlan{{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "101"},
		ScheduleSpec: execution.ScheduleSpec{EvaluationIntervalSeconds: 60}, CompletionDeadlineUnixMilli: 90000}}
	levels := map[execution.PlanIdentity]map[uint32]struct{}{due[0].Identity: {1: {}}}
	for _, source := range []string{"bk_log_search/log", "bk_monitor/log", "bk_log_search/time_series"} {
		group := QueryGroup{QueryPlan: execution.QueryPlanFacts{SourceSemantics: []string{source}, QueryRevision: "query-r1",
			QueryDelaySeconds: 30, StepMillis: 60000, AlignmentMillis: 60000,
			QueryList:     []execution.QueryClause{{TimeAggregation: execution.QueryFunction{Method: "count_over_time"}}},
			Normalization: execution.DatasetNormalizationSpec{DatasetContract: contract.DatasetContractV2{IdentityFields: []string{"host"}}}}}
		requirements, err := runtime.primaryRequirements(group, due, levels)
		if err != nil {
			t.Fatal(err)
		}
		if len(requirements) != 1 || !requirements[0].UnfilledCounts {
			t.Fatalf("%s: requirements %+v, want one carrying UnfilledCounts", source, requirements)
		}
		digest, err := execution.DeriveDuePlanSetDigest(nil, requirements)
		if err != nil {
			t.Fatal(err)
		}
		if string(requirements[0].RequirementID) != wantRequirement || string(digest) != wantDigest {
			t.Fatalf("%s: requirement %s digest %s, want %s and %s as the build before derived them",
				source, requirements[0].RequirementID, digest, wantRequirement, wantDigest)
		}
	}
}
