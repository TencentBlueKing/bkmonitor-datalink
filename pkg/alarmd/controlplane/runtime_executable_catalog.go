package controlplane

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

const runtimeCatalogClosureInvalidReason = "RUNTIME_CATALOG_CLOSURE_INVALID"

var errRuntimeCatalogClosureInvalid = errors.New("alarmd controlplane: runtime Catalog dependency closure is invalid")

// retainRuntimeExecutableCatalog applies the Evaluation Core compiler before
// publication. Deterministic Plan and Level terminals remain source-audit
// facts; only terminal-free Plans enter the immutable scheduling Catalog.
// observeRetention sums one accepted Plan's retained window into the
// Catalog's measurement.
//
// Taken here because this is the one place the compiled Level is in hand for
// every Plan the deployment runs: the composition downstream sees the frozen
// strategy document, and deriving the window from it a second time would put
// the same relation in two places with nothing comparing them.
//
// The no-data Level counts with the rest. Its facts are written on the same
// records, so its retention is retention the store pays for.
func observeRetention(retention *CatalogRetention, compiled *strategy.CompiledPlan) {
	levels := compiled.Levels()
	if noData := compiled.NoDataLevel(); noData != nil {
		levels = append(append([]strategy.CompiledLevel(nil), levels...), *noData)
	}
	for _, level := range levels {
		requirement := level.StateRequirement()
		retention.RequiredPoints += uint64(requirement.RequiredDetectHistoryPoints)
		retention.RetentionPoints += uint64(requirement.RetentionPoints)
		if requirement.RetentionPoints <= requirement.RequiredDetectHistoryPoints {
			continue
		}
		retention.LevelsWithSlack++
		// Which term of the required window is the larger one. The slack's
		// leading term is the trigger window, so a Level whose window
		// dominates pays far more for the same required size than one whose
		// recovery run does.
		if level.Trigger().WindowSize > level.Recovery().ConsecutiveWindows {
			retention.LevelsWithSlackWindowDominant++
			continue
		}
		retention.LevelsWithSlackRecoveryDominant++
	}
}

