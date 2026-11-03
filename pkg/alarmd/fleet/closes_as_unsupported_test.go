// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// Every reason the capability table knows is classified here, by name, in
// exactly one of three groups, so a reason added later fails this test until
// someone says which it is. A capability alarmd does not have - by design or
// not yet in this build - closes the strategy's alerts as no longer
// detected. A refusal a configuration change, a limit or a writer lifts, or a
// defect of this build, keeps them: closing would end alerts nothing reopens
// once it lifts. A reason that is never a refusal - a note on a strategy that
// runs, or a rejected configuration that keeps its last good Plan - never
// reaches the close, and is checked against what the control plane files as
// a refusal. A reason nobody wrote keeps the alerts too.
func TestOnlyAnIrreversibleCapabilityRefusalClosesAsUnsupported(t *testing.T) {
	closes := []string{
		"QUERY_SOURCE_NOT_MIGRATED", "QUERY_MIXED_PROMQL_NOT_MIGRATED", "QUERY_OUTPUT_CONFIG_NOT_MIGRATED",
		"QUERY_BK_DATA_LOCAL_TIME_NOT_MIGRATED", "QUERY_CMDB_LEVEL_BYPASSES_UQ", "QUERY_FUNCTION_NOT_MIGRATED",
		"EXPRESSION_FUNCTION_NOT_MIGRATED", "UNSUPPORTED_MULTI_ITEM_STRATEGY", "QUERY_FTA_UNSUPPORTED",
		"QUERY_SOURCE_RETIRED", "QUERY_PROMQL_FORM_RETIRED",
		"UNSUPPORTED_TARGET_SCOPE", "UNSUPPORTED_TARGET_SCOPE_UNRESOLVABLE", "UNSUPPORTED_TARGET_VALUE_SHAPE",
		"GLOBAL_STRATEGY_UNSUPPORTED", "UNSUPPORTED_TARGET_PLAN",
		"ALGORITHM_NOT_MIGRATED", "ALGORITHM_UNSUPPORTED", "EFFECTIVE_TIME_SCHEMA_UNSUPPORTED",
	}
	keeps := []string{
		// Lifted by the deployment's configuration or limits.
		"DYNAMIC_GROUP_SOURCE_UNCONFIGURED", "SNAPSHOT_RETENTION_INSUFFICIENT", "COMPLETION_OFFSET_BELOW_RESERVE",
		"PLAN_BUDGET_EXCEEDED", "LEVEL_BUDGET_EXCEEDED",
		// Lifted by the target plan's writer.
		"TARGET_PLAN_MISSING", "TARGET_PLAN_EMPTY", "TARGET_PLAN_TENANT_MISMATCH",
		// A defect of this build.
		"EVALUATION_STEP_INCONSISTENT",
		"A_REASON_NOBODY_WROTE",
	}
	neverRefusals := []string{
		"SOURCE_NOT_SLIDING", "ALGORITHM_NOT_SLIDING", "ABOVE_AGG_INTERVAL", "NOT_DIVISIBLE", "BELOW_FLOOR",
		"DETECT_INTERVAL_INVALID", "NO_DATA_TRIGGER_BEYOND_HORIZON", "EFFECTIVE_TIME_RANGE_INVALID",
		"LEVEL_TRIGGER_BORROWED", "AGG_INTERVAL_DEFAULTED", "RECOVERY_CONFIG_DEFAULTED",
		"RECOVERY_STATUS_SETTER_DEFAULTED", "PRIORITY_IGNORED",
		"NO_DATA_GROUP_BOUND_ABOVE_DEFAULT", "RECOVERY_DISABLED_BY_CONFIG",
	}
	classified := map[string]string{}
	for group, reasons := range map[string][]string{"closes": closes, "keeps": keeps, "never a refusal": neverRefusals} {
		for _, reason := range reasons {
			if earlier, twice := classified[reason]; twice {
				t.Fatalf("%s is classified twice: %s and %s", reason, earlier, group)
			}
			classified[reason] = group
		}
	}
	for reason := range capabilityByReason {
		if _, known := classified[reason]; !known {
			t.Errorf("%s is in the capability table and classified nowhere here: say whether it closes", reason)
		}
	}
	for reason := range capabilityCostNotes {
		if classified[reason] != "never a refusal" {
			t.Errorf("cost note %s is not classified as never a refusal", reason)
		}
	}
	for _, reason := range closes {
		if !ClosesAsUnsupported(reason) {
			t.Errorf("ClosesAsUnsupported(%s) = false, want true: a capability this build does not have", reason)
		}
	}
	for _, reason := range keeps {
		if ClosesAsUnsupported(reason) {
			t.Errorf("ClosesAsUnsupported(%s) = true, want false: the refusal lifts", reason)
		}
	}
	refused := map[string]bool{}
	for _, reason := range capabilityDispositionReasons(t) {
		refused[reason] = true
	}
	for _, reason := range neverRefusals {
		if disposition, terminal := controlplane.CompilerTerminalDisposition(reason); refused[reason] || terminal && disposition == controlplane.DispositionUnsupported {
			t.Errorf("%s is classified as never a refusal and the control plane files it as one", reason)
		}
	}
}
