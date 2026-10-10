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
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	model "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The key configuration a strategy runs with, read on request from the
// frozen execution content the Leader published for it -- one bounded point
// read per Plan, and only when the reader asks (include=config). It is what
// an operator needs to check that the strategy is running as written --
// which table and metric, over which dimensions, how often, with what
// target, which levels and algorithms, whether absence is judged -- without
// the page copying the catalog or scanning anything in the background.
//
// Redacted by construction. The page is reachable without a login on at
// least one deployment, so the projection carries names, identifiers, kinds
// and counts, and never a value a strategy was written against: not the
// member keys of a target, not the values a condition compares with, not
// the text of a PromQL expression or a query string, not an algorithm's
// thresholds. A reader who needs those has the strategy's own page; this
// one answers "is it configured the way I think" and "how big is it".

// StrategyObjectLoader reads the frozen execution content stored under an
// object digest: the catalog repository's point read, behind a function so
// the handler needs neither Redis nor the repository type.
type StrategyObjectLoader func(ctx context.Context, objectDigest string) (controlplane.QueryGroupObject, error)

// MaxStrategyConfigReads bounds the object reads one request may cost. A
// strategy is one Query Group per (tenant, business) in practice; a request
// that would read more says so and stops.
const MaxStrategyConfigReads = 8

// StrategyConfigReadTimeout bounds the whole of a request's reads. The
// object store answers a point read in milliseconds; a request that takes
// longer is a store that is not answering, and the reader is told that.
const StrategyConfigReadTimeout = 2 * time.Second

// The include word the reader passes, and the refusals a Plan's config may
// carry instead of content.
const (
	IncludeConfig = "config"

	ConfigLoaderNotWired    = "LOADER_NOT_WIRED"
	ConfigReadBoundExceeded = "READ_BOUND_EXCEEDED"
	// The three ways a read comes back without an object, told apart
	// because they are three different people's problems: the store did
	// not answer (a dependency), the digest the catalog names is not in the
	// store (the Leader's publication and the store disagree -- the Worker
	// cannot run this Plan either), the bytes under it are not the object
	// (corruption).
	ConfigObjectUnreadable = "OBJECT_UNREADABLE"
	ConfigObjectMissing    = "OBJECT_MISSING"
	ConfigObjectCorrupt    = "OBJECT_CORRUPT"
	ConfigPlanNotInObject  = "PLAN_NOT_IN_OBJECT"
	ConfigNoObjectDigest   = "NO_OBJECT_DIGEST"
)

// ConfigRefusals is the closed list, for the page's wording table.
var ConfigRefusals = []string{ConfigLoaderNotWired, ConfigReadBoundExceeded, ConfigObjectUnreadable, ConfigObjectMissing, ConfigObjectCorrupt, ConfigPlanNotInObject, ConfigNoObjectDigest}

// StrategyPlanConfigs is what one Plan reference's include=config carries:
// the Plans of this strategy inside the object (one per item), or the reason
// none could be read.
type StrategyPlanConfigs struct {
	Items   []StrategyPlanConfig `json:"items,omitempty"`
	Refusal string               `json:"refusal,omitempty"`
	// Redacted says so on the wire, so a reader does not take the absence
	// of values for their absence in the strategy.
	Redacted bool `json:"redacted"`
}

// StrategyPlanConfig is one item's frozen configuration, redacted.
type StrategyPlanConfig struct {
	PlanID             string                   `json:"plan_id"`
	StateGeneration    string                   `json:"state_generation,omitempty"`
	Schedule           StrategyScheduleConfig   `json:"schedule"`
	Query              StrategyQueryConfig      `json:"query"`
	Target             StrategyTargetConfig     `json:"target"`
	NoData             *StrategyNoDataConfig    `json:"no_data,omitempty"`
	Levels             []StrategyLevelConfig    `json:"levels"`
	Requirements       []StrategyRequirementRef `json:"requirements,omitempty"`
	TerminalReasonCode string                   `json:"terminal_reason_code,omitempty"`
}

