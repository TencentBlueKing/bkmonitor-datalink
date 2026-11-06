package elasticsearch

import (
	"context"
	"testing"
)

func TestClearScrollRequiresExplicitIDs(testContext *testing.T) {
	instance := &Instance{}
	if err := instance.ClearScroll(context.Background()); err != nil {
		testContext.Fatal(err)
	}
	for _, scrollIDs := range [][]string{{""}, {"_all"}, {"session-id", "_all"}, {"session-id", ""}} {
		err := instance.ClearScroll(context.Background(), scrollIDs...)
		if err == nil || err.Error() != "scroll cleanup requires explicit non-empty scroll IDs" {
			testContext.Fatalf("unsafe scroll IDs were not rejected before connecting: %q, error: %v", scrollIDs, err)
		}
	}
}
