// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package actiondelivery

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/projection"
	"linkd/internal/store"
)

type gateAlertReader func(context.Context, string, string) (store.StoredAlert, error)

func (f gateAlertReader) GetAlertCurrent(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	return f(ctx, tenant, id)
}

type gateProofReader func(context.Context, string, string) (projection.StoredTask, error)

func (f gateProofReader) Get(ctx context.Context, tenant, id string) (projection.StoredTask, error) {
	return f(ctx, tenant, id)
}

func gateCurrent(a domain.Alert, synced int64) store.StoredAlert {
	a = a.Clone()
	ref := a.Projection.Targets["kac"]
	ref.SyncedRevision = synced
	if synced > 0 {
		at := a.UpdateAt
		ref.SyncedAt = &at
	}
	a.Projection.Targets["kac"] = ref
	return store.StoredAlert{Alert: a, Version: store.NewVersionToken("current")}
}

func gateProof(t *testing.T, a domain.Alert) projection.StoredTask {
	t.Helper()
	task, err := projection.NewTask(a, "kac", a.UpdateAt)
	if err != nil {
		t.Fatal(err)
	}
	task.Progress.State = "succeeded"
	task.Progress.Attempts, task.Progress.TotalAttempts = 1, 1
	task.Progress.DueAt = nil
	task.Progress.Receipt = &projection.Receipt{
		SchemaVersion: projection.SchemaVersion, TenantID: a.BKTenantID, TargetID: "kac", AlertID: a.AlertID,
		AlarmID: task.Request.AlarmID, AppliedRevision: a.Revision, ContentHash: task.Request.ContentHash,
		AppliedStatus: a.Status, SearchVisible: true, DocumentRef: "owned-index/document",
	}
	if err := task.Validate(); err != nil {
		t.Fatal(err)
	}
	return projection.StoredTask{Task: task, Version: "proof"}
}

func gateTerminal(status domain.AlertStatus) domain.Alert {
	a := actionAlert("tenant", 3)
	a.Status = status
	end := a.UpdateAt
	a.EndAt, a.EndReason = &end, "done"
	a.EndType = domain.AlertEndTypeUser
	if status == domain.AlertStatusRecovered {
		a.EndType = domain.AlertEndTypeSource
	}
	return a
}

