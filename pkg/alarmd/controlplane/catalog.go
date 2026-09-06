package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

const SourceAlgorithmTypePingUnreachable = "PingUnreachable"

type SourceIdentity struct {
	TenantID   string
	BusinessID string
	SpaceScope string
}

type SourceStrategy struct {
	SourceID          string
	Document          json.RawMessage
	Identity          SourceIdentity
	SourceDisposition *ObjectDisposition
}

type PrimaryQuerySource struct {
	Identity       SourceIdentity
	StrategyID     string
	ItemID         string
	QueryMD5       string
	Expression     string
	IdentityFields []string
	Functions      []json.RawMessage
	QueryConfigs   []json.RawMessage
}

type PrimaryQueryCompiler interface {
	CompilePrimaryQuery(context.Context, PrimaryQuerySource) (execution.QueryPlanFacts, error)
}

type AlgorithmDependencyQueryCompiler interface {
	CompileAlgorithmDependencyQuery(context.Context, PrimaryQuerySource, string) (execution.QueryPlanFacts, error)
}

type BuildRequest struct {
	Strategies []SourceStrategy
	Planner    PrimaryQueryCompiler
	LastGood   *PublishedSnapshot
}

type FrozenPlan struct {
	Identity             execution.PlanIdentity
	Plan                 contract.EvaluationPlanV2
	PlanRevision         string
	StateGeneration      execution.StateGeneration `json:",omitempty"`
	ScheduleSpec         execution.ScheduleSpec
	ScheduleRevision     execution.PlanScheduleRevision
	RequirementTemplates []execution.DataRequirementTemplate                    `json:"RequirementTemplates,omitempty"`
	QueryPlans           map[execution.LogicalQueryRef]execution.QueryPlanFacts `json:"QueryPlans,omitempty"`
}

type QueryGroup struct {
	Identity         execution.QueryGroupIdentity
	QueryPlan        execution.QueryPlanFacts
	Plans            []FrozenPlan
	MembershipDigest string
	ScheduleRevision execution.ScheduleRevision
}

type Disposition string

const (
	DispositionAccepted             Disposition = "ACCEPTED"
	DispositionSourceIncomplete     Disposition = "SOURCE_INCOMPLETE"
	DispositionConfigRejected       Disposition = "CONFIG_REJECTED"
	DispositionStaleConfig          Disposition = "STALE_CONFIG"
	DispositionPendingRemoval       Disposition = "PENDING_REMOVAL"
	DispositionUnsupported          Disposition = "UNSUPPORTED_PHASE2_CAPABILITY"
	DispositionCompatibilityIgnored Disposition = "COMPATIBILITY_IGNORED"
)

type ObjectDisposition struct {
	SourceID    string
	Scope       string
	LevelID     uint32
	Disposition Disposition
	Reason      string
}

type Catalog struct {
	ObservationID    string
	SnapshotRevision execution.SnapshotRevision
	QueryGroups      []QueryGroup
	Dispositions     []ObjectDisposition
}

