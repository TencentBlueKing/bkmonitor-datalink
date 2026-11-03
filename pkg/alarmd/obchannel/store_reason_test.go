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
