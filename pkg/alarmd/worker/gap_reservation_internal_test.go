package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type reservationGapStore struct {
	reads       int
	afterAccept func()
}

func (*reservationGapStore) LoadGaps(context.Context, execution.GapLoadRequest) (execution.GapLoadResult, error) {
	return execution.GapLoadResult{}, errors.New("unexpected whole-result load")
}
func (store *reservationGapStore) LoadGapsInto(ctx context.Context, request execution.GapLoadRequest, accept func(execution.GapGuardSnapshot) error) error {
	for _, item := range request.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		store.reads++
		if err := accept(execution.GapGuardSnapshot{Identity: item.Identity, Status: execution.GapMissing}); err != nil {
			return err
		}
		if store.afterAccept != nil {
			store.afterAccept()
		}
	}
	return nil
}
func (*reservationGapStore) ApplyGap(context.Context, execution.GapGuardApplyRequest) (execution.GapGuardApplyResult, error) {
	return execution.GapGuardApplyResult{}, errors.New("unexpected gap write")
}

func gapReservationFixture() (*SlotExecutionCoordinator, *reservationGapStore, execution.GapLoadRequest) {
	store := &reservationGapStore{}
	coordinator := &SlotExecutionCoordinator{budget: sideEffectTestBudget("gap"), ports: Ports{GapGuard: store,
		Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {})}}
	request := execution.GapLoadRequest{Items: []execution.PlanGapLoadItem{{Identity: execution.PlanGapIdentity{
		Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "1", StrategyID: "1"}, StateGeneration: "generation"}}}}
	return coordinator, store, request
}

func TestNormalAndQueryFreeGapFactOwnersShareBudgetWithoutMutation(t *testing.T) {
	coordinator, _, request := gapReservationFixture()
	normal := &streamedExecution{coordinator: coordinator, request: execution.SlotExecutionRequest{Operation: execution.OperationNormal}}
	queryFree := &streamedExecution{coordinator: coordinator, request: execution.SlotExecutionRequest{Operation: execution.OperationReplay}}
	defer normal.releaseProvisional()
	defer queryFree.releaseProvisional()
	if _, err := normal.loadGapFacts(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := queryFree.loadGapFacts(context.Background(), request); err == nil {
		t.Fatal("query-free facts bypassed normal retained ownership")
	}
	if len(normal.gaps.Items) != 1 || len(queryFree.gaps.Items) != 0 || coordinator.reservations.gaps != 0 {
		t.Fatal("rejection changed accepted facts or invented mutations")
	}
	normal.releaseProvisional()
	if normal.gaps.Items != nil {
		t.Fatal("released fact still referenced")
	}
	if _, err := queryFree.loadGapFacts(context.Background(), request); err != nil {
		t.Fatalf("healthy owner cannot reuse released capacity: %v", err)
	}
	queryFree.releaseProvisional()
	queryFree.releaseProvisional()
	if coordinator.reservations.gapFacts != 0 || coordinator.reservations.retainedBytes != 0 {
		t.Fatal("double close leaked or underflowed ownership")
	}
}

func TestGapFactPartialCancelReleasesAllAcceptedReferences(t *testing.T) {
	coordinator, store, request := gapReservationFixture()
	coordinator.budget.MaxGapMutations = 2
	second := request.Items[0]
	second.Identity.Plan.StrategyID = "2"
	request.Items = append(request.Items, second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.afterAccept = cancel
	owner := &streamedExecution{coordinator: coordinator}
	_, err := owner.loadGapFacts(ctx, request)
	if !errors.Is(err, context.Canceled) || store.reads != 1 || len(owner.gaps.Items) != 1 {
		t.Fatalf("cancel err=%v reads=%d retained=%d", err, store.reads, len(owner.gaps.Items))
	}
	owner.releaseProvisional()
	if owner.gaps.Items != nil || coordinator.reservations.gapFacts != 0 || coordinator.reservations.retainedBytes != 0 {
		t.Fatal("partial cancellation leaked references or budget")
	}
}

func TestTargetBudgetRejectsBeforeCopiesOrGapReads(t *testing.T) {
	coordinator, store, _ := gapReservationFixture()
	owner := &streamedExecution{coordinator: coordinator}
	targets := []execution.PlanIdentity{{StrategyID: "1"}, {StrategyID: "2"}}
	if err := owner.retainTargets(context.Background(), len(targets), targets); err == nil {
		t.Fatal("oversized target list accepted")
	}
	if store.reads != 0 || owner.retained != 0 {
		t.Fatal("rejected targets reached read/retention")
	}
}