func TestProjectionGateRequiresBoundPersistentProof(t *testing.T) {
	for _, name := range []string{
		"same active", "newer active old ACK", "newer active new ACK", "newer remote receipt old ACK",
		"delivered receipt after local ACK", "terminal not acknowledged", "terminal acknowledged", "no ACK",
		"different tenant", "different alert", "different source", "source release changed", "target absent", "alert token absent",
		"proof tenant differs", "proof alert differs", "proof target differs", "proof source differs", "proof release differs",
		"proof token absent", "proof ID differs", "proof revision differs", "receipt absent", "receipt invisible",
		"same revision different content", "newer proof different current content", "remote current content differs",
		"local revision behind action", "terminal action status differs",
	} {
		t.Run(name, func(t *testing.T) {
			original := actionAlert("tenant", 1)
			task := actionTask(t, "tenant", 1)
			current := gateCurrent(original, 1)
			proof := gateProof(t, original)
			want := error(nil)
			expectedProofReads := 1
			switch name {
			case "newer active old ACK":
				current = gateCurrent(actionAlert("tenant", 2), 1)
			case "newer active new ACK":
				current = gateCurrent(actionAlert("tenant", 2), 2)
				proof = gateProof(t, actionAlert("tenant", 2))
			case "newer remote receipt old ACK":
				current = gateCurrent(actionAlert("tenant", 2), 1)
				proof.Task.Progress.Receipt = gateProof(t, actionAlert("tenant", 2)).Task.Progress.Receipt
			case "delivered receipt after local ACK":
				proof.Task.Progress.State = "delivered"
			case "terminal not acknowledged":
				current = gateCurrent(gateTerminal(domain.AlertStatusClosed), 1)
				want, expectedProofReads = ErrBusy, 0
			case "terminal acknowledged":
				current = gateCurrent(gateTerminal(domain.AlertStatusClosed), 3)
				proof = gateProof(t, gateTerminal(domain.AlertStatusClosed))
			case "no ACK":
				current = gateCurrent(original, 0)
				want, expectedProofReads = ErrBusy, 0
			case "different tenant":
				current.Alert.BKTenantID = "other"
				want, expectedProofReads = ErrInvalidReceipt, 0
			case "different alert":
				current.Alert.AlertID = "other"
				want, expectedProofReads = ErrInvalidReceipt, 0
			case "different source":
				current.Alert.EventSourceID = "other"
				want, expectedProofReads = ErrInvalidReceipt, 0
			case "source release changed":
				ref := current.Alert.Projection.Targets["kac"]
				ref.SourceVersion++
				current.Alert.Projection.Targets["kac"] = ref
				want, expectedProofReads = ErrInvalidReceipt, 0
			case "target absent":
				current.Alert.Projection = domain.AlertProjection{}
				want, expectedProofReads = ErrInvalidReceipt, 0
			case "alert token absent":
				current.Version = store.VersionToken{}
				want, expectedProofReads = ErrInvalidReceipt, 0
			case "proof tenant differs":
				other := original.Clone()
				other.BKTenantID = "other"
				proof = gateProof(t, other)
				want = ErrInvalidReceipt
			case "proof alert differs":
				other := original.Clone()
				other.AlertID = "other"
				proof = gateProof(t, other)
				want = ErrInvalidReceipt
			case "proof target differs":
				proof.Task.Request.TargetID = "other"
				want = ErrInvalidReceipt
			case "proof source differs":
				other := original.Clone()
				other.EventSourceID = "other"
				proof = gateProof(t, other)
				want = ErrInvalidReceipt
			case "proof release differs":
				proof.Task.SourceVersion++
				want = ErrInvalidReceipt
			case "proof token absent":
				proof.Version = ""
				want = ErrInvalidReceipt
			case "proof ID differs":
				proof.Task.ID = strings.Repeat("a", 64)
				want = ErrInvalidReceipt
			case "proof revision differs":
				proof = gateProof(t, actionAlert("tenant", 2))
				want = ErrInvalidReceipt
			case "receipt absent":
				proof.Task.Progress.Receipt = nil
				want = ErrInvalidReceipt
			case "receipt invisible":
				proof.Task.Progress.Receipt.SearchVisible = false
				want = ErrInvalidReceipt
			case "same revision different content":
				other := original.Clone()
				other.Title = "different"
				proof = gateProof(t, other)
				// 更高远端版本也不能掩盖同一原请求 revision 的内容冲突。
				proof.Task.Progress.Receipt.AppliedRevision = 2
				want = ErrInvalidReceipt
			case "newer proof different current content":
				current = gateCurrent(actionAlert("tenant", 2), 2)
				other := actionAlert("tenant", 2)
				other.Title = "different"
				proof = gateProof(t, other)
				want = ErrInvalidReceipt
			case "remote current content differs":
				current = gateCurrent(actionAlert("tenant", 2), 1)
				other := actionAlert("tenant", 2)
				other.Title = "different"
				proof.Task.Progress.Receipt = gateProof(t, other).Task.Progress.Receipt
				want = ErrInvalidReceipt
			case "local revision behind action":
				task = actionTask(t, "tenant", 2)
				want, expectedProofReads = ErrInvalidReceipt, 0
			case "terminal action status differs":
				closed := gateTerminal(domain.AlertStatusClosed)
				var err error
				task, err = NewTask(closed, "kac", Cause{Type: "user_operation", ID: "close"}, closed.UpdateAt)
				if err != nil {
					t.Fatal(err)
				}
				current = gateCurrent(gateTerminal(domain.AlertStatusRecovered), 3)
				proof = gateProof(t, gateTerminal(domain.AlertStatusRecovered))
				want = ErrInvalidReceipt
			}
			proofReads, alertReads := 0, 0
			gate, err := NewProjectionGate(gateAlertReader(func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
				alertReads++
				deadline, ok := ctx.Deadline()
				if tenant != task.Request.TenantID || id != task.Request.AlertID || !ok || time.Until(deadline) > 5*time.Second {
					t.Fatal("unscoped or unbounded Alert read")
				}
				out := current
				out.Alert = out.Alert.Clone()
				return out, nil
			}), gateProofReader(func(_ context.Context, tenant, id string) (projection.StoredTask, error) {
				proofReads++
				expectedID, _ := projection.TaskID(task.Request.TenantID, task.Request.AlertID, task.Request.TargetID, current.Alert.Projection.Targets["kac"].SyncedRevision)
				if tenant != task.Request.TenantID || id != expectedID {
					t.Fatal("proof selected by remote version or wrong scope")
				}
				out := proof
				out.Task = out.Task.Clone()
				return out, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			before := task.Clone()
			receipt, err := gate.Check(t.Context(), task)
			if !errors.Is(err, want) || proofReads != expectedProofReads {
				t.Fatalf("error=%v want=%v reads=%d want=%d", err, want, proofReads, expectedProofReads)
			}
			if want == nil {
				if receipt != *proof.Task.Progress.Receipt || alertReads != 2 {
					t.Fatal("missing proof or final reread", receipt, alertReads)
				}
				receipt.DocumentRef = "changed by caller"
				if proof.Task.Progress.Receipt.DocumentRef != "owned-index/document" {
					t.Fatal("returned mutable proof")
				}
			} else if receipt != (projection.Receipt{}) {
				t.Fatal("failure returned usable proof")
			}
			if !reflect.DeepEqual(before, task) {
				t.Fatal("gate mutated frozen action")
			}
		})
	}
}

func TestProjectionGateRechecksBusinessAndPropagatesReadFailures(t *testing.T) {
	for _, step := range []string{"first read", "proof read", "proof missing", "second read", "business changed", "ACK changed", "canceled at proof"} {
		t.Run(step, func(t *testing.T) {
			a := actionAlert("tenant", 1)
			row := gateCurrent(a, 1)
			if step == "ACK changed" {
				row = gateCurrent(actionAlert("tenant", 2), 1)
			}
			proof := gateProof(t, a)
			reads := 0
			failure := errors.New("injected store read failure")
			want := error(failure)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			gate, err := NewProjectionGate(gateAlertReader(func(ctx context.Context, _, _ string) (store.StoredAlert, error) {
				reads++
				if step == "first read" && reads == 1 || step == "second read" && reads == 2 {
					return store.StoredAlert{}, failure
				}
				if reads == 2 && (step == "business changed" || step == "ACK changed") {
					next := actionAlert("tenant", 2)
					if step == "business changed" {
						return gateCurrent(next, 1), nil
					}
					return gateCurrent(next, 2), nil
				}
				return row, nil
			}), gateProofReader(func(ctx context.Context, _, _ string) (projection.StoredTask, error) {
				switch step {
				case "proof read":
					return projection.StoredTask{}, failure
				case "proof missing":
					return projection.StoredTask{}, projection.ErrNotFound
				case "canceled at proof":
					cancel()
				}
				return proof, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			switch step {
			case "proof missing":
				want = projection.ErrNotFound
			case "business changed", "ACK changed":
				want = ErrBusy
			case "canceled at proof":
				want = context.Canceled
			}
			receipt, err := gate.Check(ctx, actionTask(t, "tenant", 1))
			if !errors.Is(err, want) || receipt != (projection.Receipt{}) {
				t.Fatal(receipt, err, want)
			}
			if step == "canceled at proof" && reads != 1 {
				t.Fatal("read after cancellation")
			}
		})
	}
}

func TestProjectionGateCancellationAndConcurrentReaders(t *testing.T) {
	a := actionAlert("tenant", 1)
	row, proof := gateCurrent(a, 1), gateProof(t, a)
	task := actionTask(t, "tenant", 1)
	var reads atomic.Int64
	gate, err := NewProjectionGate(gateAlertReader(func(ctx context.Context, _, _ string) (store.StoredAlert, error) {
		reads.Add(1)
		copy := row
		copy.Alert = row.Alert.Clone()
		return copy, nil
	}), gateProofReader(func(context.Context, string, string) (projection.StoredTask, error) {
		copy := proof
		copy.Task = proof.Task.Clone()
		return copy, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := gate.Check(ctx, task); !errors.Is(err, context.Canceled) || reads.Load() != 0 {
		t.Fatal(err, "canceled I/O")
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if _, err := gate.Check(t.Context(), task); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if reads.Load() != 64 {
		t.Fatal("unbounded reads", reads.Load())
	}
	blocked, err := NewProjectionGate(gateAlertReader(func(ctx context.Context, _, _ string) (store.StoredAlert, error) {
		<-ctx.Done()
		return store.StoredAlert{}, ctx.Err()
	}), gate.proofs)
	if err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer stop()
	if _, err := blocked.Check(short, task); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, err := NewProjectionGate(nil, gate.proofs); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := NewProjectionGate(gate.alerts, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := gate.Check(t.Context(), Task{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
