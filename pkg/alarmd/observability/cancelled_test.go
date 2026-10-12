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
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// A failure reads as a Slot cancelled from above only when it is a
// cancellation and the caller's own context is the one cancelled: a
// cancellation error under a live context, a deadline, or another failure
// under a cancelled context is not.
func TestOnlyACancellationOfTheCallersOwnContextReadsAsCancelledFromAbove(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer stop()
	wrapped := fmt.Errorf("alarmd worker: query: %w", context.Canceled)
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"cancelled from above", cancelled, wrapped, true},
		{"a cancellation under a live context", context.Background(), wrapped, false},
		{"another failure under a cancelled context", cancelled, errors.New("refused"), false},
		{"a deadline", expired, fmt.Errorf("query: %w", context.DeadlineExceeded), false},
		{"no context", nil, wrapped, false},
	} {
		if got := CancelledFromAbove(tc.ctx, tc.err); got != tc.want {
			t.Errorf("%s: CancelledFromAbove = %v, want %v", tc.name, got, tc.want)
		}
	}
}