func BuildCatalog(ctx context.Context, request BuildRequest) (Catalog, error) {
	if request.Planner == nil || request.Strategies == nil {
		return Catalog{}, errors.New("alarmd controlplane: incomplete catalog build request")
	}
	observationID, err := deriveObservationID(request.Strategies)
	if err != nil {
		return Catalog{}, err
	}
	catalog := Catalog{ObservationID: observationID, QueryGroups: []QueryGroup{}, Dispositions: []ObjectDisposition{}}
	groups := make(map[execution.QueryGroupIdentity]*QueryGroup)
	seenPlans := make(map[execution.PlanIdentity]struct{}, len(request.Strategies))
	lastGood := indexLastGoodPlans(request.LastGood)
	addPlan := func(facts execution.QueryPlanFacts, plan FrozenPlan) error {
		if _, duplicate := seenPlans[plan.Identity]; duplicate {
			return errors.New("alarmd controlplane: duplicate Plan identity")
		}
		seenPlans[plan.Identity] = struct{}{}
		identity, err := deriveQueryGroupIdentity(facts)
		if err != nil {
			return err
		}
		group := groups[identity]
		if group == nil {
			group = &QueryGroup{Identity: identity, QueryPlan: facts}
			groups[identity] = group
		} else if group.QueryPlan.QueryRevision != facts.QueryRevision {
			return errors.New("alarmd controlplane: one query group has conflicting query revisions")
		}
		group.Plans = append(group.Plans, plan)
		return nil
	}
	retainLastGood := func(sourceID string) (bool, error) {
		entry, ok := lastGood[sourceID]
		if !ok {
			return false, nil
		}
		if err := addPlan(entry.facts, entry.plan); err != nil {
			return false, err
		}
		return true, nil
	}
	observed := make(map[string]struct{}, len(request.Strategies))
	for _, source := range request.Strategies {
		observed[source.SourceID] = struct{}{}
		if source.SourceDisposition != nil {
			disposition := *source.SourceDisposition
			if disposition.SourceID == "" {
				disposition.SourceID = source.SourceID
			}
			if disposition.SourceID != source.SourceID || disposition.Scope != "STRATEGY" || disposition.Reason == "" ||
				(disposition.Disposition != DispositionSourceIncomplete && disposition.Disposition != DispositionConfigRejected) {
				return Catalog{}, errors.New("alarmd controlplane: invalid source disposition")
			}
			retained, err := retainLastGood(source.SourceID)
			if err != nil {
				return Catalog{}, err
			}
			if retained && disposition.Disposition == DispositionConfigRejected {
				disposition.Disposition = DispositionStaleConfig
			}
			catalog.Dispositions = append(catalog.Dispositions, disposition)
			continue
		}
		candidate, err := buildCandidate(ctx, request.Planner, source)
		if err != nil {
			if len(candidate.dispositions) > 0 {
				catalog.Dispositions = append(catalog.Dispositions, candidate.dispositions...)
			} else {
				catalog.Dispositions = append(catalog.Dispositions, ObjectDisposition{SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionConfigRejected, Reason: "PLAN_INVALID"})
			}
			if shouldRetainLastGood(candidate.dispositions) {
				retained, retainErr := retainLastGood(source.SourceID)
				if retainErr != nil {
					return Catalog{}, retainErr
				}
				if retained {
					markStaleConfig(catalog.Dispositions, source.SourceID)
				}
			}
			continue
		}
		planIdentity := candidate.plan.Identity
		if _, duplicate := seenPlans[planIdentity]; duplicate {
			catalog.Dispositions = append(catalog.Dispositions, ObjectDisposition{SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionConfigRejected, Reason: "DUPLICATE_STRATEGY_IDENTITY"})
			continue
		}
		if err := addPlan(candidate.facts, candidate.plan); err != nil {
			return Catalog{}, err
		}
		catalog.Dispositions = append(catalog.Dispositions, candidate.dispositions...)
		catalog.Dispositions = append(catalog.Dispositions, ObjectDisposition{SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionAccepted})
	}
	for sourceID := range lastGood {
		if _, found := observed[sourceID]; found {
			continue
		}
		retained, err := retainLastGood(sourceID)
		if err != nil {
			return Catalog{}, err
		}
		if retained {
			catalog.Dispositions = append(catalog.Dispositions, ObjectDisposition{SourceID: sourceID, Scope: "STRATEGY",
				Disposition: DispositionPendingRemoval, Reason: "REMOVED_FROM_ACTIVE_SET"})
		}
	}

	for _, group := range groups {
		sort.Slice(group.Plans, func(i, j int) bool { return lessPlanIdentity(group.Plans[i].Identity, group.Plans[j].Identity) })
		identities := make([]execution.PlanIdentity, len(group.Plans))
		schedules := make([]execution.FrozenPlanSchedule, len(group.Plans))
		for i, plan := range group.Plans {
			identities[i] = plan.Identity
			schedules[i] = execution.FrozenPlanSchedule{Identity: plan.Identity, ScheduleRevision: plan.ScheduleRevision, Spec: plan.ScheduleSpec}
		}
		digest, err := contract.DeriveCanonicalDigestV2("alarmd-query-group-membership-v1", identities)
		if err != nil {
			return Catalog{}, err
		}
		group.MembershipDigest = digest
		scheduleDigest, err := execution.DeriveQueryGroupScheduleRevision(schedules)
		if err != nil {
			return Catalog{}, err
		}
		group.ScheduleRevision = scheduleDigest
		catalog.QueryGroups = append(catalog.QueryGroups, *group)
	}
	sort.Slice(catalog.QueryGroups, func(i, j int) bool { return catalog.QueryGroups[i].Identity < catalog.QueryGroups[j].Identity })
	sort.Slice(catalog.Dispositions, func(i, j int) bool { return lessDisposition(catalog.Dispositions[i], catalog.Dispositions[j]) })
	catalog.SnapshotRevision, err = deriveSnapshotRevision(catalog.QueryGroups)
	if err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

type lastGoodPlan struct {
	facts execution.QueryPlanFacts
	plan  FrozenPlan
}

func indexLastGoodPlans(snapshot *PublishedSnapshot) map[string]lastGoodPlan {
	result := make(map[string]lastGoodPlan)
	if snapshot == nil {
		return result
	}
	for _, group := range snapshot.QueryGroups {
		for _, plan := range group.Plans {
			result[plan.Identity.StrategyID] = lastGoodPlan{facts: group.QueryPlan, plan: plan}
		}
	}
	return result
}

func shouldRetainLastGood(dispositions []ObjectDisposition) bool {
	for _, disposition := range dispositions {
		if disposition.Disposition == DispositionUnsupported {
			return false
		}
	}
	return true
}

func markStaleConfig(dispositions []ObjectDisposition, sourceID string) {
	for index := range dispositions {
		if dispositions[index].SourceID == sourceID && dispositions[index].Disposition == DispositionConfigRejected {
			dispositions[index].Disposition = DispositionStaleConfig
		}
	}
}

func lessDisposition(left, right ObjectDisposition) bool {
	if left.SourceID != right.SourceID {
		return left.SourceID < right.SourceID
	}
	if left.Scope != right.Scope {
		return left.Scope < right.Scope
	}
	if left.LevelID != right.LevelID {
		return left.LevelID < right.LevelID
	}
	if left.Disposition != right.Disposition {
		return left.Disposition < right.Disposition
	}
	return left.Reason < right.Reason
}

func deriveSnapshotRevision(groups []QueryGroup) (execution.SnapshotRevision, error) {
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-strategy-snapshot-v1", groups)
	return execution.SnapshotRevision(digest), err
}

type sourceCandidate struct {
	facts        execution.QueryPlanFacts
	plan         FrozenPlan
	dispositions []ObjectDisposition
}

func buildCandidate(ctx context.Context, planner PrimaryQueryCompiler, source SourceStrategy) (sourceCandidate, error) {
	candidate := sourceCandidate{}
	if err := source.Identity.validate(); err != nil {
		return sourceCandidate{}, err
	}
	legacy, err := decodeLegacyStrategy(source.Document)
	if err != nil {
		return sourceCandidate{}, err
	}
	if strconv.FormatInt(legacy.BusinessID, 10) != source.Identity.BusinessID {
		return sourceCandidate{}, errors.New("BUSINESS_IDENTITY_MISMATCH")
	}
	if source.SourceID == "" || source.SourceID != strconv.FormatInt(legacy.ID, 10) {
		return sourceCandidate{}, errors.New("SOURCE_IDENTITY_MISMATCH")
	}
	if hasJSONValue(legacy.Priority) || legacy.PriorityGroupKey != "" {
		return sourceCandidate{dispositions: []ObjectDisposition{{SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionUnsupported, Reason: "UNSUPPORTED_PRIORITY_SEMANTICS"}}}, errors.New("alarmd controlplane: priority semantics unsupported")
	}
	if len(legacy.Items) > 1 {
		return sourceCandidate{dispositions: []ObjectDisposition{{SourceID: source.SourceID, Scope: "PLAN", Disposition: DispositionUnsupported, Reason: "UNSUPPORTED_MULTI_ITEM_STRATEGY"}}}, errors.New("alarmd controlplane: multiple Item strategies unsupported in phase two")
	}
	item := legacy.Items[0]
	if item.ID <= 0 || item.QueryMD5 == "" || item.Expression == "" || len(item.QueryConfigs) == 0 || len(item.Algorithms) == 0 {
		return candidate, errors.New("INCOMPLETE_SERIES_THRESHOLD_ITEM")
	}
	primaryExpression, identityFields := primaryQueryContract(item)
	functions := append([]json.RawMessage(nil), item.Functions...)
	if itemHasAlgorithm(item, strategy.DetectorKindOsRestart) {
		functions = nil
	}
	querySource := PrimaryQuerySource{
		Identity: source.Identity, StrategyID: source.SourceID, ItemID: strconv.FormatInt(item.ID, 10),
		QueryMD5: item.QueryMD5, Expression: primaryExpression, IdentityFields: identityFields,
		Functions: functions, QueryConfigs: append([]json.RawMessage(nil), item.QueryConfigs...),
	}
	facts, err := planner.CompilePrimaryQuery(ctx, querySource)
	if err != nil {
		var compileFailure *QueryPlanCompileError
		if errors.As(err, &compileFailure) && compileFailure.Disposition != "" && compileFailure.Reason != "" {
			candidate.dispositions = append(candidate.dispositions, ObjectDisposition{SourceID: source.SourceID, Scope: "PLAN", Disposition: compileFailure.Disposition, Reason: compileFailure.Reason})
		}
		return candidate, fmt.Errorf("QUERY_PLAN_INVALID: %w", err)
	}
	if err := validateQueryIdentity(source.Identity, facts); err != nil {
		return candidate, err
	}
	compiledInputs := compiledPlanInputs{primary: facts}
	if itemHasAlgorithm(item, strategy.DetectorKindOsRestart) {
		dependencyCompiler, ok := planner.(AlgorithmDependencyQueryCompiler)
		if !ok {
			return candidate, errors.New("ALGORITHM_DEPENDENCY_QUERY_COMPILER_UNAVAILABLE")
		}
		history, historyErr := dependencyCompiler.CompileAlgorithmDependencyQuery(ctx, querySource, "a")
		if historyErr != nil {
			return candidate, fmt.Errorf("QUERY_PLAN_INVALID: %w", historyErr)
		}
		if err := validateQueryIdentity(source.Identity, history); err != nil {
			return candidate, err
		}
		compiledInputs.osRestartHistory = &history
	}
	plan, scheduleSpec, schedule, dispositions, err := compilePlan(
		legacy, item, source.Identity, facts.Normalization.DatasetContract, source.SourceID, &compiledInputs,
	)
	if err != nil {
		candidate.dispositions = append(candidate.dispositions, dispositions...)
		return candidate, err
	}
	revision, err := contract.DeriveCanonicalDigestV2("alarmd-plan-semantics-v1", plan)
	if err != nil {
		return sourceCandidate{}, err
	}
	candidate.facts = facts
	candidate.plan = FrozenPlan{
		Identity: execution.PlanIdentity{TenantID: source.Identity.TenantID, BusinessID: source.Identity.BusinessID, StrategyID: source.SourceID},
		Plan:     plan, PlanRevision: revision, ScheduleSpec: scheduleSpec, ScheduleRevision: schedule,
		RequirementTemplates: compiledInputs.requirements, QueryPlans: compiledInputs.queryPlans,
	}
	candidate.dispositions = append(candidate.dispositions, dispositions...)
	return candidate, nil
}

func primaryQueryContract(item legacyItem) (string, []string) {
	expression := item.Expression
	if itemHasAlgorithm(item, strategy.DetectorKindOsRestart) {
		expression = "a <= 3600"
	}
	if itemHasAlgorithm(item, strategy.DetectorKindProcPort) {
		return expression, []string{"bk_target_cloud_id", "bk_target_ip", "display_name"}
	}
	return expression, nil
}

func itemHasAlgorithm(item legacyItem, kind string) bool {
	for _, algorithm := range item.Algorithms {
		if algorithm.Type == kind {
			return true
		}
	}
	return false
}

func (identity SourceIdentity) validate() error {
	if identity.TenantID == "" || identity.BusinessID == "" || identity.SpaceScope == "" {
		return errors.New("alarmd controlplane: tenant, business and space facts are required")
	}
	business, err := strconv.ParseInt(identity.BusinessID, 10, 64)
	if err != nil || business == 0 || strconv.FormatInt(business, 10) != identity.BusinessID {
		return errors.New("alarmd controlplane: business identity must be canonical non-zero signed decimal")
	}
	return nil
}

func validateQueryIdentity(identity SourceIdentity, facts execution.QueryPlanFacts) error {
	if err := facts.Validate(); err != nil {
		return err
	}
	if facts.TenantID != identity.TenantID || facts.BusinessID != identity.BusinessID || facts.SpaceScope != identity.SpaceScope {
		return errors.New("alarmd controlplane: query plan identity differs from control facts")
	}
	return nil
}

func deriveQueryGroupIdentity(facts execution.QueryPlanFacts) (execution.QueryGroupIdentity, error) {
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-query-group-identity-v1", struct {
		Provider      execution.ProviderKind             `json:"provider"`
		Route         execution.ProviderRouteRef         `json:"route"`
		Tenant        string                             `json:"tenant"`
		Business      string                             `json:"business"`
		Space         string                             `json:"space"`
		QueryList     []execution.QueryClause            `json:"query_list"`
		MetricMerge   string                             `json:"metric_merge"`
		StepMillis    int64                              `json:"step_millis"`
		Alignment     int64                              `json:"alignment_millis"`
		DownSample    execution.DownSampleRange          `json:"down_sample_range"`
		Timezone      string                             `json:"timezone"`
		NotTimeAlign  bool                               `json:"not_time_align"`
		Normalization execution.DatasetNormalizationSpec `json:"normalization"`
	}{facts.Provider, facts.ProviderRouteRef, facts.TenantID, facts.BusinessID, facts.SpaceScope,
		facts.QueryList, facts.MetricMerge, facts.StepMillis, facts.AlignmentMillis, facts.DownSampleRange,
		facts.Timezone, facts.NotTimeAlign, facts.Normalization})
	return execution.QueryGroupIdentity(digest), err
}