// StrategyScheduleConfig is how often and when the Plan is evaluated.
type StrategyScheduleConfig struct {
	IntervalSeconds         int64  `json:"interval_seconds"`
	AlignmentSeconds        int64  `json:"alignment_seconds"`
	Timezone                string `json:"timezone,omitempty"`
	CompletionOffsetSeconds int64  `json:"completion_offset_seconds,omitempty"`
}

// StrategyQueryConfig is what the Plan reads, by identifier and shape.
type StrategyQueryConfig struct {
	Provider          string   `json:"provider,omitempty"`
	Tenant            string   `json:"tenant,omitempty"`
	Business          string   `json:"business,omitempty"`
	SpaceScope        string   `json:"space_scope,omitempty"`
	SourceSemantics   []string `json:"source_semantics,omitempty"`
	QueryDelaySeconds int64    `json:"query_delay_seconds,omitempty"`
	StepMillis        int64    `json:"step_millis,omitempty"`
	// MetricMerge is the expression the clauses are combined with, written
	// by the strategy's author: present and how long, never the text.
	MetricMerge *StrategyRedactedTextInfo `json:"metric_merge,omitempty"`
	Timezone    string                    `json:"timezone,omitempty"`
	Clauses     []StrategyQueryClause     `json:"clauses"`
	// PromQL says an expression is frozen, and how long it is; never the
	// expression.
	PromQL *StrategyPromQLConfig `json:"promql,omitempty"`
}

// StrategyQueryClause is one query of the Plan: table, metric, dimensions,
// functions, and the shape of its conditions.
type StrategyQueryClause struct {
	Reference       string                    `json:"reference,omitempty"`
	DataSource      string                    `json:"data_source,omitempty"`
	Driver          string                    `json:"driver,omitempty"`
	TableID         string                    `json:"table_id,omitempty"`
	Field           string                    `json:"field,omitempty"`
	FieldSemantics  string                    `json:"field_semantics,omitempty"`
	TimeField       string                    `json:"time_field,omitempty"`
	Regexp          bool                      `json:"regexp,omitempty"`
	Functions       []StrategyQueryFunction   `json:"functions,omitempty"`
	TimeAggregation *StrategyQueryFunction    `json:"time_aggregation,omitempty"`
	Dimensions      []string                  `json:"dimensions,omitempty"`
	Conditions      []StrategyQueryCondition  `json:"conditions,omitempty"`
	Connectors      []string                  `json:"connectors,omitempty"`
	Offset          string                    `json:"offset,omitempty"`
	QueryString     *StrategyRedactedTextInfo `json:"query_string,omitempty"`
}

// StrategyQueryFunction is one aggregation or function by name and window.
type StrategyQueryFunction struct {
	Method     string   `json:"method"`
	Window     string   `json:"window,omitempty"`
	Dimensions []string `json:"dimensions,omitempty"`
	Arguments  int      `json:"arguments,omitempty"`
}

// StrategyQueryCondition is one condition by field and operator; the values
// are a count.
type StrategyQueryCondition struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Values   int    `json:"values"`
}

// StrategyPromQLConfig says an expression is frozen and how long it is.
type StrategyPromQLConfig struct {
	Present         bool `json:"present"`
	ExpressionBytes int  `json:"expression_bytes"`
}

// StrategyRedactedTextInfo says a text is frozen and how long it is.
type StrategyRedactedTextInfo struct {
	Present bool `json:"present"`
	Bytes   int  `json:"bytes"`
}

// StrategyTargetConfig is the Plan's target: none, a frozen scope, or a
// target plan; both forms as shapes and counts.
type StrategyTargetConfig struct {
	Kind  string                    `json:"kind"`
	Scope *StrategyTargetScopeInfo  `json:"scope,omitempty"`
	Plan  *StrategyTargetPlanConfig `json:"plan,omitempty"`
}

// The three kinds a target comes in.
const (
	TargetKindNone  = "NONE"
	TargetKindScope = "SCOPE"
	TargetKindPlan  = "PLAN"
)

// TargetKinds is the closed list, for the page's wording table.
var TargetKinds = []string{TargetKindNone, TargetKindScope, TargetKindPlan}

