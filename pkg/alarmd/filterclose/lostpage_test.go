// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filterclose

import (
	"context"
	"net/http"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

// A real page whose request the round's own context ends (the leader
// stops, the round's time is up) may have been closed all the same: its
// candidates are kept although the context is gone, and the retry by id
// settles them.
func TestAPageTheRoundEndedKeepsItsCandidates(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 10; i++ {
		h.link.add(&fakeAlert{id: alertID(i), labels: map[string]string{"strategy_id": "41", "strategy_version": "3"}})
	}
	h.opened(t, "41", "3", nil, fingerprints(0, 10)...)
	ctx, cancel := context.WithCancel(context.Background())
	h.link.beforeClose = func(_ *fakeLink, request *http.Request) {
		// The round ends while the link works the page: the answer comes
		// after the caller stopped waiting, which the request's own context
		// ending says.
		cancel()
		select {
		case <-request.Context().Done():
		case <-testwait.Expired(t):
			t.Error("the caller never stopped waiting for the page")
		}
	}
	closer := h.closer(t)
	if word := closer.Inactive(ctx, testScope, "3", 3, fakeOpenedAt, testPeriod); word != OutcomeLinkFailed {
		t.Fatalf("word %s (stats %v)", word, nonZero(closer.Stats()))
	}
	if stats := closer.Stats(); stats[OutcomeItemPending] != 10 || stats[OutcomeItemPendingLost] != 0 {
		t.Fatalf("stats %v, want the ten candidates kept", nonZero(stats))
	}
	h.link.beforeClose = nil
	closer.RetryPending(context.Background(), 100)
	if size := closer.PendingSize(context.Background()); size != 0 {
		t.Fatalf("pending %d after the retry (stats %v)", size, nonZero(closer.Stats()))
	}
}
