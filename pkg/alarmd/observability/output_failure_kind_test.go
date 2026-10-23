// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"errors"
	"fmt"
	"testing"
)

type kinded struct {
	kind  string
	inner error
}

func (k *kinded) Error() string             { return "kinded " + k.kind }
func (k *kinded) Unwrap() error             { return k.inner }
func (k *kinded) OutputFailureKind() string { return k.kind }

// When errors carrying kinds wrap one another, the written order decides:
// snapshot_store, sink_not_open, client_rejected, broker_refused,
// ack_unknown. A dependency error (ack_unknown) wrapping each of the others
// reads as the other; the order holds through fmt wrapping and errors.Join.
func TestNestedOutputFailureKindsFollowTheWrittenOrder(t *testing.T) {
	order := []string{OutputFailureSnapshotStore, OutputFailureSinkNotOpen, OutputFailureClientRejected,
		OutputFailureBrokerRefused, OutputFailureAckUnknown}
	for i, outer := range order {
		for _, inner := range order[:i] {
			err := &kinded{kind: outer, inner: fmt.Errorf("wrapped: %w", &kinded{kind: inner})}
			if got := OutputFailureKindOf(err); got != inner {
				t.Errorf("%s wrapping %s read %s, want the inner, earlier in the order", outer, inner, got)
			}
			joined := errors.Join(&kinded{kind: inner}, &kinded{kind: outer})
			if got := OutputFailureKindOf(joined); got != inner {
				t.Errorf("%s joined with %s read %s, want %s", outer, inner, got, inner)
			}
		}
	}
	if got := OutputFailureKindOf(errors.New("plain")); got != OutputFailureUnknown {
		t.Fatalf("an error without a kind read %s, want unknown", got)
	}
	if got := OutputFailureKindOf(&kinded{kind: "not-a-kind"}); got != OutputFailureUnknown {
		t.Fatalf("a word outside the closed set read %s, want unknown", got)
	}
	if got := OutputFailureKindOf(nil); got != OutputFailureUnknown {
		t.Fatalf("nil read %s", got)
	}
}