func retainRuntimeExecutableCatalog(
	ctx context.Context,
	catalog Catalog,
	lastGood *PublishedSnapshot,
	compiler RuntimePlanCompiler,
	stateSemantics strategy.StateSemantics,
) (Catalog, error) {
	if compiler == nil || !validStateSemantics(stateSemantics) {
		return Catalog{}, errors.New("alarmd controlplane: invalid runtime executable Catalog compiler")
	}
	result := catalog
	result.QueryGroups = make([]QueryGroup, 0, len(catalog.QueryGroups))
	result.Dispositions = append([]ObjectDisposition(nil), catalog.Dispositions...)
	groups := make(map[execution.QueryGroupIdentity]*QueryGroup, len(catalog.QueryGroups))
	seenPlans := make(map[execution.PlanKey]struct{})
	lastGoodPlans := indexLastGoodPlans(lastGood)
	rejectClosure := func(sourceID string, dispositions ...ObjectDisposition) {
		result.Dispositions = append(result.Dispositions, dispositions...)
		result.Dispositions = withoutAcceptedPlanDisposition(result.Dispositions, sourceID)
		result.Dispositions = append(result.Dispositions, ObjectDisposition{
			SourceID: sourceID, Scope: "PLAN", Disposition: DispositionConfigRejected, Reason: runtimeCatalogClosureInvalidReason,
		})
	}
	addPlan := func(facts execution.QueryPlanFacts, plan FrozenPlan, compiled *strategy.CompiledPlan) error {
		if compiled == nil || compiled.StateCompatibilityHash() == "" {
			return errors.New("alarmd controlplane: runtime executable Plan has no state compatibility")
		}
		plan.StateGeneration = execution.StateGeneration(compiled.StateCompatibilityHash())
		// The refs from the same compilation as the generation, so the key
		// and the contract a Worker holds a record to were derived by one
		// build.
		refs, err := execution.DeriveRuntimeLevelContractRefs(compiled)
		if err != nil {
			return err
		}
		plan.LevelContractRefs = refs
		plan.NoDataLevelContractRefs = nil
		if view := compiled.NoDataView(); view != nil {
			if plan.NoDataLevelContractRefs, err = execution.DeriveRuntimeLevelContractRefs(view); err != nil {
				return err
			}
		}
		observeRetention(&result.Retention, compiled)
		if _, duplicate := seenPlans[plan.Key()]; duplicate {
			return errors.New("alarmd controlplane: duplicate runtime executable Plan identity")
		}
		seenPlans[plan.Key()] = struct{}{}
		identity, err := deriveQueryGroupIdentity(facts)
		if err != nil {
			return err
		}
		group := groups[identity]
		if group == nil {
			group = &QueryGroup{Identity: identity, QueryPlan: facts}
			groups[identity] = group
		} else if group.QueryPlan.QueryRevision != facts.QueryRevision {
			// The same assertion as in BuildCatalog, on the executable part
			// of the Catalog, and named the same way for the same reason.
			return fmt.Errorf("alarmd controlplane: runtime executable Query Group has conflicting query "+
				"revisions: group %s holds %s and %s", identity, group.QueryPlan.QueryRevision, facts.QueryRevision)
		}
		group.Plans = append(group.Plans, plan)
		return nil
	}
	for _, sourceGroup := range catalog.QueryGroups {
		for _, sourcePlan := range sourceGroup.Plans {
			compiledResult, err := compiler.Compile(ctx, strategy.CompileRequest{
				Plan: sourcePlan.Plan, DatasetContract: sourceGroup.QueryPlan.Normalization.DatasetContract,
				StateSemantics: stateSemantics,
			})
			if err != nil {
				return Catalog{}, err
			}
			if terminal := compiledResult.PlanTerminal(); terminal != nil {
				disposition := terminalDisposition(sourcePlan.Identity.StrategyID, "PLAN", *terminal)
				result.Dispositions = withoutAcceptedPlanDisposition(result.Dispositions, sourcePlan.Identity.StrategyID)
				if RetainsLastGoodDefinition(disposition.Disposition) {
					if entry, ok := lastGoodPlans[sourcePlan.Identity.StrategyID]; ok {
						lastGoodCompiled, executable, err := runtimePlanIsTerminalFree(ctx, entry, compiler, stateSemantics)
						if err != nil {
							return Catalog{}, err
						}
						if executable {
							if err := validateRuntimePlanDependencyClosure(entry.plan, lastGoodCompiled); err != nil {
								if errors.Is(err, errRuntimeCatalogClosureInvalid) {
									rejectClosure(sourcePlan.Identity.StrategyID, disposition)
									continue
								}
								return Catalog{}, err
							}
							disposition.Disposition = DispositionStaleConfig
							result.Dispositions = append(result.Dispositions, disposition)
							if err := addPlan(entry.facts, entry.plan, lastGoodCompiled); err != nil {
								return Catalog{}, err
							}
							continue
						}
					}
				}
				result.Dispositions = append(result.Dispositions, disposition)
				continue
			}
			compiled, ok := compiledResult.Plan()
			if !ok {
				return Catalog{}, errors.New("alarmd controlplane: runtime compiler returned neither Plan nor terminal")
			}
			levelTerminals := compiledResult.LevelTerminals()
			terminalDispositions := make([]ObjectDisposition, 0, len(levelTerminals))
			// Any refusal that keeps the last good definition, not only a
			// config one: the Levels this round could not compile are
			// supplemented from the definition that did.
			retainsLastGood := false
			for _, terminal := range levelTerminals {
				disposition := terminalDisposition(sourcePlan.Identity.StrategyID, "LEVEL", terminal)
				terminalDispositions = append(terminalDispositions, disposition)
				retainsLastGood = retainsLastGood || RetainsLastGoodDefinition(disposition.Disposition)
			}
			plan, err := retainCompiledLevels(sourcePlan, compiled)
			if err != nil {
				if errors.Is(err, errRuntimeCatalogClosureInvalid) {
					rejectClosure(sourcePlan.Identity.StrategyID)
					continue
				}
				return Catalog{}, err
			}
			supplementedLevels := map[uint32]struct{}{}
			if retainsLastGood {
				if entry, ok := lastGoodPlans[sourcePlan.Identity.StrategyID]; ok {
					_, executable, err := runtimePlanIsTerminalFree(ctx, entry, compiler, stateSemantics)
					if err != nil {
						return Catalog{}, err
					}
					if executable {
						plan, supplementedLevels, err = supplementRejectedLevels(plan, entry.plan, terminalDispositions)
						if err != nil {
							if errors.Is(err, errRuntimeCatalogClosureInvalid) {
								rejectClosure(sourcePlan.Identity.StrategyID, terminalDispositions...)
								continue
							}
							return Catalog{}, err
						}
					}
				}
			}
			if len(plan.Plan.StrategyIR.Levels) == 0 {
				result.Dispositions = append(result.Dispositions, terminalDispositions...)
				result.Dispositions = withoutAcceptedPlanDisposition(result.Dispositions, sourcePlan.Identity.StrategyID)
				continue
			}
			if len(supplementedLevels) > 0 {
				verification, err := compiler.Compile(ctx, strategy.CompileRequest{
					Plan: plan.Plan, DatasetContract: sourceGroup.QueryPlan.Normalization.DatasetContract,
					StateSemantics: stateSemantics,
				})
				if err != nil {
					return Catalog{}, err
				}
				verifiedPlan, ok := verification.Plan()
				if verification.PlanTerminal() == nil && len(verification.LevelTerminals()) == 0 && ok && len(verifiedPlan.Levels()) > 0 {
					if err := validateRuntimePlanDependencyClosure(plan, verifiedPlan); err != nil {
						if errors.Is(err, errRuntimeCatalogClosureInvalid) {
							rejectClosure(sourcePlan.Identity.StrategyID, terminalDispositions...)
							continue
						}
						return Catalog{}, err
					}
					markSupplementedLevelDispositionsStale(terminalDispositions, supplementedLevels)
					result.Dispositions = append(result.Dispositions, terminalDispositions...)
					if err := addPlan(sourceGroup.QueryPlan, plan, verifiedPlan); err != nil {
						return Catalog{}, err
					}
					continue
				}
				verificationDispositions, err := compileResultDispositions(sourcePlan.Identity.StrategyID, verification)
				if err != nil {
					return Catalog{}, err
				}
				result.Dispositions = append(result.Dispositions, terminalDispositions...)
				result.Dispositions = append(result.Dispositions, verificationDispositions...)
				result.Dispositions = withoutAcceptedPlanDisposition(result.Dispositions, sourcePlan.Identity.StrategyID)
				continue
			}
			result.Dispositions = append(result.Dispositions, terminalDispositions...)
			if err := addPlan(sourceGroup.QueryPlan, plan, compiled); err != nil {
				return Catalog{}, err
			}
		}
	}
	for _, group := range groups {
		sort.Slice(group.Plans, func(i, j int) bool { return lessPlanIdentity(group.Plans[i].Identity, group.Plans[j].Identity) })
		if err := refreshQueryGroupDigests(group); err != nil {
			return Catalog{}, err
		}
		result.QueryGroups = append(result.QueryGroups, *group)
	}
	sort.Slice(result.QueryGroups, func(i, j int) bool { return result.QueryGroups[i].Identity < result.QueryGroups[j].Identity })
	sort.Slice(result.Dispositions, func(i, j int) bool { return lessDisposition(result.Dispositions[i], result.Dispositions[j]) })
	revision, err := deriveSnapshotRevision(result.QueryGroups)
	if err != nil {
		return Catalog{}, err
	}
	result.SnapshotRevision = revision
	return result, nil
}