func lessPlanIdentity(left, right execution.PlanIdentity) bool {
	if left.TenantID != right.TenantID {
		return left.TenantID < right.TenantID
	}
	if left.BusinessID != right.BusinessID {
		return left.BusinessID < right.BusinessID
	}
	return left.StrategyID < right.StrategyID
}

type legacyStrategy struct {
	ID               int64           `json:"id"`
	BusinessID       int64           `json:"bk_biz_id"`
	UpdateTime       json.Number     `json:"update_time"`
	Priority         json.RawMessage `json:"priority"`
	PriorityGroupKey string          `json:"priority_group_key"`
	Items            []legacyItem    `json:"items"`
	Detects          []legacyDetect  `json:"detects"`
}
type legacyItem struct {
	ID           int64             `json:"id"`
	QueryMD5     string            `json:"query_md5"`
	Expression   string            `json:"expression"`
	Functions    []json.RawMessage `json:"functions"`
	QueryConfigs []json.RawMessage `json:"query_configs"`
	Algorithms   []legacyAlgorithm `json:"algorithms"`
	Unit         string            `json:"unit"`
}
type legacyAlgorithm struct {
	Level      uint32          `json:"level"`
	Type       string          `json:"type"`
	UnitPrefix string          `json:"unit_prefix"`
	Config     json.RawMessage `json:"config"`
}
type legacyDetect struct {
	Level     uint32          `json:"level"`
	Priority  *uint32         `json:"priority"`
	Connector string          `json:"connector"`
	Trigger   legacyTrigger   `json:"trigger_config"`
	Recovery  json.RawMessage `json:"recovery_config"`
}
type legacyTrigger struct {
	Count       uint32          `json:"count"`
	CheckWindow uint32          `json:"check_window"`
	Uptime      json.RawMessage `json:"uptime"`
}
type legacyRecovery struct {
	CheckWindow uint32 `json:"check_window"`
}

