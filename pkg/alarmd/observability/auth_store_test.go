package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The authorization store's failure is a line of its own, carrying the text
// the public answer leaves out.
func TestAnAuthStoreFailureIsLoggedWithItsText(t *testing.T) {
	var output bytes.Buffer
	newQueryFailureTestObserver(&output).Observe(context.Background(), Observation{Component: ComponentRuntime, Stage: StageAuthStore,
		Result: ResultFailed, Direction: DirectionInternal, ReasonCode: ReasonContractRetryable,
		Err: errors.New("authorization store sentinel_unreachable: redis: all sentinels specified in configuration are unreachable")})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("no line: %q (%v)", output.String(), err)
	}
	if event["stage"] != StageAuthStore || event["component"] != ComponentRuntime || !strings.Contains(output.String(), "sentinel_unreachable") {
		t.Fatalf("line %v", event)
	}
}
