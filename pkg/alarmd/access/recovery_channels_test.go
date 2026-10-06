package access

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

type realChannelPermits struct{ flights *scheduler.FlightCoordinator }

func (p realChannelPermits) AcquireQueryPermit(ctx context.Context, slot execution.SlotIdentity, op execution.Operation, deadline time.Time) (QueryPermit, error) {
	return p.flights.AcquireQueryPermit(ctx, slot, op, deadline)
}

func (p realChannelPermits) AcquireRecoveryChannels(ctx context.Context, slot execution.SlotIdentity, op execution.Operation, deadline time.Time, maximum int, beforeWait func()) (RecoveryChannels, error) {
	c, err := p.flights.AcquireRecoveryChannels(ctx, slot, op, deadline, maximum, beforeWait)
	if err != nil {
		return nil, err
	}
	return realSourceChannels{c}, nil
}

type realSourceChannels struct{ *scheduler.RecoveryChannels }

func (p realSourceChannels) AcquireQueryPermit(ctx context.Context, slot execution.SlotIdentity, op execution.Operation, deadline time.Time) (QueryPermit, error) {
	return p.RecoveryChannels.AcquireQueryPermit(ctx, slot, op, deadline)
}

func TestSourceRecoveryReusesOneRealChannelForNonemptyQueries(t *testing.T) {
	ref, frozen := frozenExecution(t)
	second := frozen.Requirements[0]
	second.RequirementID, second.DatasetName = "secondary", "secondary"
	second.RelativeWindow.StartOffsetSeconds = -120
	frozen.Requirements = append(frozen.Requirements, second)
	ref = bindFrozenDueDigest(t, ref, frozen)
	now := time.UnixMilli(2_000_000_000_000)
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(scheduler.RecoveryLimits{
		ProcessQueryPermits: 2, RecoveryQueryPermits: 1, ReadyQueueCapacity: 10,
		RecoveryQueueCapacity: 10, MaxQueuedItemsPerQG: 1, MaxReplaySlots: 3,
		MaxReplayAge: time.Minute, RetryMinDelay: time.Millisecond, RetryMaxDelay: time.Second,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, provider, realChannelPermits{flights}, Config{MinReadyDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return now }
	consumer := &recordingConsumer{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	completion, err := source.Execute(ctx, execution.QueryExecutionRequest{Contract: ref, Operation: execution.OperationReplay, AttemptNo: 1}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	if len(consumer.batches) != 2 || len(completion.PhysicalQueries) != 2 || !completion.AllRequiredCompleted {
		t.Fatalf("nonempty multi-query completion lost: batches=%d completion=%+v", len(consumer.batches), completion)
	}
	for _, query := range completion.PhysicalQueries {
		if query.DataState != execution.DataStateData || query.Delivery.Records != 1 {
			t.Fatalf("expected actual nonempty delivery: %+v", query)
		}
	}
	// A following execution can reserve R and both P slots: neither the two
	// body deliveries nor the retained consumer inputs left permits behind.
	r, err := flights.AcquireRecoveryChannels(ctx, ref.Slot, execution.OperationReplay, now.Add(time.Minute), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	p, err := r.AcquireQueryPermit(ctx, ref.Slot, execution.OperationReplay, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Release()
	normal, err := flights.AcquireQueryPermit(ctx, execution.SlotIdentity{QueryGroup: "healthy", EvaluationTime: ref.Slot.EvaluationTime}, execution.OperationNormal, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	normal.Release()
}