func decodeLegacyStrategy(document json.RawMessage) (legacyStrategy, error) {
	decoder := json.NewDecoder(strings.NewReader(string(document)))
	decoder.UseNumber()
	var value legacyStrategy
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("alarmd controlplane: decode strategy: %w", err)
	}
	if value.ID <= 0 || value.BusinessID == 0 || len(value.Items) == 0 {
		return value, errors.New("alarmd controlplane: incomplete legacy strategy")
	}
	return value, nil
}

type compiledPlanInputs struct {
	primary          execution.QueryPlanFacts
	osRestartHistory *execution.QueryPlanFacts
	requirements     []execution.DataRequirementTemplate
	queryPlans       map[execution.LogicalQueryRef]execution.QueryPlanFacts
	seenRequirements map[execution.RequirementID]struct{}
}

func compilePlan(
	source legacyStrategy,
	item legacyItem,
	identity SourceIdentity,
	dataset contract.DatasetContractV2,
	sourceID string,
	inputs *compiledPlanInputs,
) (contract.EvaluationPlanV2, execution.ScheduleSpec, execution.PlanScheduleRevision, []ObjectDisposition, error) {
	if hasJSONValue(source.Priority) || source.PriorityGroupKey != "" {
		return contract.EvaluationPlanV2{}, execution.ScheduleSpec{}, "", nil, errors.New("alarmd controlplane: G1 does not support priority semantics")
	}
	interval, err := itemInterval(item)
	if err != nil {
		return contract.EvaluationPlanV2{}, execution.ScheduleSpec{}, "", nil, err
	}
	strategyID := strconv.FormatInt(source.ID, 10)
	revision := source.UpdateTime.String()
	if revision != "" {
		value, parseErr := source.UpdateTime.Float64()
		if parseErr != nil {
			return contract.EvaluationPlanV2{}, execution.ScheduleSpec{}, "", nil, fmt.Errorf("alarmd controlplane: invalid strategy update_time: %w", parseErr)
		}
		if value == 0 {
			revision = ""
		}
	}
	if revision == "" {
		revision, err = contract.DeriveCanonicalDigestV2("alarmd-legacy-strategy-revision-v1", source)
		if err != nil {
			return contract.EvaluationPlanV2{}, execution.ScheduleSpec{}, "", nil, err
		}
	}
	ref := contract.StrategyRefV2{TenantID: identity.TenantID, StrategyID: strategyID, Revision: revision}
	dimensionFields := append([]string(nil), dataset.IdentityFields...)
	if itemHasAlgorithm(item, strategy.DetectorKindProcPort) {
		dimensionFields = []string{"bind_ip", "listen", "nonlisten", "not_accurate_listen", "protocol"}
	}
	projection := contract.InputProjectionV2{ValueFields: []string{"value"}, DimensionFields: dimensionFields, BusinessIdentityField: "bk_biz_id", MultiValueAlignment: "SINGLE_VALUE", DataUnit: item.Unit, MissingValuePolicy: contract.MissingValuePolicyRequired}
	detectByLevel := make(map[uint32]legacyDetect, len(source.Detects))
	duplicateDetect := make(map[uint32]struct{})
	for _, detect := range source.Detects {
		if _, duplicate := detectByLevel[detect.Level]; duplicate {
			duplicateDetect[detect.Level] = struct{}{}
		}
		detectByLevel[detect.Level] = detect
	}
	rawAlgorithms := make(map[uint32][]legacyAlgorithm)
	for _, raw := range item.Algorithms {
		rawAlgorithms[raw.Level] = append(rawAlgorithms[raw.Level], raw)
	}
	levelIDs := make([]int, 0, len(rawAlgorithms))
	for level := range rawAlgorithms {
		levelIDs = append(levelIDs, int(level))
	}
	sort.Ints(levelIDs)
	for _, rawLevel := range levelIDs {
		detect, ok := detectByLevel[uint32(rawLevel)]
		if ok && !isAlwaysActiveUptime(detect.Trigger.Uptime) {
			disposition := ObjectDisposition{SourceID: sourceID, Scope: "PLAN", Disposition: DispositionUnsupported, Reason: "EFFECTIVE_TIME_NOT_MIGRATED"}
			return contract.EvaluationPlanV2{}, execution.ScheduleSpec{}, "", []ObjectDisposition{disposition}, errors.New("alarmd controlplane: non-default uptime unsupported")
		}
	}
	levels := make([]contract.LevelIRV2, 0, len(levelIDs))
	dispositions := make([]ObjectDisposition, 0)
	for _, rawLevel := range levelIDs {
		levelID := uint32(rawLevel)
		detect, ok := detectByLevel[levelID]
		_, duplicate := duplicateDetect[levelID]
		if !ok || detect.Level == 0 || detect.Trigger.Count == 0 || detect.Trigger.CheckWindow == 0 || duplicate {
			dispositions = append(dispositions, ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionConfigRejected, Reason: "TRIGGER_CONFIG_MISSING"})
			continue
		}
		levelInputs := compiledPlanInputs{primary: inputs.primary, osRestartHistory: inputs.osRestartHistory}
		compiledAlgorithms := make([]contract.AlgorithmIRV2, 0, len(rawAlgorithms[levelID]))
		invalid := false
		for _, raw := range rawAlgorithms[levelID] {
			if !supportedAlgorithmKind(raw.Type) {
				dispositions = append(dispositions, ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionUnsupported, Reason: "ALGORITHM_NOT_MIGRATED"})
				invalid = true
				break
			}
			if err := validateCanonicalAlgorithmQuery(raw.Type, item.QueryConfigs); err != nil {
				dispositions = append(dispositions, ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionConfigRejected, Reason: "ALGORITHM_QUERY_INVALID"})
				invalid = true
				break
			}
			config, err := compileAlgorithmConfig(raw, item.Unit, levelID, projection, dataset.IdentityFields, interval, &levelInputs)
			if err != nil {
				reason := "ALGORITHM_CONFIG_INVALID"
				if raw.Type == strategy.DetectorKindThreshold {
					reason = "THRESHOLD_CONFIG_INVALID"
				}
				dispositions = append(dispositions, ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionConfigRejected, Reason: reason})
				invalid = true
				break
			}
			detectorKind := raw.Type
			if raw.Type == SourceAlgorithmTypePingUnreachable {
				detectorKind = strategy.DetectorKindThreshold
			}
			compiledAlgorithms = append(compiledAlgorithms, contract.AlgorithmIRV2{Type: detectorKind, Version: 1, Config: config})
		}
		if invalid {
			continue
		}
		priority := uint32(0)
		if detect.Priority == nil {
			if levelID <= 3 {
				priority = levelID
			}
		} else {
			priority = *detect.Priority
		}
		if priority == 0 {
			dispositions = append(dispositions, ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionConfigRejected, Reason: "LEVEL_PRIORITY_INVALID"})
			continue
		}
		recoveryConfig, recoveryEnabled, err := decodeLegacyRecovery(detect.Recovery)
		if err != nil || (recoveryEnabled && recoveryConfig.CheckWindow == 0) {
			dispositions = append(dispositions, ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionConfigRejected, Reason: "RECOVERY_CONFIG_INVALID"})
			continue
		}
		trigger, _ := json.Marshal(map[string]any{"required_anomalies": detect.Trigger.Count, "step_seconds": interval, "window_size": detect.Trigger.CheckWindow})
		recovery, _ := json.Marshal(map[string]any{"consecutive_windows": recoveryConfig.CheckWindow, "enabled": recoveryEnabled})
		connector := contract.LevelConnectorAND
		if strings.EqualFold(detect.Connector, "or") {
			connector = contract.LevelConnectorOR
		}
		levels = append(levels, contract.LevelIRV2{Definition: contract.LevelDefinitionV2{LevelID: levelID, Priority: priority}, Connector: connector, DetectPlan: contract.DetectPlanV2{Algorithms: compiledAlgorithms}, TriggerPlan: contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: trigger}, RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: recovery}})
		inputs.merge(levelInputs)
	}
	if len(levels) == 0 {
		return contract.EvaluationPlanV2{}, execution.ScheduleSpec{}, "", dispositions, errors.New("alarmd controlplane: no executable level")
	}
	semantics := contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: uint32(interval), AggregationInterval: uint32(interval), EvaluationInterval: uint32(interval), LatenessTolerance: uint32(interval * 2)}
	ir := contract.StrategyIRV2{Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2, Minor: 0}, RequiredFeatures: []string{}, StrategyRef: ref, ExecutionSemantics: semantics, InputProjection: projection, Levels: levels}
	plan := contract.EvaluationPlanV2{PlanID: strategyID, StrategyRef: ref, InputProjection: projection, SourceCompatibility: &contract.SourceCompatibilityV2{ItemID: strconv.FormatInt(item.ID, 10)}, StrategyIR: ir}
	scheduleSpec := execution.ScheduleSpec{EvaluationIntervalSeconds: interval, Alignment: 0, Timezone: "UTC"}
	if interval == 10 || interval == 15 {
		scheduleSpec.CompletionDeadlineOffsetSeconds = 30
	}
	schedule, err := execution.DerivePlanScheduleRevision(scheduleSpec)
	return plan, scheduleSpec, schedule, dispositions, err
}

