// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

// The capability matrix: which part of the capability boundary a refusal is
// about, and what it leaves the strategy doing. One table for the Leader that
// counts strategies per cell when it publishes and for the overview that
// shows them; the boundary itself is the capability and sizing reference.

// The areas, closed: the capability boundary's five parts.
const (
	CapabilitySource    = "source"
	CapabilityTarget    = "target"
	CapabilityAlgorithm = "algorithm"
	CapabilityOutput    = "output"
	CapabilityConfig    = "config"
)

// CapabilityAreas is the closed list, in the order the overview shows it.
var CapabilityAreas = []string{CapabilitySource, CapabilityTarget, CapabilityAlgorithm, CapabilityOutput, CapabilityConfig}

// The outcomes, closed: supported; partial, a strategy that detects with a
// part of it stopped (one level, its no-data detection, or a part read the
// platform's way); unsupported by design, never to be supported; and not yet
// supported, everything else that keeps it out.
const (
	CapabilitySupported           = "supported"
	CapabilityPartial             = "partial"
	CapabilityUnsupportedByDesign = "unsupported_by_design"
	CapabilityNotYetSupported     = "not_yet_supported"
)

// CapabilityOutcomes is the closed list, least severe first.
var CapabilityOutcomes = []string{CapabilitySupported, CapabilityPartial, CapabilityUnsupportedByDesign, CapabilityNotYetSupported}