func runtimePlanIsTerminalFree(
	ctx context.Context,
	entry lastGoodPlan,
	compiler RuntimePlanCompiler,
	stateSemantics strategy.StateSemantics,
) (*strategy.CompiledPlan, bool, error) {
	return runtimeFrozenPlanIsTerminalFree(
		ctx, entry.plan, entry.facts.Normalization.DatasetContract, compiler, stateSemantics,
	)
}

func runtimeFrozenPlanIsTerminalFree(
	ctx context.Context,
	plan FrozenPlan,
	datasetContract contract.DatasetContractV2,
	compiler RuntimePlanCompiler,
	stateSemantics strategy.StateSemantics,
) (*strategy.CompiledPlan, bool, error) {
	result, err := compiler.Compile(ctx, strategy.CompileRequest{
		Plan: plan.Plan, DatasetContract: datasetContract, StateSemantics: stateSemantics,
	})
	if err != nil {
		return nil, false, err
	}
	compiled, ok := result.Plan()
	executable := ok && result.PlanTerminal() == nil && len(result.LevelTerminals()) == 0 && len(compiled.Levels()) > 0
	return compiled, executable, nil
}

func retainCompiledLevels(plan FrozenPlan, compiled *strategy.CompiledPlan) (FrozenPlan, error) {
	retained := make(map[uint32]struct{}, len(compiled.Levels()))
	for _, level := range compiled.Levels() {
		retained[level.Definition().LevelID] = struct{}{}
	}
	levels := make([]contract.LevelIRV2, 0, len(retained))
	for _, level := range plan.Plan.StrategyIR.Levels {
		if _, ok := retained[level.Definition.LevelID]; ok {
			levels = append(levels, level)
		}
	}
	plan.Plan.StrategyIR.Levels = levels
	requirements := make([]execution.DataRequirementTemplate, 0, len(plan.RequirementTemplates))
	for _, requirement := range plan.RequirementTemplates {
		if _, ok := retained[requirement.ConsumerLevelID]; ok {
			requirements = append(requirements, requirement)
		}
	}
	queryPlans, err := retainRequiredQueryPlans(plan.QueryPlans, requirements)
	if err != nil {
		return FrozenPlan{}, err
	}
	plan.RequirementTemplates = requirements
	plan.QueryPlans = queryPlans
	revision, err := contract.DeriveCanonicalDigestV2("alarmd-plan-semantics-v1", plan.Plan)
	if err != nil {
		return FrozenPlan{}, err
	}
	plan.PlanRevision = revision
	if err := validateRuntimePlanDependencyClosure(plan, compiled); err != nil {
		return FrozenPlan{}, err
	}
	return plan, nil
}

