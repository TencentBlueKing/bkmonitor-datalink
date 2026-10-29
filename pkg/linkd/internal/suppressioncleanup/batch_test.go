// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package suppressioncleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"linkd/internal/policy"
)

type cleanupBatchDocs struct {
	*memoryDocs
	reads, writes int
}

func (d *cleanupBatchDocs) ReadCleanupBatch(ctx context.Context, ids []string) []Document {
	d.reads++
	out := make([]Document, len(ids))
	for i, id := range ids {
		b, v, e := d.Get(ctx, "suppression_cleanups", id)
		out[i] = Document{ID: id, Payload: b, Version: v, Err: e}
	}
	return out
}

func (d *cleanupBatchDocs) WriteCleanupBatch(ctx context.Context, docs []Document) []Document {
	d.writes++
	out := append([]Document(nil), docs...)
	for i, row := range docs {
		out[i].Err = d.Put(ctx, "suppression_cleanups", row.ID, row.Version, row.Payload)
		if out[i].Err == nil {
			out[i].Payload, out[i].Version, out[i].Err = d.Get(ctx, "suppression_cleanups", row.ID)
		}
	}
	return out
}

func TestCleanupJournal5000BatchIntentCompletionAndRetry(t *testing.T) {
	for _, scenario := range []string{"normal", "intent_failure", "finish_failure", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			d := &cleanupBatchDocs{memoryDocs: newDocs()}
			j := journal(t, d)
			causes := make([]Cause, 5000)
			for i := range causes {
				causes[i] = terminalCause()
				causes[i].AlertID = fmt.Sprintf("alert-%04d", i)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			d.failStart = scenario == "intent_failure"
			d.failFinish = scenario == "finish_failure"
			calls := 0
			run := func(call context.Context, selected []Cause) ([]Result, []error) {
				calls++
				if d.writes != 1 {
					t.Fatal("cleanup ran before pending batch")
				}
				r, e := make([]Result, len(selected)), make([]error, len(selected))
				for i, c := range selected {
					raw, _, err := d.Get(call, "suppression_cleanups", prefix(c.TenantID)+c.ID())
					if err != nil {
						t.Fatal(err)
					}
					var saved Record
					if json.Unmarshal(raw, &saved) != nil || saved.State != "pending" {
						t.Fatal("missing pending intent")
					}
					r[i], e[i] = success(call)
				}
				return r, e
			}
			results := j.RunBatch(ctx, causes, run)
			for _, err := range results {
				if scenario == "normal" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil {
					t.Fatal("failure reported as success")
				}
			}
			if d.reads != 1 {
				t.Fatal("per-item reads", d.reads)
			}
			if scenario == "normal" {
				if d.writes != 2 || calls != 1 {
					t.Fatal(d.writes, calls)
				}
				for _, err := range j.RunBatch(ctx, causes, run) {
					if err != nil {
						t.Fatal(err)
					}
				}
				if calls != 1 {
					t.Fatal("repeated completed cleanup")
				}
			}
			if scenario == "intent_failure" || scenario == "cancelled" {
				if calls != 0 {
					t.Fatal("effect after failed intent")
				}
			}
			if scenario == "finish_failure" {
				if calls != 1 {
					t.Fatal(calls)
				}
				d.failFinish = false
				d.writes = 0
				results = j.RunBatch(t.Context(), causes, run)
				for _, err := range results {
					if err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestCleanupBatchExistingCauseConflict(t *testing.T) {
	d := &cleanupBatchDocs{memoryDocs: newDocs()}
	j := journal(t, d)
	c := terminalCause()
	if err := j.Run(t.Context(), c, success); err != nil {
		t.Fatal(err)
	}
	c.Fingerprint = "different"
	errs := j.RunBatch(t.Context(), []Cause{c}, func(context.Context, []Cause) ([]Result, []error) {
		t.Fatal("conflicting cleanup ran")
		return nil, nil
	})
	if !errors.Is(errs[0], policy.ErrConflict) {
		t.Fatal(errs[0])
	}
}