// MoreSevereCapability is the outcome a strategy is counted under in an area
// where it has both: not yet supported over unsupported by design over
// partial over supported.
func MoreSevereCapability(a, b string) string {
	rank := func(outcome string) int {
		for index, known := range CapabilityOutcomes {
			if known == outcome {
				return index
			}
		}
		return len(CapabilityOutcomes)
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// CapabilityClass is one refusal's cell. IgnoredByDesign is a capability of
// the platform's own path that alarmd leaves out by design while the
// strategy runs on its own (priority arbitration); it is in no cell.
type CapabilityClass struct {
	Area            string
	Outcome         string
	IgnoredByDesign bool
	// Reversible is a refusal a configuration change, a limit or a writer
	// lifts - the deployment's dynamic-group source, its downstream reserve or
	// snapshot retention, its compiler budgets, a target plan the writer has
	// not written, wrote empty or wrote for another tenant - or a defect of
	// this build that a fixed one lifts, rather than a capability this build
	// does not have. Counted with the capability outcome it has, and never a
	// reason to close the strategy's alerts (ClosesAsUnsupported): closing
	// them for a refusal that lifts would end alerts nothing reopens.
	Reversible bool
}

// ClosesAsUnsupported is whether a strategy refused for reason
// (UNSUPPORTED_PHASE2_CAPABILITY) is one alarmd does not detect: a known
// capability refusal, by design or not yet supported in this build, that no
// configuration change lifts. Its open alerts close as no longer detected; a
// reversible refusal, or a reason this table does not know, keeps them open
// with the refusal named.
func ClosesAsUnsupported(reason string) bool {
	class, known := capabilityByReason[reason]
	if !known || class.Reversible {
		return false
	}
	return class.Outcome == CapabilityUnsupportedByDesign || class.Outcome == CapabilityNotYetSupported
}

// dispositionCompatibilityIgnored is a strategy alarmd leaves to the
// platform's own path by design; capabilityRuntime is not a disposition but
// the reasons a running Plan reports for a part of it that does not run,
// classified through the same table.
const (
	dispositionCompatibilityIgnored = "COMPATIBILITY_IGNORED"
	capabilityRuntime               = "RUNTIME"
)

// capabilityByReason is each withheld or noted reason's area and the outcome
// it has when it keeps the whole strategy out. Every reason in
// withheldReasonWords has a row; the words table's own test holds that list
// to the control plane's literals.
var capabilityByReason = map[string]CapabilityClass{
	"QUERY_SOURCE_NOT_MIGRATED":             {Area: CapabilitySource, Outcome: CapabilityNotYetSupported},
	"QUERY_MIXED_PROMQL_NOT_MIGRATED":       {Area: CapabilitySource, Outcome: CapabilityNotYetSupported},
	"QUERY_OUTPUT_CONFIG_NOT_MIGRATED":      {Area: CapabilitySource, Outcome: CapabilityNotYetSupported},
	"QUERY_BK_DATA_LOCAL_TIME_NOT_MIGRATED": {Area: CapabilitySource, Outcome: CapabilityNotYetSupported},
	"QUERY_CMDB_LEVEL_BYPASSES_UQ":          {Area: CapabilitySource, Outcome: CapabilityNotYetSupported},
	"QUERY_FUNCTION_NOT_MIGRATED":           {Area: CapabilitySource, Outcome: CapabilityNotYetSupported},
	"EXPRESSION_FUNCTION_NOT_MIGRATED":      {Area: CapabilitySource, Outcome: CapabilityNotYetSupported},
	"UNSUPPORTED_MULTI_ITEM_STRATEGY":       {Area: CapabilitySource, Outcome: CapabilityNotYetSupported},
	"SOURCE_NOT_SLIDING":                    {Area: CapabilitySource, Outcome: CapabilityNotYetSupported},
	"QUERY_FTA_UNSUPPORTED":                 {Area: CapabilitySource, Outcome: CapabilityUnsupportedByDesign},
	"QUERY_SOURCE_RETIRED":                  {Area: CapabilitySource, Outcome: CapabilityUnsupportedByDesign},
	"QUERY_PROMQL_FORM_RETIRED":             {Area: CapabilitySource, Outcome: CapabilityUnsupportedByDesign},

	"UNSUPPORTED_TARGET_SCOPE":              {Area: CapabilityTarget, Outcome: CapabilityNotYetSupported},
	"UNSUPPORTED_TARGET_SCOPE_UNRESOLVABLE": {Area: CapabilityTarget, Outcome: CapabilityNotYetSupported},
	"UNSUPPORTED_TARGET_VALUE_SHAPE":        {Area: CapabilityTarget, Outcome: CapabilityNotYetSupported},
	"GLOBAL_STRATEGY_UNSUPPORTED":           {Area: CapabilityTarget, Outcome: CapabilityNotYetSupported},
	"UNSUPPORTED_TARGET_PLAN":               {Area: CapabilityTarget, Outcome: CapabilityNotYetSupported},
	"TARGET_PLAN_EMPTY":                     {Area: CapabilityTarget, Outcome: CapabilityNotYetSupported, Reversible: true},
	"TARGET_PLAN_TENANT_MISMATCH":           {Area: CapabilityTarget, Outcome: CapabilityNotYetSupported, Reversible: true},
	"TARGET_PLAN_MISSING":                   {Area: CapabilityTarget, Outcome: CapabilityNotYetSupported, Reversible: true},
	"DYNAMIC_GROUP_SOURCE_UNCONFIGURED":     {Area: CapabilityTarget, Outcome: CapabilityNotYetSupported, Reversible: true},

	"ALGORITHM_NOT_MIGRATED":         {Area: CapabilityAlgorithm, Outcome: CapabilityNotYetSupported},
	"ALGORITHM_UNSUPPORTED":          {Area: CapabilityAlgorithm, Outcome: CapabilityNotYetSupported},
	"ALGORITHM_NOT_SLIDING":          {Area: CapabilityAlgorithm, Outcome: CapabilityNotYetSupported},
	"EVALUATION_STEP_INCONSISTENT":   {Area: CapabilityAlgorithm, Outcome: CapabilityNotYetSupported, Reversible: true},
	"NO_DATA_TRIGGER_BEYOND_HORIZON": {Area: CapabilityAlgorithm, Outcome: CapabilityNotYetSupported},

	"EFFECTIVE_TIME_SCHEMA_UNSUPPORTED": {Area: CapabilityConfig, Outcome: CapabilityNotYetSupported},
	"SNAPSHOT_RETENTION_INSUFFICIENT":   {Area: CapabilityConfig, Outcome: CapabilityNotYetSupported, Reversible: true},
	"COMPLETION_OFFSET_BELOW_RESERVE":   {Area: CapabilityConfig, Outcome: CapabilityNotYetSupported, Reversible: true},
	"PLAN_BUDGET_EXCEEDED":              {Area: CapabilityConfig, Outcome: CapabilityNotYetSupported, Reversible: true},
	"LEVEL_BUDGET_EXCEEDED":             {Area: CapabilityConfig, Outcome: CapabilityNotYetSupported, Reversible: true},
	"ABOVE_AGG_INTERVAL":                {Area: CapabilityConfig, Outcome: CapabilityNotYetSupported},
	"NOT_DIVISIBLE":                     {Area: CapabilityConfig, Outcome: CapabilityNotYetSupported},
	"BELOW_FLOOR":                       {Area: CapabilityConfig, Outcome: CapabilityNotYetSupported},
	"DETECT_INTERVAL_INVALID":           {Area: CapabilityConfig, Outcome: CapabilityNotYetSupported},
	"EFFECTIVE_TIME_RANGE_INVALID":      {Area: CapabilityConfig, Outcome: CapabilityNotYetSupported},
	"LEVEL_TRIGGER_BORROWED":            {Area: CapabilityConfig, Outcome: CapabilityNotYetSupported},
	"AGG_INTERVAL_DEFAULTED":            {Area: CapabilityConfig, Outcome: CapabilityNotYetSupported},
	"RECOVERY_CONFIG_DEFAULTED":         {Area: CapabilityConfig, Outcome: CapabilityNotYetSupported},
	"RECOVERY_STATUS_SETTER_DEFAULTED":  {Area: CapabilityConfig, Outcome: CapabilityNotYetSupported},

	"PRIORITY_IGNORED": {IgnoredByDesign: true},
}

// capabilityCostNotes are the reasons noted on a strategy that runs entirely
// as written: the note is a cost or a behaviour its owner chose, not a part
// of it that runs differently, so it is no capability fact and leaves the
// strategy supported.
var capabilityCostNotes = map[string]bool{
	"NO_DATA_GROUP_BOUND_ABOVE_DEFAULT": true,
	"RECOVERY_DISABLED_BY_CONFIG":       true,
}

// capabilityRejected is the area of the configuration refusals that are
// about one part of the boundary rather than the writer's configuration as
// a whole; every other refusal is the configuration's.
var capabilityRejected = map[string]string{
	"OUTPUT_PROTOCOL_REQUIRES_STRATEGY_REVISION": CapabilityOutput,
	"ALGORITHM_QUERY_INVALID":                    CapabilityAlgorithm,
}

// capabilityRuntimeReasons are what a running Plan reports for a part of it
// that does not run: the strategy is accepted and its refusal is only seen
// at runtime.
var capabilityRuntimeReasons = map[string]CapabilityClass{
	"NO_DATA_ROSTER_UNSUPPORTED":          {Area: CapabilityAlgorithm, Outcome: CapabilityPartial},
	"NO_DATA_CONFIG_INVALID":              {Area: CapabilityAlgorithm, Outcome: CapabilityPartial},
	"DETECT_INTERVAL_STORAGE_NOT_SLIDING": {Area: CapabilitySource, Outcome: CapabilityNotYetSupported},
}

// ClassifyCapability is the cell of one disposition record: its area and
// what it leaves the strategy doing. A noted strategy runs, so whatever was
// noted is a part of it; a refusal scoped to one level leaves the others
// detecting. known is false for a reason this table does not know, which is
// counted with the writer's configuration as not yet supported, never as
// supported -- and for a disposition about the source's state rather than
// the strategy (accepted, incomplete, stale, being removed), which is no
// capability fact and returns the zero class, as does a cost note.
func ClassifyCapability(disposition, reason, scope string) (CapabilityClass, bool) {
	var class CapabilityClass
	known := true
	if disposition == dispositionConfigNoted && capabilityCostNotes[reason] {
		return class, true
	}
	switch disposition {
	case dispositionCapabilityUnsupported, dispositionConfigNoted:
		class, known = capabilityByReason[reason]
		if !known {
			class = CapabilityClass{Area: CapabilityConfig, Outcome: CapabilityNotYetSupported}
		}
	case dispositionConfigRejected:
		class = CapabilityClass{Area: CapabilityConfig, Outcome: CapabilityNotYetSupported}
		if area, about := capabilityRejected[reason]; about {
			class.Area = area
		}
	case dispositionCompatibilityIgnored:
		return CapabilityClass{IgnoredByDesign: true}, true
	case capabilityRuntime:
		class, known = capabilityRuntimeReasons[reason]
		if !known {
			class = CapabilityClass{Area: CapabilityConfig, Outcome: CapabilityNotYetSupported}
		}
		return class, known
	default:
		return CapabilityClass{}, false
	}
	if class.IgnoredByDesign {
		return class, known
	}
	if disposition == dispositionConfigNoted || scope == "LEVEL" {
		class.Outcome = CapabilityPartial
	}
	return class, known
}