func supplementRejectedLevels(
	plan FrozenPlan,
	lastGood FrozenPlan,
	dispositions []ObjectDisposition,
) (FrozenPlan, map[uint32]struct{}, error) {
	lastGoodLevels := make(map[uint32]contract.LevelIRV2, len(lastGood.Plan.StrategyIR.Levels))
	for _, level := range lastGood.Plan.StrategyIR.Levels {
		lastGoodLevels[level.Definition.LevelID] = level
	}
	retained := make(map[uint32]struct{}, len(plan.Plan.StrategyIR.Levels))
	for _, level := range plan.Plan.StrategyIR.Levels {
		retained[level.Definition.LevelID] = struct{}{}
	}
	supplemented := make(map[uint32]struct{})
	for index := range dispositions {
		disposition := &dispositions[index]
		if disposition.Disposition != DispositionConfigRejected {
			continue
		}
		level, ok := lastGoodLevels[disposition.LevelID]
		if !ok {
			continue
		}
		if _, duplicate := retained[disposition.LevelID]; duplicate {
			return FrozenPlan{}, nil, errors.New("alarmd controlplane: rejected Level is already runtime executable")
		}
		plan.Plan.StrategyIR.Levels = append(plan.Plan.StrategyIR.Levels, level)
		retained[disposition.LevelID] = struct{}{}
		supplemented[disposition.LevelID] = struct{}{}
	}
	sort.Slice(plan.Plan.StrategyIR.Levels, func(i, j int) bool {
		return plan.Plan.StrategyIR.Levels[i].Definition.LevelID < plan.Plan.StrategyIR.Levels[j].Definition.LevelID
	})
	if len(supplemented) > 0 {
		var err error
		plan.RequirementTemplates, plan.QueryPlans, err = supplementLevelDependencies(plan, lastGood, supplemented)
		if err != nil {
			return FrozenPlan{}, nil, err
		}
	}
	revision, err := contract.DeriveCanonicalDigestV2("alarmd-plan-semantics-v1", plan.Plan)
	if err != nil {
		return FrozenPlan{}, nil, err
	}
	plan.PlanRevision = revision
	return plan, supplemented, nil
}

func retainRequiredQueryPlans(
	source map[execution.LogicalQueryRef]execution.QueryPlanFacts,
	requirements []execution.DataRequirementTemplate,
) (map[execution.LogicalQueryRef]execution.QueryPlanFacts, error) {
	if len(requirements) == 0 {
		return nil, nil
	}
	retained := make(map[execution.LogicalQueryRef]execution.QueryPlanFacts)
	for _, requirement := range requirements {
		facts, ok := source[requirement.LogicalQueryRef]
		if !ok {
			return nil, errRuntimeCatalogClosureInvalid
		}
		retained[requirement.LogicalQueryRef] = facts
	}
	return retained, nil
}

