// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"reflect"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A streamed binding is the same for every series of its query but for the
// series' Dataset and view (access/source.go builds each series' binding from
// the query, the requirement and the consumer alone). So the Slot keeps the
// rest once, as a shared binding, and per series only the two pointers
// (decision-020 batch 8, the input's resident bytes): half a million series
// of one query are one shared binding and half a million streamedInputs, not
// half a million copies of the binding.

// streamedInput is one streamed (consumer, series, requirement) binding as the
// Slot keeps it.
type streamedInput struct {
	shared  *execution.NamedInputBinding
	dataset *execution.Dataset
	view    *execution.DatasetView
}

// binding is the series' binding whole: the shared part with its own Dataset
// and view, the value every reader of a series' inputs is handed.
func (input streamedInput) binding() execution.NamedInputBinding {
	binding := *input.shared
	binding.Dataset, binding.View = input.dataset, input.view
	return binding
}

// sharedBindingKey narrows the search for a shared binding equal to a new
// one; the equality itself is sameSharedBinding.
type sharedBindingKey struct {
	consumer    execution.ConsumerRef
	requirement execution.RequirementID
	provider    execution.ProviderResultRef
}

// sharedBindings are the Slot's shared bindings, each kept once.
type sharedBindings struct {
	byKey map[sharedBindingKey][]*execution.NamedInputBinding
	// all is every shared binding in the order it was first kept.
	all []*execution.NamedInputBinding
}

// share returns the shared binding equal to binding less its Dataset and view,
// keeping a new one if none is, and whether it kept one.
func (shared *sharedBindings) share(binding execution.NamedInputBinding) (*execution.NamedInputBinding, bool) {
	binding.Dataset, binding.View = nil, nil
	key := sharedBindingKey{consumer: binding.Consumer, requirement: binding.RequirementID, provider: binding.ProviderResult}
	if shared.byKey == nil {
		shared.byKey = make(map[sharedBindingKey][]*execution.NamedInputBinding)
	}
	candidates := shared.byKey[key]
	for index := len(candidates) - 1; index >= 0; index-- {
		if sameSharedBinding(candidates[index], &binding) {
			return candidates[index], false
		}
	}
	kept := &binding
	shared.byKey[key] = append(candidates, kept)
	shared.all = append(shared.all, kept)
	return kept, true
}

// sameSharedBinding reports whether two bindings without their Dataset and
// view are the same binding: every field equal, the facts and the partial
// evidence by value.
func sameSharedBinding(left, right *execution.NamedInputBinding) bool {
	if left.Consumer != right.Consumer || left.RequirementID != right.RequirementID ||
		left.DatasetName != right.DatasetName || left.Role != right.Role ||
		left.ProviderResult != right.ProviderResult || left.QueryWindow != right.QueryWindow ||
		left.Dataset != right.Dataset || left.View != right.View ||
		left.Completeness != right.Completeness || left.DataState != right.DataState ||
		left.Disposition != right.Disposition || left.ReasonCode != right.ReasonCode ||
		left.ImpactScope != right.ImpactScope || left.Provenance != right.Provenance ||
		left.UnavailableAttribution != right.UnavailableAttribution {
		return false
	}
	if (left.PartialEvidence == nil) != (right.PartialEvidence == nil) ||
		(left.PartialEvidence != nil && left.PartialEvidence != right.PartialEvidence &&
			!reflect.DeepEqual(*left.PartialEvidence, *right.PartialEvidence)) {
		return false
	}
	if len(left.QualityFacts) != len(right.QualityFacts) || len(left.Terminals) != len(right.Terminals) {
		return false
	}
	if len(left.QualityFacts) != 0 && !reflect.DeepEqual(left.QualityFacts, right.QualityFacts) {
		return false
	}
	return len(left.Terminals) == 0 || reflect.DeepEqual(left.Terminals, right.Terminals)
}

// What the Slot keeps of streamed bindings, by structure: a shared binding
// once, at its struct and the facts it carries; and per series an entry of
// the streamed map - the key and the streamedInput side by side, a control
// byte, the map filled to seven eighths.
var (
	streamedEntryBytes = (uint64(unsafe.Sizeof(streamedInputKey{})+unsafe.Sizeof(streamedInput{})) + 1) * 8 / 7
	sharedBindingBytes = uint64(unsafe.Sizeof(execution.NamedInputBinding{}))
)

// What a series is beside its bindings: its hold (inputHolds, a string key
// and a pointer in a map, and the hold itself) and its entry in its Plan's
// series (planSeries, a string key in a set), each a map slot filled to
// seven eighths; and, from the completion to the Slot's end, its prepared
// series.
var (
	seriesBookkeepingBytes = (uint64(unsafe.Sizeof(execution.SeriesIdentityDigest("")))+8+1)*8/7 +
		uint64(unsafe.Sizeof(seriesInputHold{})) + (uint64(unsafe.Sizeof(execution.SeriesIdentityDigest("")))+1)*8/7
	preparedSeriesBytes = uint64(unsafe.Sizeof(preparedSeries{}))
)

// sharedBindingHeldBytes is what one shared binding holds.
func sharedBindingHeldBytes(binding *execution.NamedInputBinding) uint64 {
	total := sharedBindingBytes + uint64(cap(binding.QualityFacts))*qualityFactBytes +
		uint64(cap(binding.Terminals))*inputTerminalBytes
	if binding.PartialEvidence != nil {
		total += partialEvidenceBytes
	}
	return total
}

// keepStreamed keeps one series' binding under key: the part it shares, once,
// charged to the input phase when it is first kept and held to the Slot's
// end, and the series' Dataset and view in the entry the batch was charged.
func (stream *streamedExecution) keepStreamed(ctx context.Context, key streamedInputKey, binding execution.NamedInputBinding) error {
	shared, fresh := stream.shared.share(binding)
	if fresh {
		if err := stream.reserveRetainedInput(ctx, sharedBindingHeldBytes(shared)); err != nil {
			return err
		}
	}
	stream.streamed[key] = streamedInput{shared: shared, dataset: binding.Dataset, view: binding.View}
	return nil
}