func supportedAlgorithmKind(kind string) bool {
	switch kind {
	case strategy.DetectorKindThreshold, strategy.DetectorKindSimpleRingRatio, strategy.DetectorKindOsRestart,
		strategy.DetectorKindProcPort, SourceAlgorithmTypePingUnreachable:
		return true
	default:
		return false
	}
}

func compileAlgorithmConfig(
	raw legacyAlgorithm,
	unit string,
	levelID uint32,
	projection contract.InputProjectionV2,
	identityFields []string,
	interval int64,
	inputs *compiledPlanInputs,
) (json.RawMessage, error) {
	if raw.Type == strategy.DetectorKindThreshold {
		return thresholdConfig(raw, unit)
	}
	if inputs == nil {
		return nil, errors.New("alarmd controlplane: algorithm input facts are missing")
	}
	inputProjection := execution.InputProjection{
		ValueFields:     append([]string(nil), projection.ValueFields...),
		DimensionFields: append([]string(nil), projection.DimensionFields...),
		IdentityFields:  append([]string(nil), identityFields...),
	}
	requirements, err := inputs.buildRequirements(raw.Type, levelID, interval, inputProjection)
	if err != nil {
		return nil, err
	}
	algorithmProjection := strategy.AlgorithmInputProjection{
		ValueFields:     append([]string(nil), inputProjection.ValueFields...),
		DimensionFields: append([]string(nil), inputProjection.DimensionFields...),
		IdentityFields:  append([]string(nil), inputProjection.IdentityFields...),
	}
	algorithmRequirements := make([]strategy.AlgorithmInputRequirement, len(requirements))
	for index, requirement := range requirements {
		algorithmRequirements[index] = strategy.AlgorithmInputRequirement{
			RequirementID: string(requirement.RequirementID), DatasetName: string(requirement.DatasetName),
			Role: strategy.AlgorithmInputRole(requirement.Role), ConsumerLevelID: requirement.ConsumerLevelID,
			LogicalQueryRef: string(requirement.LogicalQueryRef),
			RelativeWindow: strategy.AlgorithmRelativeWindow{
				StartOffsetSeconds: requirement.RelativeWindow.StartOffsetSeconds,
				EndOffsetSeconds:   requirement.RelativeWindow.EndOffsetSeconds,
				HalfOpen:           requirement.RelativeWindow.HalfOpen,
			},
			StepMillis: requirement.StepMillis, AlignmentMillis: requirement.AlignmentMillis,
			ReadinessClass:      strategy.AlgorithmReadinessClass(requirement.ReadinessClass),
			InputProjection:     algorithmProjection,
			PointOffsetsSeconds: append([]int64(nil), requirement.PointOffsetsSeconds...),
			NamedPoints:         make([]strategy.AlgorithmNamedInputPoint, len(requirement.NamedPoints)),
		}
		for pointIndex, point := range requirement.NamedPoints {
			algorithmRequirements[index].NamedPoints[pointIndex] = strategy.AlgorithmNamedInputPoint{
				Name: point.Name, OffsetSeconds: point.OffsetSeconds,
			}
		}
	}
	if raw.Type == strategy.DetectorKindSimpleRingRatio {
		var sourceConfig struct {
			Floor json.RawMessage `json:"floor"`
			Ceil  json.RawMessage `json:"ceil"`
		}
		if err := json.Unmarshal(raw.Config, &sourceConfig); err != nil {
			return nil, errors.New("alarmd controlplane: invalid SimpleRingRatio config")
		}
		return json.Marshal(struct {
			Floor           json.RawMessage                      `json:"floor"`
			Ceil            json.RawMessage                      `json:"ceil"`
			InputProjection strategy.AlgorithmInputProjection    `json:"input_projection"`
			Requirements    []strategy.AlgorithmInputRequirement `json:"requirements"`
		}{sourceConfig.Floor, sourceConfig.Ceil, algorithmProjection, algorithmRequirements})
	}
	if raw.Type == SourceAlgorithmTypePingUnreachable {
		if !emptyAlgorithmConfig(raw.Config) {
			return nil, errors.New("alarmd controlplane: invalid PingUnreachable config")
		}
		return json.Marshal(struct {
			ValueField            string                               `json:"value_field"`
			DataUnit              string                               `json:"data_unit"`
			ThresholdUnitPrefix   string                               `json:"threshold_unit_prefix"`
			Precision             map[string]any                       `json:"precision"`
			Groups                []map[string]any                     `json:"groups"`
			SourceAlgorithmFamily string                               `json:"source_algorithm_family"`
			SourceMappingVersion  string                               `json:"source_mapping_version"`
			CanonicalQueryDigest  string                               `json:"canonical_query_digest"`
			InputProjection       strategy.AlgorithmInputProjection    `json:"input_projection"`
			Requirements          []strategy.AlgorithmInputRequirement `json:"requirements"`
		}{
			ValueField: "value", DataUnit: unit, ThresholdUnitPrefix: "",
			Precision:             map[string]any{"decimal_places": 6, "rounding": "HALF_EVEN"},
			Groups:                []map[string]any{{"conditions": []map[string]any{{"operator": "GTE", "threshold_decimal": "1"}}}},
			SourceAlgorithmFamily: strategy.SourceAlgorithmFamilyPingUnreachable,
			SourceMappingVersion:  strategy.SourceMappingPingUnreachableV1,
			CanonicalQueryDigest:  string(inputs.primary.QueryRevision),
			InputProjection:       algorithmProjection, Requirements: algorithmRequirements,
		})
	}
	return json.Marshal(struct {
		InputProjection strategy.AlgorithmInputProjection    `json:"input_projection"`
		Requirements    []strategy.AlgorithmInputRequirement `json:"requirements"`
	}{algorithmProjection, algorithmRequirements})
}