func supplementLevelDependencies(
	plan FrozenPlan,
	lastGood FrozenPlan,
	supplemented map[uint32]struct{},
) ([]execution.DataRequirementTemplate, map[execution.LogicalQueryRef]execution.QueryPlanFacts, error) {
	currentByLevel := make(map[uint32][]execution.DataRequirementTemplate)
	for _, requirement := range plan.RequirementTemplates {
		currentByLevel[requirement.ConsumerLevelID] = append(currentByLevel[requirement.ConsumerLevelID], requirement)
	}
	lastGoodByLevel := make(map[uint32][]execution.DataRequirementTemplate)
	for _, requirement := range lastGood.RequirementTemplates {
		lastGoodByLevel[requirement.ConsumerLevelID] = append(lastGoodByLevel[requirement.ConsumerLevelID], requirement)
	}

	requirements := make([]execution.DataRequirementTemplate, 0, len(plan.RequirementTemplates)+len(lastGood.RequirementTemplates))
	queryPlans := make(map[execution.LogicalQueryRef]execution.QueryPlanFacts)
	for _, level := range plan.Plan.StrategyIR.Levels {
		levelID := level.Definition.LevelID
		if _, ok := supplemented[levelID]; ok {
			for _, requirement := range lastGoodByLevel[levelID] {
				facts, exists := lastGood.QueryPlans[requirement.LogicalQueryRef]
				if !exists {
					return nil, nil, errRuntimeCatalogClosureInvalid
				}
				requirements = append(requirements, requirement)
				if _, shared := queryPlans[requirement.LogicalQueryRef]; !shared {
					queryPlans[requirement.LogicalQueryRef] = facts
				}
			}
			continue
		}
		for _, requirement := range currentByLevel[levelID] {
			facts, exists := plan.QueryPlans[requirement.LogicalQueryRef]
			if !exists {
				return nil, nil, errRuntimeCatalogClosureInvalid
			}
			requirements = append(requirements, requirement)
			queryPlans[requirement.LogicalQueryRef] = facts
		}
	}
	if len(queryPlans) == 0 {
		queryPlans = nil
	}
	return requirements, queryPlans, nil
}

type runtimeRequirementKey struct {
	requirementID   execution.RequirementID
	consumerLevelID uint32
}

func validateRuntimePlanDependencyClosure(plan FrozenPlan, compiled *strategy.CompiledPlan) error {
	if compiled == nil {
		return errRuntimeCatalogClosureInvalid
	}
	expected := make(map[runtimeRequirementKey]execution.DataRequirementTemplate)
	for _, level := range compiled.Levels() {
		for _, algorithm := range level.Algorithms() {
			for _, requirement := range algorithm.InputRequirements() {
				template, err := runtimeRequirementTemplate(requirement)
				if err != nil {
					return errRuntimeCatalogClosureInvalid
				}
				key := runtimeRequirementKey{requirementID: template.RequirementID, consumerLevelID: template.ConsumerLevelID}
				if existing, duplicate := expected[key]; duplicate && !reflect.DeepEqual(existing, template) {
					return errRuntimeCatalogClosureInvalid
				}
				expected[key] = template
			}
		}
	}

	actual := make(map[runtimeRequirementKey]execution.DataRequirementTemplate, len(plan.RequirementTemplates))
	referencedQueries := make(map[execution.LogicalQueryRef]struct{})
	for _, template := range plan.RequirementTemplates {
		candidate := template
		candidate.RequirementID = ""
		rebuilt, err := execution.BuildDataRequirementTemplate(candidate)
		if err != nil || !reflect.DeepEqual(rebuilt, template) {
			return errRuntimeCatalogClosureInvalid
		}
		key := runtimeRequirementKey{requirementID: template.RequirementID, consumerLevelID: template.ConsumerLevelID}
		expectedTemplate, ok := expected[key]
		if !ok || !reflect.DeepEqual(expectedTemplate, template) {
			return errRuntimeCatalogClosureInvalid
		}
		if _, duplicate := actual[key]; duplicate {
			return errRuntimeCatalogClosureInvalid
		}
		actual[key] = template
		referencedQueries[template.LogicalQueryRef] = struct{}{}
	}
	if len(actual) != len(expected) || len(plan.QueryPlans) != len(referencedQueries) {
		return errRuntimeCatalogClosureInvalid
	}
	for ref, facts := range plan.QueryPlans {
		if _, ok := referencedQueries[ref]; !ok || ref != execution.LogicalQueryRef(facts.QueryRevision) || facts.Validate() != nil {
			return errRuntimeCatalogClosureInvalid
		}
	}
	return nil
}

