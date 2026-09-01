package controlplane

import (
	"context"
	"errors"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// retainRuntimeExecutableCatalog applies the Evaluation Core compiler before
// publication. Deterministic Plan and Level terminals remain source-audit
// facts; only terminal-free Plans enter the immutable scheduling Catalog.
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
	seenPlans := make(map[execution.PlanIdentity]struct{})
	lastGoodPlans := indexLastGoodPlans(lastGood)
	addPlan := func(facts execution.QueryPlanFacts, plan FrozenPlan) error {
		if _, duplicate := seenPlans[plan.Identity]; duplicate {
			return errors.New("alarmd controlplane: duplicate runtime executable Plan identity")
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
			return errors.New("alarmd controlplane: runtime executable Query Group has conflicting query revisions")
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
				disposition, err := terminalDisposition(sourcePlan.Identity.StrategyID, "PLAN", *terminal)
				if err != nil {
					return Catalog{}, err
				}
				result.Dispositions = withoutAcceptedPlanDisposition(result.Dispositions, sourcePlan.Identity.StrategyID)
				if disposition.Disposition == DispositionConfigRejected {
					if entry, ok := lastGoodPlans[sourcePlan.Identity.StrategyID]; ok {
						executable, err := runtimePlanIsTerminalFree(ctx, entry, compiler, stateSemantics)
						if err != nil {
							return Catalog{}, err
						}
						if executable {
							disposition.Disposition = DispositionStaleConfig
							result.Dispositions = append(result.Dispositions, disposition)
							if err := addPlan(entry.facts, entry.plan); err != nil {
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
			hasConfigRejected := false
			for _, terminal := range levelTerminals {
				disposition, err := terminalDisposition(sourcePlan.Identity.StrategyID, "LEVEL", terminal)
				if err != nil {
					return Catalog{}, err
				}
				terminalDispositions = append(terminalDispositions, disposition)
				hasConfigRejected = hasConfigRejected || disposition.Disposition == DispositionConfigRejected
			}
			plan, err := retainCompiledLevels(sourcePlan, compiled)
			if err != nil {
				return Catalog{}, err
			}
			supplementedLevels := map[uint32]struct{}{}
			if hasConfigRejected {
				if entry, ok := lastGoodPlans[sourcePlan.Identity.StrategyID]; ok {
					executable, err := runtimePlanIsTerminalFree(ctx, entry, compiler, stateSemantics)
					if err != nil {
						return Catalog{}, err
					}
					if executable {
						plan, supplementedLevels, err = supplementRejectedLevels(plan, entry.plan, terminalDispositions)
						if err != nil {
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
					markSupplementedLevelDispositionsStale(terminalDispositions, supplementedLevels)
					result.Dispositions = append(result.Dispositions, terminalDispositions...)
					if err := addPlan(sourceGroup.QueryPlan, plan); err != nil {
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
			if err := addPlan(sourceGroup.QueryPlan, plan); err != nil {
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
) (bool, error) {
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
) (bool, error) {
	result, err := compiler.Compile(ctx, strategy.CompileRequest{
		Plan: plan.Plan, DatasetContract: datasetContract, StateSemantics: stateSemantics,
	})
	if err != nil {
		return false, err
	}
	compiled, ok := result.Plan()
	return ok && result.PlanTerminal() == nil && len(result.LevelTerminals()) == 0 && len(compiled.Levels()) > 0, nil
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
	revision, err := contract.DeriveCanonicalDigestV2("alarmd-plan-semantics-v1", plan.Plan)
	if err != nil {
		return FrozenPlan{}, err
	}
	plan.PlanRevision = revision
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
	revision, err := contract.DeriveCanonicalDigestV2("alarmd-plan-semantics-v1", plan.Plan)
	if err != nil {
		return FrozenPlan{}, nil, err
	}
	plan.PlanRevision = revision
	return plan, supplemented, nil
}

func compileResultDispositions(sourceID string, result strategy.CompileResult) ([]ObjectDisposition, error) {
	if terminal := result.PlanTerminal(); terminal != nil {
		disposition, err := terminalDisposition(sourceID, "PLAN", *terminal)
		if err != nil {
			return nil, err
		}
		return []ObjectDisposition{disposition}, nil
	}
	terminals := result.LevelTerminals()
	if len(terminals) == 0 {
		return nil, errors.New("alarmd controlplane: runtime compiler returned neither Plan nor terminal")
	}
	dispositions := make([]ObjectDisposition, 0, len(terminals))
	for _, terminal := range terminals {
		disposition, err := terminalDisposition(sourceID, "LEVEL", terminal)
		if err != nil {
			return nil, err
		}
		dispositions = append(dispositions, disposition)
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

func terminalDisposition(sourceID, scope string, terminal strategy.Terminal) (ObjectDisposition, error) {
	var disposition Disposition
	switch terminal.ReasonCode {
	case contract.ReasonAlgorithmUnsupported, contract.ReasonPlanBudgetExceeded, contract.ReasonLevelBudgetExceeded:
		disposition = DispositionUnsupported
	case contract.ReasonPlanInvalid, contract.ReasonPlanDuplicateLevelID, contract.ReasonProjectionInvalid, contract.ReasonLevelInvalid:
		disposition = DispositionConfigRejected
	default:
		return ObjectDisposition{}, errors.New("alarmd controlplane: runtime compiler returned an unclassified terminal reason")
	}
	return ObjectDisposition{SourceID: sourceID, Scope: scope, LevelID: terminal.LevelID,
		Disposition: disposition, Reason: terminal.ReasonCode}, nil
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
