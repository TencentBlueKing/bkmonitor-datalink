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

// The per-key write path counts the items the envelope decided, and only those.
//
// That path reads both keys of every series it writes, so the preflight's
// count - which is what said the envelope was no longer needed - cannot see
// it: a deployment could read zero there while this path still classified
// writes against envelope records. Deleting the envelope on that reading
// would change what this path decides. Each shape is its own case, so a
// count that is always one or always zero fails one of them.
func TestTheSequentialApplyCountsTheItemsTheEnvelopeDecided(t *testing.T) {
	older := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 30, SlotDigest: "slot-0"}
	newer := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 60, SlotDigest: "slot-1"}
	next := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 120, SlotDigest: "slot-2"}
	type seed struct {
		envelope        *execution.ApplyVersion
		envelopeGarbage bool
		framed          *execution.ApplyVersion
	}
	cases := []struct {
		name string
		seed seed
		want int
	}{
		{name: "only the envelope: the write is classified against it", seed: seed{envelope: &older}, want: 1},
		{name: "the envelope is newer than the frame: this path takes the envelope", seed: seed{envelope: &newer, framed: &older}, want: 1},
		{name: "an envelope that does not read and no frame: the envelope refuses the write", seed: seed{envelopeGarbage: true}, want: 1},
		{name: "the frame is newer than the envelope: the envelope decides nothing", seed: seed{envelope: &older, framed: &newer}, want: 0},
		{name: "only the frame", seed: seed{framed: &older}, want: 0},
		{name: "neither key", seed: seed{}, want: 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			backend := newPipelineMemoryBackend()
			store := newBatchStore(t, backend, nil)
			identity := seriesIdentity(0)
			envelopeKey, _ := RuntimeStateKeyV2("alarmd", identity)
			framedKey, _ := RuntimeStateKeyV3("alarmd", identity)
			if test.seed.envelope != nil {
				backend.values[envelopeKey], _ = encodeRuntime(seriesMutation(t, identity, *test.seed.envelope, 0, "env"), 7)
			}
			if test.seed.envelopeGarbage {
				backend.values[envelopeKey] = []byte("not a record")
			}
			if test.seed.framed != nil {
				backend.values[framedKey], _ = encodeRuntimePacked(seriesMutation(t, identity, *test.seed.framed, 0, ""), 3)
			}

			// No preflight, so no witness: the per-key path is the one taken.
			result, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(),
				Retention: testRetention(), Items: []execution.StateMutation{seriesMutation(t, identity, next, 0, "")}})
			if err != nil {
				t.Fatal(err)
			}
			if backend.casCalls+backend.mgetCalls == 0 {
				t.Fatal("the apply did not take the per-key path")
			}
			if result.EnvelopeReads != test.want {
				t.Fatalf("envelope reads = %d, want %d (item %+v)", result.EnvelopeReads, test.want, result.Items[0])
			}
		})
	}
}