type canonicalAlgorithmQuery struct {
	ResultTableID string
	MetricID      string
	MetricField   string
	AggMethod     string
	AggInterval   int64
}

func validateCanonicalAlgorithmQuery(kind string, rawConfigs []json.RawMessage) error {
	expected, fixed := map[string]canonicalAlgorithmQuery{
		strategy.DetectorKindOsRestart:     {ResultTableID: "system.env", MetricID: "bk_monitor.os_restart", MetricField: "uptime", AggMethod: "MAX", AggInterval: 60},
		strategy.DetectorKindProcPort:      {ResultTableID: "system.proc_port", MetricID: "bk_monitor.proc_port", MetricField: "proc_exists", AggMethod: "MAX", AggInterval: 60},
		SourceAlgorithmTypePingUnreachable: {ResultTableID: "pingserver.base", MetricID: "bk_monitor.ping-gse", MetricField: "loss_percent", AggMethod: "MAX", AggInterval: 60},
	}[kind]
	if !fixed {
		return nil
	}
	if len(rawConfigs) != 1 {
		return errors.New("alarmd controlplane: fixed algorithm requires one canonical query")
	}
	config, err := decodeLegacyQueryConfig(rawConfigs[0])
	if err != nil {
		return err
	}
	table := config.ResultTableID
	if config.DataLabel != "" {
		table = config.DataLabel
	}
	if config.ResultTableID != expected.ResultTableID || table != expected.ResultTableID || config.MetricID != expected.MetricID || config.MetricField != expected.MetricField ||
		config.AggMethod != expected.AggMethod || config.AggInterval != expected.AggInterval || len(config.Values) != 0 {
		return errors.New("alarmd controlplane: fixed algorithm query differs from canonical source")
	}
	return nil
}