// StrategyTargetScopeInfo is a frozen scope as groups of conditions, each
// condition by field and method with its key count.
type StrategyTargetScopeInfo struct {
	Groups     int                            `json:"groups"`
	Conditions []StrategyTargetScopeCondition `json:"conditions"`
}

// StrategyTargetScopeCondition is one condition of a scope.
type StrategyTargetScopeCondition struct {
	Group  int    `json:"group"`
	Field  string `json:"field"`
	Method string `json:"method"`
	Keys   int    `json:"keys"`
}

// StrategyTargetPlanConfig is a target plan by rule, identity and counts.
type StrategyTargetPlanConfig struct {
	SchemaVersion      int      `json:"schema_version"`
	ModelID            string   `json:"model_id,omitempty"`
	Rule               string   `json:"rule"`
	IdentityDimensions []string `json:"identity_dimensions,omitempty"`
	ModelDimension     string   `json:"model_dimension,omitempty"`
	StaticKeys         int      `json:"static_keys"`
	StaticMembers      int      `json:"static_members"`
	DynamicGroups      int      `json:"dynamic_groups"`
	DynamicTopologies  int      `json:"dynamic_topologies"`
}

// StrategyNoDataConfig is whether and how absence is judged.
type StrategyNoDataConfig struct {
	Continuous   uint32   `json:"continuous"`
	Level        uint32   `json:"level"`
	AggDimension []string `json:"agg_dimension,omitempty"`
	// TrackingHorizonSeconds is the effective horizon compilation froze into
	// the Plan, the platform's or the item's own; zero is none.
	TrackingHorizonSeconds int64 `json:"tracking_horizon_seconds,omitempty"`
}

// StrategyLevelConfig is one level: its definition, how its algorithms
// combine, the algorithms by type and version, and the trigger and recovery
// plans by type, version and the window counts the compiler defines. The
// algorithm configuration is a size. The trigger and recovery configuration
// is an open document -- json.RawMessage, whatever a plan type puts there
// -- so it is read by the keys this build's compiler defines and nothing
// else reaches the wire: a closed guard cannot cover an open input, and a
// plan type that one day carries text would otherwise carry it here.
type StrategyLevelConfig struct {
	LevelID    uint32                  `json:"level_id"`
	LevelCode  string                  `json:"level_code,omitempty"`
	Priority   uint32                  `json:"priority"`
	Connector  string                  `json:"connector,omitempty"`
	Algorithms []StrategyAlgorithmInfo `json:"algorithms"`
	Trigger    StrategyTypedPlanConfig `json:"trigger"`
	Recovery   StrategyTypedPlanConfig `json:"recovery"`
}

// StrategyAlgorithmInfo is one algorithm by type and version, with the size
// of its configuration.
type StrategyAlgorithmInfo struct {
	Type        string `json:"type"`
	Version     uint32 `json:"version"`
	ConfigBytes int    `json:"config_bytes"`
}

// StrategyTypedPlanConfig is a trigger or recovery plan by type and version,
// with the window counts the compiler defines for the two plan types this
// build has (N_OF_M: window_size, required_anomalies, step_seconds;
// CONTINUOUS_TRIGGER_MISS: enabled, consecutive_windows). A key outside them
// is counted under UnknownKeys and its value dropped, so a reader knows the
// document had more than the projection shows.
type StrategyTypedPlanConfig struct {
	Type               string  `json:"type,omitempty"`
	Version            uint32  `json:"version,omitempty"`
	WindowSize         *uint32 `json:"window_size,omitempty"`
	RequiredAnomalies  *uint32 `json:"required_anomalies,omitempty"`
	StepSeconds        *uint32 `json:"step_seconds,omitempty"`
	Enabled            *bool   `json:"enabled,omitempty"`
	ConsecutiveWindows *uint32 `json:"consecutive_windows,omitempty"`
	UnknownKeys        int     `json:"unknown_keys,omitempty"`
	// Undecodable says the document was not a JSON object at all.
	Undecodable bool `json:"undecodable,omitempty"`
}

