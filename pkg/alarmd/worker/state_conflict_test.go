// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

func TestStateConflictReasonSurvivesWrappers(t *testing.T) {
	for _, stage := range []string{"state mutation preflight", "state apply did not complete"} {
		for _, test := range []struct{ status, want string }{
			{string(execution.StateVersionConflict), contract.ReasonStateVersionConflict},
			{string(execution.StateStaleVersion), contract.ReasonStateStaleVersion},
			// The apply path's transient write failure. It went unnamed only
			// because it is not one of the preflight's two statuses, and
			// arrived on the completion line as internal_unknown - the word
			// for a site that could not classify its own failure - which is
			// not what a retryable IO error is.
			{string(execution.StateApplyRetryable), contract.ReasonStateWriteRetryable},
			// The store sets this status and this reason code together at
			// every site that produces a CAS conflict, so reading it as
			// anything else here is this layer disagreeing with the layer
			// that decided.
			{string(execution.StateApplyCASConflict), contract.ReasonStateWriteRetryable},
		} {
			t.Run(stage+"/"+test.status, func(t *testing.T) {
				cause := &worker.StateConflictError{Stage: stage, Status: test.status}
				wrapped := fmt.Errorf("execute Slot: %w", fmt.Errorf("alarmd worker: %w", cause))
				if got, ok := worker.StateConflictReason(wrapped); !ok || string(got) != test.want {
					t.Fatalf("reason = %q/%v, want %s/true", got, ok, test.want)
				}
				if cause.Error() != stage+": "+test.status || !errors.Is(wrapped, cause) {
					t.Fatalf("error text or error chain changed: %v", wrapped)
				}
			})
		}
	}
	// The rule that has not changed: a name comes from the status value, never
	// from the error's text. An error that merely says the words is not the
	// thing, or any wrapped message anywhere could claim the word.
	//
	// A status this build does not know stays here rather than borrowing a
	// name, so a status added later is a decision somebody has to take rather
	// than one that quietly never got named. Every status this build does know
	// is in the table above.
	for _, err := range []error{nil, errors.New("STATE_VERSION_CONFLICT"),
		&worker.StateConflictError{Status: "A_STATUS_THIS_BUILD_DOES_NOT_KNOW"}} {
		if got, ok := worker.StateConflictReason(err); ok || got != "" {
			t.Fatalf("unclassified error %v was named %q", err, got)
		}
	}
}

// A conflict on a repeated key says so in its text: the same status from a
// race with another writer and from a producer that made two statements for
// one series must not read alike.
func TestStateConflictErrorNamesARepeatedKey(t *testing.T) {
	plain := &worker.StateConflictError{Stage: "state apply did not complete", Status: "STATE_VERSION_CONFLICT"}
	repeated := &worker.StateConflictError{Stage: "state apply did not complete", Status: "STATE_VERSION_CONFLICT", RepeatedKey: true}
	if plain.Error() == repeated.Error() {
		t.Fatalf("a repeated-key conflict reads the same as a plain one: %q", plain.Error())
	}
	if want := "state apply did not complete: STATE_VERSION_CONFLICT (repeated key in the same request)"; repeated.Error() != want {
		t.Fatalf("repeated-key conflict = %q, want %q", repeated.Error(), want)
	}
	if reason, ok := worker.StateConflictReason(repeated); !ok || string(reason) != "STATE_VERSION_CONFLICT" {
		t.Fatalf("reason of a repeated-key conflict = %q/%v, want STATE_VERSION_CONFLICT: the code stays, the text carries the cause", reason, ok)
	}
}

// A conflict that names its comparison says so in its text with the two
// revisions it compared, and the reason code stays the same: the code is
// what the retry decision and the terminal line read, the text is what a
// reader acts on.
func TestStateConflictErrorNamesTheComparisonThatRefused(t *testing.T) {
	for _, test := range []struct {
		err  *worker.StateConflictError
		want string
	}{
		{&worker.StateConflictError{Stage: "state apply did not complete", Status: "STATE_VERSION_CONFLICT",
			Kind: execution.StateVersionConflictMissing, ExpectedRevision: 7},
			"state apply did not complete: STATE_VERSION_CONFLICT (missing: expected revision 7, stored revision 0)"},
		{&worker.StateConflictError{Stage: "state apply did not complete", Status: "STATE_VERSION_CONFLICT",
			Kind: execution.StateVersionConflictRevisionMoved, ExpectedRevision: 7, StoredRevision: 9, VersionComparison: execution.ApplyVersionPersistedNewer},
			"state apply did not complete: STATE_VERSION_CONFLICT (revision_moved: expected revision 7, stored revision 9, stored version PERSISTED_NEWER)"},
		{&worker.StateConflictError{Stage: "state apply did not complete", Status: "STATE_VERSION_CONFLICT",
			Kind: execution.StateVersionConflictRevisionMoved, ExpectedRevision: 0, StoredRevision: 1, VersionComparison: execution.ApplyVersionEqual, RepeatedKey: true},
			"state apply did not complete: STATE_VERSION_CONFLICT (revision_moved: expected revision 0, stored revision 1, stored version PERSISTED_EQUAL) (repeated key in the same request)"},
		{&worker.StateConflictError{Stage: "state mutation preflight", Status: "STATE_VERSION_CONFLICT",
			Kind: execution.StateVersionConflictSameVersionOtherStatement, ExpectedRevision: 3, StoredRevision: 3, VersionComparison: execution.ApplyVersionEqual},
			"state mutation preflight: STATE_VERSION_CONFLICT (same_version_other_statement: expected revision 3, stored revision 3, stored version PERSISTED_EQUAL)"},
	} {
		if test.err.Error() != test.want {
			t.Fatalf("text = %q, want %q", test.err.Error(), test.want)
		}
		if reason, ok := worker.StateConflictReason(test.err); !ok || string(reason) != contract.ReasonStateVersionConflict {
			t.Fatalf("reason of %q = %q/%v, want STATE_VERSION_CONFLICT: the kind is in the text, not the code", test.want, reason, ok)
		}
	}
}
