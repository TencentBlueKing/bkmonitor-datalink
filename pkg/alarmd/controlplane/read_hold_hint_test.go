package controlplane

import (
	"context"
	"testing"
)

func TestForeignReadHoldCanClearTheOwnedTimelineHint(t *testing.T) {
	ctx := WithTimelineRevisionHint(context.Background(), 7)
	if got := timelineRevisionHint(WithTimelineRevisionHint(ctx, 0)); got != 0 {
		t.Fatalf("foreign predecessor kept owned revision %d", got)
	}
	if timelineRevisionHint(ctx) != 7 {
		t.Fatal("clearing foreign hint changed owned context")
	}
}