// StrategyRequirementRef is one data requirement of the Plan: which level
// consumes which logical query over which relative window.
type StrategyRequirementRef struct {
	Dataset         string `json:"dataset"`
	Role            string `json:"role"`
	ConsumerLevelID uint32 `json:"consumer_level_id"`
	LogicalQuery    string `json:"logical_query"`
	WindowStart     int64  `json:"window_start_offset_seconds"`
	WindowEnd       int64  `json:"window_end_offset_seconds"`
	StepMillis      int64  `json:"step_millis,omitempty"`
	ReadinessClass  string `json:"readiness_class,omitempty"`
	Points          int    `json:"points,omitempty"`
}

// StrategyPlanConfigsOf projects the Plans of one strategy inside an object.
// business narrows to one when given; a strategy with no Plan in the object
// is a refusal, not an empty success.
func StrategyPlanConfigsOf(object controlplane.QueryGroupObject, strategyID, business string) StrategyPlanConfigs {
	configs := StrategyPlanConfigs{Redacted: true}
	for _, plan := range object.Plans {
		if plan.Identity.StrategyID != strategyID || business != "" && plan.Identity.BusinessID != business {
			continue
		}
		configs.Items = append(configs.Items, strategyPlanConfigOf(object, plan))
	}
	if len(configs.Items) == 0 {
		configs.Refusal = ConfigPlanNotInObject
	}
	return configs
}

func strategyPlanConfigOf(object controlplane.QueryGroupObject, plan controlplane.QueryGroupPlanObject) StrategyPlanConfig {
	config := StrategyPlanConfig{
		PlanID: plan.PlanID, StateGeneration: string(plan.StateGeneration),
		Schedule: StrategyScheduleConfig{
			IntervalSeconds: plan.ScheduleSpec.EvaluationIntervalSeconds, AlignmentSeconds: int64(plan.ScheduleSpec.Alignment),
			Timezone: plan.ScheduleSpec.Timezone, CompletionOffsetSeconds: plan.ScheduleSpec.CompletionDeadlineOffsetSeconds,
		},
		Query:              strategyQueryConfigOf(object.QueryPlan, plan.QueryPlans),
		Target:             strategyTargetConfigOf(plan.TargetScope, plan.TargetPlan),
		Levels:             []StrategyLevelConfig{},
		TerminalReasonCode: plan.TerminalReasonCode,
	}
	if plan.NoData != nil {
		config.NoData = &StrategyNoDataConfig{Continuous: plan.NoData.Continuous, Level: plan.NoData.Level,
			AggDimension:           append([]string(nil), plan.NoData.AggDimension...),
			TrackingHorizonSeconds: plan.NoData.TrackingHorizonSeconds}
	}
	for _, level := range plan.StrategyIR.Levels {
		entry := StrategyLevelConfig{
			LevelID: level.Definition.LevelID, LevelCode: level.Definition.LevelCode, Priority: level.Definition.Priority,
			Connector: level.Connector, Algorithms: []StrategyAlgorithmInfo{},
			Trigger:  typedPlanConfigOf(level.TriggerPlan),
			Recovery: typedPlanConfigOf(level.RecoveryPlan),
		}
		for _, algorithm := range level.DetectPlan.Algorithms {
			entry.Algorithms = append(entry.Algorithms, StrategyAlgorithmInfo{Type: algorithm.Type, Version: algorithm.Version, ConfigBytes: len(algorithm.Config)})
		}
		config.Levels = append(config.Levels, entry)
	}
	for _, template := range plan.RequirementTemplates {
		config.Requirements = append(config.Requirements, StrategyRequirementRef{
			Dataset: string(template.DatasetName), Role: string(template.Role), ConsumerLevelID: template.ConsumerLevelID,
			LogicalQuery: string(template.LogicalQueryRef), WindowStart: template.RelativeWindow.StartOffsetSeconds,
			WindowEnd: template.RelativeWindow.EndOffsetSeconds, StepMillis: template.StepMillis,
			ReadinessClass: string(template.ReadinessClass), Points: len(template.PointOffsetsSeconds) + len(template.NamedPoints),
		})
	}
	return config
}

