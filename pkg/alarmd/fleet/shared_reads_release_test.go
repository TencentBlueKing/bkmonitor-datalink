// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"sync/atomic"
	"testing"
)

// The read's hold is gone the moment its last caller lets go of the answer:
// a page that releases what it held and then asks for the view it keeps is
// never charged for the read twice. The read used to let go of its own share
// after telling its callers the answer was there, so a caller fast enough
// could release first and leave the hold to the read's goroutine; the window
// is narrow, and the case runs it many times.
func TestASharedReadsHoldIsGoneWhenItsLastCallerLetsGo(t *testing.T) {
	for round := 0; round < 2000; round++ {
		var reads sharedReads[int]
		var released atomic.Bool
		read := func(ctx context.Context) int {
			pageHoldsOf(ctx).add(func() { released.Store(true) }, 1)
			return 1
		}
		ctx, release := withPageHolds(context.Background())
		if answer := reads.do(ctx, "k", read, func(error) int { return 0 }); answer != 1 {
			t.Fatalf("round %d: answer %d", round, answer)
		}
		release()
		if !released.Load() {
			t.Fatalf("round %d: the read's hold outlived the last caller that used its answer", round)
		}
	}
}