func runtimeRequirementTemplate(requirement strategy.AlgorithmInputRequirement) (execution.DataRequirementTemplate, error) {
	namedPoints := make([]execution.NamedInputPoint, len(requirement.NamedPoints))
	for index, point := range requirement.NamedPoints {
		namedPoints[index] = execution.NamedInputPoint{Name: point.Name, OffsetSeconds: point.OffsetSeconds}
	}
	template, err := execution.BuildDataRequirementTemplate(execution.DataRequirementTemplate{
		DatasetName: execution.DatasetName(requirement.DatasetName), Role: execution.InputRole(requirement.Role),
		ConsumerLevelID: requirement.ConsumerLevelID, LogicalQueryRef: execution.LogicalQueryRef(requirement.LogicalQueryRef),
		RelativeWindow: execution.RelativeQueryWindow{
			StartOffsetSeconds: requirement.RelativeWindow.StartOffsetSeconds,
			EndOffsetSeconds:   requirement.RelativeWindow.EndOffsetSeconds,
			HalfOpen:           requirement.RelativeWindow.HalfOpen,
		},
		StepMillis: requirement.StepMillis, AlignmentMillis: requirement.AlignmentMillis,
		ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessClass(requirement.ReadinessClass),
		InputProjection: execution.InputProjection{
			ValueFields:     append([]string(nil), requirement.InputProjection.ValueFields...),
			DimensionFields: append([]string(nil), requirement.InputProjection.DimensionFields...),
			IdentityFields:  append([]string(nil), requirement.InputProjection.IdentityFields...),
		},
		PointOffsetsSeconds: append([]int64(nil), requirement.PointOffsetsSeconds...), NamedPoints: namedPoints,
	})
	if err != nil || string(template.RequirementID) != requirement.RequirementID {
		return execution.DataRequirementTemplate{}, errRuntimeCatalogClosureInvalid
	}
	return template, nil
}

func compileResultDispositions(sourceID string, result strategy.CompileResult) ([]ObjectDisposition, error) {
	if terminal := result.PlanTerminal(); terminal != nil {
		return []ObjectDisposition{terminalDisposition(sourceID, "PLAN", *terminal)}, nil
	}
	terminals := result.LevelTerminals()
	if len(terminals) == 0 {
		return nil, errors.New("alarmd controlplane: runtime compiler returned neither Plan nor terminal")
	}
	dispositions := make([]ObjectDisposition, 0, len(terminals))
	for _, terminal := range terminals {
		dispositions = append(dispositions, terminalDisposition(sourceID, "LEVEL", terminal))
	}
	return dispositions, nil
}

func markSupplementedLevelDispositionsStale(
	dispositions []ObjectDisposition,
	supplemented map[uint32]struct{},
) {
	for index := range dispositions {
		if dispositions[index].Disposition != DispositionConfigRejected {
			continue
		}
		if _, ok := supplemented[dispositions[index].LevelID]; ok {
			dispositions[index].Disposition = DispositionStaleConfig
		}
	}
}

func refreshQueryGroupDigests(group *QueryGroup) error {
	identities := make([]execution.PlanIdentity, len(group.Plans))
	schedules := make([]execution.FrozenPlanSchedule, len(group.Plans))
	for index, plan := range group.Plans {
		identities[index] = plan.Identity
		schedules[index] = execution.FrozenPlanSchedule{
			Identity: plan.Identity, ScheduleRevision: plan.ScheduleRevision, Spec: plan.ScheduleSpec,
		}
	}
	membership, err := contract.DeriveCanonicalDigestV2("alarmd-query-group-membership-v1", identities)
	if err != nil {
		return err
	}
	schedule, err := execution.DeriveQueryGroupScheduleRevision(schedules)
	if err != nil {
		return err
	}
	group.MembershipDigest = membership
	group.ScheduleRevision = schedule
	return nil
}