func emptyAlgorithmConfig(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null" || trimmed == "{}" || trimmed == "[]"
}

func (inputs *compiledPlanInputs) buildRequirements(
	kind string,
	levelID uint32,
	interval int64,
	projection execution.InputProjection,
) ([]execution.DataRequirementTemplate, error) {
	if err := inputs.primary.Validate(); err != nil {
		return nil, err
	}
	primaryRef := execution.LogicalQueryRef(inputs.primary.QueryRevision)
	primary, err := execution.BuildDataRequirementTemplate(execution.DataRequirementTemplate{
		DatasetName: "primary", Role: execution.InputRolePrimary, ConsumerLevelID: levelID,
		LogicalQueryRef: primaryRef,
		RelativeWindow:  execution.RelativeQueryWindow{StartOffsetSeconds: -interval, EndOffsetSeconds: 0, HalfOpen: true},
		StepMillis:      inputs.primary.StepMillis, AlignmentMillis: inputs.primary.AlignmentMillis,
		ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessEager,
		InputProjection: projection,
	})
	if err != nil {
		return nil, err
	}
	result := []execution.DataRequirementTemplate{primary}
	inputs.addRequirement(primary)
	inputs.addQuery(primaryRef, inputs.primary)

	switch kind {
	case strategy.DetectorKindSimpleRingRatio:
		dependency, err := execution.BuildDataRequirementTemplate(execution.DataRequirementTemplate{
			DatasetName: "previous", Role: execution.InputRoleAlgorithmDependency, ConsumerLevelID: levelID,
			LogicalQueryRef: primaryRef,
			RelativeWindow:  execution.RelativeQueryWindow{StartOffsetSeconds: -2 * interval, EndOffsetSeconds: -interval, HalfOpen: true},
			StepMillis:      inputs.primary.StepMillis, AlignmentMillis: inputs.primary.AlignmentMillis,
			ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessFinalizedRequired,
			InputProjection: projection, PointOffsetsSeconds: []int64{interval},
			NamedPoints: []execution.NamedInputPoint{{Name: "previous", OffsetSeconds: interval}},
		})
		if err != nil {
			return nil, err
		}
		result = append(result, dependency)
		inputs.addRequirement(dependency)
	case strategy.DetectorKindOsRestart:
		if inputs.osRestartHistory == nil {
			return nil, errors.New("alarmd controlplane: OsRestart history query facts are missing")
		}
		historyRef := execution.LogicalQueryRef(inputs.osRestartHistory.QueryRevision)
		dependency, err := execution.BuildDataRequirementTemplate(execution.DataRequirementTemplate{
			DatasetName: "uptime_history", Role: execution.InputRoleAlgorithmDependency, ConsumerLevelID: levelID,
			LogicalQueryRef: historyRef,
			RelativeWindow:  execution.RelativeQueryWindow{StartOffsetSeconds: -(1500 + interval), EndOffsetSeconds: 0, HalfOpen: true},
			StepMillis:      inputs.osRestartHistory.StepMillis, AlignmentMillis: inputs.osRestartHistory.AlignmentMillis,
			ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessFinalizedRequired,
			InputProjection: projection, PointOffsetsSeconds: []int64{interval, 600, 1500},
			NamedPoints: []execution.NamedInputPoint{
				{Name: "previous", OffsetSeconds: interval}, {Name: "previous_10m", OffsetSeconds: 600},
				{Name: "previous_25m", OffsetSeconds: 1500},
			},
		})
		if err != nil {
			return nil, err
		}
		result = append(result, dependency)
		inputs.addRequirement(dependency)
		inputs.addQuery(historyRef, *inputs.osRestartHistory)
	}
	return result, nil
}

