// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storetest

import (
	"errors"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func runSourceEventsContract(t *testing.T, factory Factory) {
	t.Run("source event window without alerts", func(t *testing.T) {
		repo := factory(t)
		first := Event("tenant-1", "window-a", "fp-a", "warning")
		second := Event("tenant-1", "window-b", "fp-b", "warning")
		foreign := Event("tenant-2", "window-a", "fp-a", "warning")
		other := Event("tenant-1", "window-other", "fp-o", "warning")
		other.EventSourceID = "other"
		late := Event("tenant-1", "window-late", "fp-l", "warning")
		late.ReceivedAt = first.ReceivedAt.Add(2 * time.Hour)
		for _, event := range []domain.Event{first, second, foreign, other, late} {
			if _, err := repo.CreateEvent(t.Context(), event); err != nil {
				t.Fatal(err)
			}
		}
		request := store.EventBySourceRequest{ReceivedFrom: first.ReceivedAt, ReceivedTo: first.ReceivedAt.Add(time.Hour), Page: store.PageRequest{Limit: 1}}
		var page store.EventPage
		var err error
		deadline := time.Now().Add(3 * time.Second)
		for {
			page, err = repo.ListEventsBySource(t.Context(), "tenant-1", "source", request)
			if err == nil && len(page.Events) == 1 && page.NextCursor != "" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("first page=%+v err=%v", page, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if page.Events[0].Event.EventID != "window-a" {
			t.Fatalf("first=%+v", page)
		}
		request.Page.Cursor = page.NextCursor
		if _, err := repo.ListEventsBySource(t.Context(), "tenant-2", "source", request); !errors.Is(err, store.ErrInvalidCursor) {
			t.Fatalf("foreign tenant cursor=%v", err)
		}
		if _, err := repo.ListEventsBySource(t.Context(), "tenant-1", "other", request); !errors.Is(err, store.ErrInvalidCursor) {
			t.Fatalf("foreign source cursor=%v", err)
		}
		page, err = repo.ListEventsBySource(t.Context(), "tenant-1", "source", request)
		if err != nil || len(page.Events) != 1 || page.Events[0].Event.EventID != "window-b" || page.NextCursor != "" {
			t.Fatalf("last page=%+v err=%v", page, err)
		}
	})
}
