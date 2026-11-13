// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// changeInPlace changes a built mutation's content where its slices hold it:
// the seal still holds: it proves the build, not the content.
func changeInPlace(mutation execution.StateMutation) {
	mutation.Points[0].RecordID += "-changed"
}

// Content changed in place after the build passes the checks inside the
// process - they prove only that it is the mutation built - and is refused
// by the store's admission, which checks it whole: the last check before the
// Slot's events (decision-020 0.6.x throughput design 3.2-1).
func TestContentChangedAfterTheBuildIsRefusedAtTheAdmission(t *testing.T) {
	mutations := seriesMutations(t, 1, applyVersion(), 0)
	changeInPlace(mutations[0])
	if err := mutations[0].ValidateSealed(); err != nil {
		t.Fatalf("the check inside the process hashed the content again: %v", err)
	}
	store := newBatchStore(t, newPipelineMemoryBackend(), nil)
	admitted, err := store.AdmitRuntime(context.Background(), execution.StateApplyRequest{
		Contract: frozenRef(), Retention: testRetention(), Items: mutations,
	})
	if err != nil {
		t.Fatal(err)
	}
	item := admitted.Items[0]
	if item.Status != execution.StateAdmissionDeterministicInvalid || item.ReasonCode != execution.ReasonCode(contract.ReasonStateCorrupt) ||
		item.RefusalRule != PackedRuleMutationDigestMismatch || item.Frame != nil {
		t.Fatalf("admission of a mutation changed after its build = %+v, want it refused as a digest mismatch", item)
	}
}

// A mutation changed after its admission is written as the admission framed
// it: the bytes stored are the frame encoded from the content checked whole,
// not the content as it stands. One the admission did not frame is checked
// whole at the write and refused.
func TestTheWriteStoresTheAdmittedFrameAndChecksWholeWithoutOne(t *testing.T) {
	mutations := seriesMutations(t, 1, applyVersion(), 0)
	want := writeWithFrames(t, mutations, nil)
	admitting := newBatchStore(t, newPipelineMemoryBackend(), nil)
	admitted, err := admitting.AdmitRuntime(context.Background(), execution.StateApplyRequest{
		Contract: frozenRef(), Retention: testRetention(), Items: mutations,
	})
	if err != nil || admitted.Items[0].Frame == nil {
		t.Fatalf("admission = %+v (%v), want the mutation framed", admitted, err)
	}
	changed := seriesMutations(t, 1, applyVersion(), 0)
	changeInPlace(changed[0])
	stored := writeWithFrames(t, changed, []*execution.EncodedStateFrame{admitted.Items[0].Frame})
	for key, value := range want {
		if string(stored[key]) != string(value) {
			t.Fatalf("stored %q under %s after a change past the admission, want the admitted frame", stored[key], key)
		}
	}

	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, fixedFenceKeys{testFenceKeys()})
	if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems(changed)}); err != nil {
		t.Fatal(err)
	}
	result, err := store.ApplyRuntimeFenced(context.Background(), execution.StateApplyRequest{
		Contract: frozenRef(), Retention: testRetention(), Items: changed,
	}, testApplyFence())
	if err != nil {
		t.Fatal(err)
	}
	if item := result.Items[0]; item.Status != execution.StateApplyDeterministicInvalid || item.RefusalRule != PackedRuleMutationDigestMismatch {
		t.Fatalf("a changed mutation written without a frame = %+v, want it refused as a digest mismatch", item)
	}
	if len(backend.values) != 0 {
		t.Fatal("a changed mutation written without a frame was stored")
	}
}
