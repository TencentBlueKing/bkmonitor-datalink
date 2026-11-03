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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// writeWithFrames loads the mutations, so the batched write path witnesses
// them, and writes them with the given frames; it returns what the backend
// holds afterwards.
func writeWithFrames(t *testing.T, mutations []execution.StateMutation, frames []*execution.EncodedStateFrame) map[string][]byte {
	t.Helper()
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, fixedFenceKeys{testFenceKeys()})
	if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems(mutations)}); err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}
	result, err := store.ApplyRuntimeFenced(context.Background(), execution.StateApplyRequest{
		Contract: frozenRef(), Retention: testRetention(), Items: mutations, Frames: frames,
	}, testApplyFence())
	if err != nil {
		t.Fatalf("ApplyRuntimeFenced() error = %v", err)
	}
	requireAllStatus(t, result, execution.StateApplied)
	if backend.pipelines == 0 {
		t.Fatal("setup: the write did not take the batched path the frames are for")
	}
	return backend.values
}

// The frame admission hands back is the frame the write stores: kept and
// handed to the write, it leaves exactly the bytes the write leaves when it
// encodes every mutation itself.
func TestTheFrameAdmissionMeasuredIsTheOneTheWriteStores(t *testing.T) {
	mutations := seriesMutations(t, 40, applyVersion(), 0)
	admitting := newBatchStore(t, newPipelineMemoryBackend(), nil)
	admitted, err := admitting.AdmitRuntime(context.Background(), execution.StateApplyRequest{
		Contract: frozenRef(), Retention: testRetention(), Items: mutations,
	})
	if err != nil {
		t.Fatalf("AdmitRuntime() error = %v", err)
	}
	frames := make([]*execution.EncodedStateFrame, len(mutations))
	for index, item := range admitted.Items {
		if item.Status != execution.StateAdmissionAccepted || item.Frame == nil || len(item.Frame.Bytes) != item.EncodedBytes ||
			item.Frame.Revision != mutations[index].ExpectedBlobRevision+1 || item.Frame.MutationDigest != mutations[index].MutationDigest {
			t.Fatalf("admission item %d = %+v, want an admitted mutation with the frame it measured", index, item)
		}
		frames[index] = item.Frame
	}
	encoded := writeWithFrames(t, mutations, nil)
	reused := writeWithFrames(t, mutations, frames)
	if len(encoded) != len(mutations) || len(reused) != len(encoded) {
		t.Fatalf("stored keys = %d with frames, %d without, want %d", len(reused), len(encoded), len(mutations))
	}
	for key, want := range encoded {
		if got := reused[key]; string(got) != string(want) {
			t.Fatalf("stored bytes of %s differ between the kept frame and a fresh encode", key)
		}
	}
}

// The write stores a kept frame only where it is the one it would encode:
// this mutation, this revision. A frame for another mutation or another
// revision, a missing frame, or a list shorter than the items is encoded
// afresh. Marker bytes stand in for the frame, so which one was written is
// visible in what the backend holds.
func TestAKeptFrameIsWrittenOnlyWhereItIsTheOneTheWriteWouldEncode(t *testing.T) {
	marker := []byte("frame kept from admission")
	mutations := seriesMutations(t, 1, applyVersion(), 0)
	fresh := writeWithFrames(t, mutations, nil)
	matching := &execution.EncodedStateFrame{MutationDigest: mutations[0].MutationDigest, Revision: mutations[0].ExpectedBlobRevision + 1, Bytes: marker}
	for name, test := range map[string]struct {
		frames []*execution.EncodedStateFrame
		kept   bool
	}{
		"this mutation, this revision": {frames: []*execution.EncodedStateFrame{matching}, kept: true},
		"another revision": {frames: []*execution.EncodedStateFrame{{MutationDigest: matching.MutationDigest,
			Revision: matching.Revision + 1, Bytes: marker}}},
		"another mutation": {frames: []*execution.EncodedStateFrame{{MutationDigest: "another-digest",
			Revision: matching.Revision, Bytes: marker}}},
		"no frame":             {frames: []*execution.EncodedStateFrame{nil}},
		"a shorter frame list": {frames: []*execution.EncodedStateFrame{}},
	} {
		t.Run(name, func(t *testing.T) {
			stored := writeWithFrames(t, mutations, test.frames)
			for key, value := range stored {
				want := fresh[key]
				if test.kept {
					want = marker
				}
				if string(value) != string(want) {
					t.Fatalf("stored %q under %s, want the %s", value, key, map[bool]string{true: "kept frame", false: "fresh encode"}[test.kept])
				}
			}
		})
	}
}
