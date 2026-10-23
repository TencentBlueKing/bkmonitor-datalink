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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// SourceRead is one place a strategy document is read: the path of the value
// read, as dotted keys with arrays left out and empty for the whole document,
// and what the value is decoded into - a reflect.Type of a struct, walked by
// its json tags, or the keys read from it, each a dotted path below the value.
type SourceRead struct {
	Path    string
	Decoded any
}

// LegacySourceReads is every place this package reads a strategy document,
// and what the compiler and the target plan decoder it hands values raw to
// decode them into. The operator evidence's source view shows every key they
// reach as written and any other key by its shape only; its test walks these
// reads, with those of the other packages that read the document, into that
// list (internal/sourcereads).
//
// A struct declared inside a function, a value kept raw and decoded later,
// and a key read from a map by name are what a walk of the structs cannot
// find, and are registered here by hand. A new place in this package that
// reads the document has to be registered here too, or the view can hide
// what it reads.
func LegacySourceReads() []SourceRead {
	return []SourceRead{
		// The catalog's decode of the document, and of the values it keeps
		// raw to decode later.
		{"", reflect.TypeOf(legacyStrategy{})},
		{"items.query_configs", reflect.TypeOf(legacyQueryConfig{})},
		{"items.query_configs", reflect.TypeOf(legacyQueryConfigIdentity{})},
		{"items.functions", reflect.TypeOf(legacyFunction{})},
		{"items.no_data_config", reflect.TypeOf(legacyNoDataConfig{})},
		{"items.target", reflect.TypeOf(legacyTargetCondition{})},
		{"items.target.value", reflect.TypeOf(legacyTargetValue{})},

		// Structs declared inside functions of this package, and keys it
		// reads by name, each under the function that reads it.
		//
		// The source adapter's identity read.
		{"", []string{"id", "bk_biz_id", "bk_tenant_id", "space_uid", "is_global_strategy"}},
		// decodeLegacyStrategy, for a document without a strategy_revision.
		{"", []string{"update_time"}},
		// compileTargetPlanDocument.
		{"items", []string{"target_plan", "query_configs"}},
		// itemUnit; frozenSubjectFacts; itemInterval and decodeLegacyQueryConfig.
		{"items.query_configs", []string{"unit", "result_table_id", "agg_interval"}},
		// thresholdConfig; compileAlgorithmConfig for SimpleRingRatio.
		{"items.algorithms.config", []string{"method", "threshold", "floor", "ceil"}},
		// orderedTargetValues.
		{"items.target", []string{"value"}},
		// objectModelInstanceKey.
		{"items.target.value", []string{defaultObjectModelField, defaultObjectModelInstField}},
		// legacyRecoveryWindow.
		{"detects.recovery_config", []string{"check_window"}},
		// isAlwaysActiveUptime.
		{"detects.trigger_config.uptime", []string{"calendars", "active_calendars", "time_ranges.start", "time_ranges.end"}},

		// What the compiler decodes the values the catalog hands it raw into.
		{"items.algorithms.config", strategy.TraditionalComparisonKeys()},
		{"detects.trigger_config.uptime", strategy.UptimeSource()},
		{"effective_time_snapshot", strategy.EffectiveTimeSnapshotSource()},
		{"effective_time_snapshot.calendars.items.repeat", strategy.EffectiveTimeRepeatSource()},
		// The target plan protocol, which its decoder holds a document to
		// key by key.
		{"items.target_plan", targetplan.DocumentKeys()},
	}
}