func (inputs *compiledPlanInputs) addRequirement(requirement execution.DataRequirementTemplate) {
	if inputs.seenRequirements == nil {
		inputs.seenRequirements = make(map[execution.RequirementID]struct{})
	}
	if _, exists := inputs.seenRequirements[requirement.RequirementID]; exists {
		return
	}
	inputs.seenRequirements[requirement.RequirementID] = struct{}{}
	inputs.requirements = append(inputs.requirements, requirement)
}

func (inputs *compiledPlanInputs) addQuery(ref execution.LogicalQueryRef, facts execution.QueryPlanFacts) {
	if inputs.queryPlans == nil {
		inputs.queryPlans = make(map[execution.LogicalQueryRef]execution.QueryPlanFacts)
	}
	inputs.queryPlans[ref] = facts
}

func (inputs *compiledPlanInputs) merge(source compiledPlanInputs) {
	for _, requirement := range source.requirements {
		inputs.addRequirement(requirement)
	}
	for ref, facts := range source.queryPlans {
		inputs.addQuery(ref, facts)
	}
}

func decodeLegacyRecovery(raw json.RawMessage) (legacyRecovery, bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "{}" {
		return legacyRecovery{}, false, nil
	}
	var recovery legacyRecovery
	if err := json.Unmarshal(raw, &recovery); err != nil {
		return legacyRecovery{}, false, err
	}
	return recovery, true, nil
}

func hasJSONValue(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null" && value != "0" && value != `""`
}

func isAlwaysActiveUptime(raw json.RawMessage) bool {
	if !hasJSONValue(raw) {
		return true
	}
	var uptime struct {
		Calendars       []json.RawMessage             `json:"calendars"`
		ActiveCalendars []json.RawMessage             `json:"active_calendars"`
		TimeRanges      []struct{ Start, End string } `json:"time_ranges"`
	}
	if json.Unmarshal(raw, &uptime) != nil || len(uptime.Calendars) > 0 || len(uptime.ActiveCalendars) > 0 {
		return false
	}
	if len(uptime.TimeRanges) == 0 {
		return true
	}
	return len(uptime.TimeRanges) == 1 && ((uptime.TimeRanges[0].Start == "00:00" && uptime.TimeRanges[0].End == "23:59") ||
		(uptime.TimeRanges[0].Start == "00:00:00" && uptime.TimeRanges[0].End == "23:59:59"))
}

func itemInterval(item legacyItem) (int64, error) {
	if len(item.QueryConfigs) == 0 {
		return 0, errors.New("alarmd controlplane: invalid query config")
	}
	var minimum int64
	for _, raw := range item.QueryConfigs {
		var query struct {
			AggInterval json.Number `json:"agg_interval"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if decoder.Decode(&query) != nil {
			return 0, errors.New("alarmd controlplane: invalid query config")
		}
		interval, err := strconv.ParseInt(query.AggInterval.String(), 10, 64)
		if err != nil || interval <= 0 {
			return 0, errors.New("alarmd controlplane: positive aggregation interval is required")
		}
		if minimum == 0 || interval < minimum {
			minimum = interval
		}
	}
	return minimum, nil
}

func thresholdConfig(raw legacyAlgorithm, unit string) (json.RawMessage, error) {
	type condition struct {
		Method    string      `json:"method"`
		Threshold json.Number `json:"threshold"`
	}
	var groups [][]condition
	decoder := json.NewDecoder(strings.NewReader(string(raw.Config)))
	decoder.UseNumber()
	if err := decoder.Decode(&groups); err != nil || len(groups) == 0 {
		var single []condition
		decoder = json.NewDecoder(strings.NewReader(string(raw.Config)))
		decoder.UseNumber()
		if err := decoder.Decode(&single); err != nil || len(single) == 0 {
			return nil, errors.New("alarmd controlplane: invalid Threshold config")
		}
		groups = [][]condition{single}
	}
	wireGroups := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		conditions := make([]map[string]any, 0, len(group))
		for _, condition := range group {
			operator := map[string]string{"gt": "GT", "gte": "GTE", "eq": "EQ", "neq": "NEQ", "lt": "LT", "lte": "LTE"}[strings.ToLower(condition.Method)]
			if operator == "" || condition.Threshold.String() == "" {
				return nil, errors.New("alarmd controlplane: invalid Threshold condition")
			}
			conditions = append(conditions, map[string]any{"operator": operator, "threshold_decimal": condition.Threshold.String()})
		}
		wireGroups = append(wireGroups, map[string]any{"conditions": conditions})
	}
	return json.Marshal(map[string]any{"value_field": "value", "data_unit": unit, "threshold_unit_prefix": raw.UnitPrefix, "precision": map[string]any{"decimal_places": 6, "rounding": "HALF_EVEN"}, "groups": wireGroups})
}