func typedPlanConfigOf(plan contract.TypedPlanV1) StrategyTypedPlanConfig {
	config := StrategyTypedPlanConfig{Type: plan.Type, Version: plan.Version}
	if len(plan.Config) == 0 {
		return config
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(plan.Config, &document); err != nil {
		config.Undecodable = true
		return config
	}
	for key, raw := range document {
		switch key {
		case "window_size":
			config.WindowSize = typedPlanCount(raw, &config.UnknownKeys)
		case "required_anomalies":
			config.RequiredAnomalies = typedPlanCount(raw, &config.UnknownKeys)
		case "step_seconds":
			config.StepSeconds = typedPlanCount(raw, &config.UnknownKeys)
		case "consecutive_windows":
			config.ConsecutiveWindows = typedPlanCount(raw, &config.UnknownKeys)
		case "enabled":
			var enabled bool
			if json.Unmarshal(raw, &enabled) == nil {
				config.Enabled = &enabled
			} else {
				config.UnknownKeys++
			}
		default:
			config.UnknownKeys++
		}
	}
	return config
}

// typedPlanCount reads one window count; a value that is not a whole
// non-negative number is not a count and is counted as unknown instead.
func typedPlanCount(raw json.RawMessage, unknown *int) *uint32 {
	var value float64
	if json.Unmarshal(raw, &value) != nil || value < 0 || value != float64(uint32(value)) {
		*unknown++
		return nil
	}
	count := uint32(value)
	return &count
}

// strategyQueryConfigOf reads the object's query plan, and the Plan's own
// query plans by logical reference where it has them; the clauses are the
// union, each by its reference.
func strategyQueryConfigOf(shared model.QueryPlanFacts, own map[model.LogicalQueryRef]model.QueryPlanFacts) StrategyQueryConfig {
	config := StrategyQueryConfig{
		Provider: string(shared.Provider), Tenant: shared.TenantID, Business: shared.BusinessID, SpaceScope: shared.SpaceScope,
		SourceSemantics: append([]string(nil), shared.SourceSemantics...), QueryDelaySeconds: shared.QueryDelaySeconds,
		StepMillis: shared.StepMillis, Timezone: shared.Timezone, Clauses: []StrategyQueryClause{},
	}
	if shared.MetricMerge != "" {
		config.MetricMerge = &StrategyRedactedTextInfo{Present: true, Bytes: len(shared.MetricMerge)}
	}
	if shared.PromQL != nil {
		config.PromQL = &StrategyPromQLConfig{Present: true, ExpressionBytes: len(shared.PromQL.Expression)}
	}
	for _, clause := range shared.QueryList {
		config.Clauses = append(config.Clauses, strategyQueryClauseOf(clause))
	}
	refs := make([]string, 0, len(own))
	for ref := range own {
		refs = append(refs, string(ref))
	}
	sort.Strings(refs)
	for _, ref := range refs {
		facts := own[model.LogicalQueryRef(ref)]
		for _, clause := range facts.QueryList {
			projected := strategyQueryClauseOf(clause)
			if projected.Reference == "" {
				projected.Reference = ref
			}
			config.Clauses = append(config.Clauses, projected)
		}
		if facts.PromQL != nil && config.PromQL == nil {
			config.PromQL = &StrategyPromQLConfig{Present: true, ExpressionBytes: len(facts.PromQL.Expression)}
		}
	}
	return config
}

func strategyQueryClauseOf(clause model.QueryClause) StrategyQueryClause {
	projected := StrategyQueryClause{
		Reference: clause.ReferenceName, DataSource: clause.DataSource, Driver: clause.Driver, TableID: clause.TableID,
		Field: clause.FieldName, FieldSemantics: clause.FieldSemantics, TimeField: clause.TimeField, Regexp: clause.IsRegexp,
		Dimensions: append([]string(nil), clause.Dimensions...), Offset: clause.Offset,
		Connectors: append([]string(nil), clause.Conditions.Connectors...),
	}
	for _, function := range clause.Functions {
		projected.Functions = append(projected.Functions, strategyQueryFunctionOf(function))
	}
	if clause.TimeAggregation.Method != "" || clause.TimeAggregation.Window != "" {
		aggregation := strategyQueryFunctionOf(clause.TimeAggregation)
		projected.TimeAggregation = &aggregation
	}
	for _, condition := range clause.Conditions.Fields {
		projected.Conditions = append(projected.Conditions, StrategyQueryCondition{Field: condition.Field, Operator: condition.Operator, Values: len(condition.Values)})
	}
	if clause.QueryString != "" {
		projected.QueryString = &StrategyRedactedTextInfo{Present: true, Bytes: len(clause.QueryString)}
	}
	return projected
}

func strategyQueryFunctionOf(function model.QueryFunction) StrategyQueryFunction {
	return StrategyQueryFunction{Method: function.Method, Window: function.Window,
		Dimensions: append([]string(nil), function.Dimensions...), Arguments: len(function.Arguments)}
}

func strategyTargetConfigOf(scope *contract.TargetScopeV2, plan *contract.TargetPlanV1) StrategyTargetConfig {
	switch {
	case plan != nil:
		return StrategyTargetConfig{Kind: TargetKindPlan, Plan: &StrategyTargetPlanConfig{
			SchemaVersion: plan.SchemaVersion, ModelID: plan.ModelID, Rule: string(plan.Rule),
			IdentityDimensions: append([]string(nil), plan.Identity.Dimensions...), ModelDimension: plan.Identity.ModelDimension,
			StaticKeys: len(plan.StaticKeys), StaticMembers: len(plan.StaticMembers),
			DynamicGroups: len(plan.DynamicGroups), DynamicTopologies: len(plan.DynamicTopologies),
		}}
	case scope != nil:
		info := &StrategyTargetScopeInfo{Groups: len(scope.Groups), Conditions: []StrategyTargetScopeCondition{}}
		for index, group := range scope.Groups {
			for _, condition := range group.Conditions {
				info.Conditions = append(info.Conditions, StrategyTargetScopeCondition{Group: index, Field: string(condition.Field), Method: string(condition.Method), Keys: len(condition.Keys)})
			}
		}
		return StrategyTargetConfig{Kind: TargetKindScope, Scope: info}
	default:
		return StrategyTargetConfig{Kind: TargetKindNone}
	}
}

// attachStrategyConfigs reads each Plan's object once and attaches its
// configs, within the read bound and the request's deadline. A Plan past the
// bound, without a digest, or whose object could not be read carries the
// refusal in place of content, so a reader can tell "not configured" from
// "not read".
func attachStrategyConfigs(ctx context.Context, standing *StrategyStanding, loader StrategyObjectLoader) {
	ctx, cancel := context.WithTimeout(ctx, StrategyConfigReadTimeout)
	defer cancel()
	reads := 0
	for index := range standing.Plans {
		plan := &standing.Plans[index]
		configs := StrategyPlanConfigs{Redacted: true}
		switch {
		case loader == nil:
			configs.Refusal = ConfigLoaderNotWired
		case plan.ObjectDigest == "":
			configs.Refusal = ConfigNoObjectDigest
		case reads >= MaxStrategyConfigReads:
			configs.Refusal = ConfigReadBoundExceeded
		default:
			reads++
			object, err := loader(ctx, plan.ObjectDigest)
			if err != nil {
				configs.Refusal = configReadRefusal(err)
				if errors.Is(err, context.DeadlineExceeded) {
					// The deadline is the request's: every Plan after this
					// one would wait on the same store, so they are told the
					// same thing without being tried.
					cancel()
				}
			} else {
				configs = StrategyPlanConfigsOf(object, standing.StrategyID, plan.Business)
			}
		}
		plan.Config = &configs
	}
}

// configReadRefusal names why a read came back without an object, from the
// repository's typed errors; anything else is the store not answering.
func configReadRefusal(err error) string {
	switch {
	case errors.Is(err, controlplane.ErrCatalogObjectUnavailable):
		return ConfigObjectMissing
	case errors.Is(err, controlplane.ErrCatalogObjectCorrupt):
		return ConfigObjectCorrupt
	default:
		return ConfigObjectUnreadable
	}
}
