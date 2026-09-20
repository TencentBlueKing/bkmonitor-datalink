package execution

import (
	"context"
	"testing"
)

func TestSlotCoverageDisabledAndFailureIsolation(t *testing.T) {
	CaptureSlotCoverage(context.Background(), func(*SlotCoverageCapture) { t.Fatal("disabled called") })
	lost := false
	ctx := WithSlotCoverageCapture(context.Background(), &SlotCoverageCapture{Lost: func() { lost = true }})
	CaptureSlotCoverage(ctx, func(*SlotCoverageCapture) { panic("callback") })
	if !lost {
		t.Fatal("failed capture became complete")
	}
	ctx = WithSlotCoverageCapture(ctx, &SlotCoverageCapture{Lost: func() { panic("lost callback") }})
	CaptureSlotCoverage(ctx, func(*SlotCoverageCapture) { panic("callback") })
}
