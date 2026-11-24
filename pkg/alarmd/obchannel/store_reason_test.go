package obchannel

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obevidence"
)

// A store read the server did not answer, and an authorization whose store
// did not, both reach the CLI with the reason.
func TestAnUnansweredStoreReachesTheCLIWithItsReason(t *testing.T) {
	out := storeOutcome(obevidence.Result{Status: "dependency_unavailable", Reason: "connection_closed"})
	if out.Error == nil || out.Error.Code != "evidence_unavailable" || out.Error.Reason != "connection_closed" {
		t.Fatalf("store outcome %+v", out.Error)
	}
	wrapped := fmt.Errorf("admission: %w", &cliauth.Error{Code: "auth_store_unavailable", Reason: "timeout", HTTPStatus: 503})
	if authReason(wrapped) != "timeout" || authReason(errors.New("plain")) != "" {
		t.Fatalf("auth reason %q", authReason(wrapped))
	}
}

// A session the authorization store could not read is refused naming why,
// on the channel's own answer.
func TestASessionTheStoreDidNotReadIsRefusedWithItsReason(t *testing.T) {
	a := &testAuth{err: &cliauth.Error{Code: "auth_store_unavailable", Message: "store", Reason: "connection_closed", HTTPStatus: 503}}
	c := testChannel(t, a, Operation{ID: "read", Summary: "Read", Run: func(context.Context, Params) Outcome { return Outcome{Complete: true} }})
	status, out := call(t, c, envelope(c, "invoke", "read", Params{}))
	if status != 503 || out.Error == nil || out.Error.Code != "auth_store_unavailable" || out.Error.Reason != "connection_closed" {
		t.Fatalf("status %d error %+v", status, out.Error)
	}
}

// A no-data memory whose group keys are not all listed says so on the answer:
// how many of how many, and whether a stopped scan makes them only the first
// of what it saw. A memory listed whole adds nothing.
func TestAPartialGroupKeyListingIsNamedOnTheAnswer(t *testing.T) {
	record := func(key string, listing obevidence.GroupKeyListing) obevidence.PlanRecord {
		return obevidence.PlanRecord{Kind: obevidence.RecordNoDataMemory, Key: key, Status: "ok", GroupKeys: &listing}
	}
	fifty := make([]string, 50)
	out := storeOutcome(obevidence.Result{Status: "ok", Complete: true, Value: []obevidence.PlanRecords{{Records: []obevidence.PlanRecord{
		record("cut", obevidence.GroupKeyListing{Keys: fifty, Total: 120, Scan: obevidence.GroupKeyScanComplete, CutBy: obevidence.GroupKeysCutByCount}),
		record("whole", obevidence.GroupKeyListing{Keys: []string{"a", "b", "c"}, Total: 3, Scan: obevidence.GroupKeyScanComplete}),
		record("stray", obevidence.GroupKeyListing{Keys: []string{"a"}, Total: 1, Scan: obevidence.GroupKeyScanComplete, UnknownFields: 2}),
		record("stopped", obevidence.GroupKeyListing{Keys: fifty, Total: 5000, Scan: obevidence.GroupKeyScanCommands, ScannedFields: 1000}),
		{Kind: obevidence.RecordNoDataMemory, Key: "absent", Status: "missing"},
	}}}})
	want := []string{
		"cut: group_keys lists the first 50 of 120 groups in byte order (cut by count).",
		"stray: 2 fields are neither the header nor a group; the runtime reads this record as corrupt.",
		"stopped: group_keys scan stopped at commands after 1000 fields; the 50 keys listed are the first of the fields seen, not of all 5000 groups.",
	}
	if fmt.Sprint(out.Limitations) != fmt.Sprint(want) {
		t.Fatalf("limitations = %q,\nwant %q", out.Limitations, want)
	}
}
