package execution

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// SlotCoverageCapture is a synchronous, optional side channel for one actual
// execution. Callbacks consume references before returning and must not retain
// datasets or mutate business objects. No observation authorizes execution.
type SlotCoverageCapture struct {
	Prepared          func(InternalExecutionHeader, map[ConsumerRef]strategy.EffectiveTimeFact)
	QueryCalled       func(QueryAttempt)
	InputCompleted    func(QueryExecutionCompletion)
	Evaluated         func(EvaluationRequest, EvaluationResult)
	OutputWritten     func([]contract.TriggerEventV1, error)
	PriorStateApplied func()
	ProgressCommitted func(ProgressCommitRequest, ProgressCommitResult)
	BeginCommitted    func(bool)
	Lost              func()
}

type slotCoverageKey struct{}

func WithSlotCoverageCapture(ctx context.Context, c *SlotCoverageCapture) context.Context {
	return context.WithValue(ctx, slotCoverageKey{}, c)
}

// CaptureSlotCoverage isolates side-channel failures; Lost prevents a partially
// observed execution from later being advertised as complete.
func CaptureSlotCoverage(ctx context.Context, consume func(*SlotCoverageCapture)) {
	c, _ := ctx.Value(slotCoverageKey{}).(*SlotCoverageCapture)
	if c == nil {
		return
	}
	defer func() {
		if recover() != nil && c.Lost != nil {
			func() { defer func() { _ = recover() }(); c.Lost() }()
		}
	}()
	consume(c)
}