// CompilerTerminalDisposition is how the catalog files a compiler terminal: the
// codes that mean "this definition cannot run as written in this build" and the
// codes that mean "this definition is wrong". Exported as the one place that
// grouping is written down, so that the fleet page's own reading of the same
// codes -- who has to act on an object carrying one -- can be checked against
// it by a test that walks the whole reason catalogue, rather than by a list
// somebody keeps in step by hand.
//
// ok is false for a code the compiler does not produce as a terminal.
func CompilerTerminalDisposition(reasonCode string) (Disposition, bool) {
	switch reasonCode {
	case contract.ReasonAlgorithmUnsupported, contract.ReasonPlanBudgetExceeded, contract.ReasonLevelBudgetExceeded,
		strategy.ReasonEffectiveTimeSchemaUnsupported:
		return DispositionUnsupported, true
	case contract.ReasonPlanInvalid, contract.ReasonPlanDuplicateLevelID, contract.ReasonProjectionInvalid, contract.ReasonLevelInvalid,
		contract.ReasonNoDataPlanUncompilable,
		strategy.ReasonEffectiveTimeInvalid, strategy.ReasonEffectiveTimeSnapshotInvalid,
		strategy.ReasonEffectiveTimeSnapshotStatusInvalid, strategy.ReasonEffectiveTimeCalendarIdentity,
		strategy.ReasonEffectiveTimeCalendarDuplicate, strategy.ReasonEffectiveTimeCalendarItemsMissing:
		return DispositionConfigRejected, true
	case strategy.ReasonEffectiveTimeSnapshotUnavailable, strategy.ReasonEffectiveTimeCalendarsMissing,
		strategy.ReasonEffectiveTimeCalendarNotPresent, strategy.ReasonEffectiveTimeCalendarMissing:
		// The strategy names a calendar the snapshot did not carry, or the
		// snapshot did not arrive. The definition is not wrong and this build
		// is not lacking anything: what is missing is a piece of the source,
		// which is the one disposition that says so.
		return DispositionSourceIncomplete, true
	default:
		return "", false
	}
}

// ReasonCompilerTerminalUnclassified files a terminal this build's table has no
// entry for. It carries the compiler's own reason code and field path so the
// strategy can still be named, which is the half that mattered: the code this
// replaces failed the whole refresh and dropped all three.
const ReasonCompilerTerminalUnclassified = contract.ReasonCompilerTerminalUnclassified

func terminalDisposition(sourceID, scope string, terminal strategy.Terminal) ObjectDisposition {
	disposition, known := CompilerTerminalDisposition(terminal.ReasonCode)
	if !known {
		// One strategy's compile refusal is one strategy's refusal. Failing
		// the round here is what a live deployment met: a reason the compiler
		// had gained and the table had not took every config refresh with it,
		// fifty-three rounds with none published, every strategy left on a
		// catalog from before - and the error carried neither the strategy nor
		// the reason, so nothing could say which definition to look at.
		//
		// The unknown code travels in the detail rather than the disposition,
		// because a code this build cannot classify is exactly the one a
		// reader needs to see verbatim.
		return ObjectDisposition{SourceID: sourceID, Scope: scope, LevelID: terminal.LevelID,
			Disposition: DispositionConfigRejected, Reason: ReasonCompilerTerminalUnclassified,
			FieldPath: terminal.FieldPath, Detail: dispositionDetail(terminal.ReasonCode)}
	}
	return ObjectDisposition{SourceID: sourceID, Scope: scope, LevelID: terminal.LevelID,
		Disposition: disposition, Reason: terminal.ReasonCode, FieldPath: terminal.FieldPath}
}

func withoutAcceptedPlanDisposition(dispositions []ObjectDisposition, sourceID string) []ObjectDisposition {
	result := dispositions[:0]
	for _, disposition := range dispositions {
		if disposition.SourceID == sourceID && disposition.Scope == "PLAN" && disposition.Disposition == DispositionAccepted {
			continue
		}
		result = append(result, disposition)
	}
	return result
}
