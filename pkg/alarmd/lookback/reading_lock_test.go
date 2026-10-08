package lookback

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"testing"
)

func TestSupplementReadingDoesNotHoldTheEngineLockWhenCheckingOwnership(t *testing.T) {
	f, _ := directedFixture(t, SupplementOutcome{})
	f.engine.options.Owns = func(qg execution.QueryGroupIdentity) bool { f.engine.Forget(qg); return true }
	if _, found := f.engine.SupplementReading("qg"); found {
		t.Fatal("forgotten group retained a reading")
	}
	_ = f
}
