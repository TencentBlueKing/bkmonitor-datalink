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
	"reflect"
	"strings"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func runMergeOriginContract(t *testing.T, factory Factory) {
	t.Run("internal origin survives event projection and rejects external forgery", func(t *testing.T) {
		repo := factory(t)
		event := Event("tenant", "merge-origin", "merge-fp", "warning")
		event.EventSourceID = domain.BuiltinMergeEventSourceID
		event.MergeOrigin = &domain.MergeOrigin{OperationID: strings.Repeat("a", 64), Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("b", 64)}, WindowID: strings.Repeat("c", 64), MembersDigest: strings.Repeat("d", 64), MemberCount: 2, AlarmTags: []int64{2, 9}}
		created, err := repo.CreateEvent(t.Context(), event)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
		if err != nil || !reflect.DeepEqual(actual.Event.MergeOrigin, created.Event.MergeOrigin) {
			t.Fatal("origin missing from stored event", err)
		}
		if lifecycleReader, ok := repo.(store.LifecycleEventStore); ok {
			read, err := lifecycleReader.GetLifecycleEvent(t.Context(), event.BKTenantID, event.EventID)
			if err != nil || !reflect.DeepEqual(read.Event.MergeOrigin, event.MergeOrigin) {
				t.Fatal("lifecycle projection lost internal origin", err)
			}
		}
		bad := event.Clone()
		bad.MergeOrigin.MemberCount = 3
		if _, err := repo.CreateEvent(t.Context(), bad); err == nil {
			t.Fatal("internal origin overwritten on redelivery")
		}
		bad = event.Clone()
		bad.MergeOrigin.AlarmTags = []int64{3, 9}
		if _, err := repo.CreateEvent(t.Context(), bad); err == nil {
			t.Fatal("origin tags changed on redelivery")
		}
		bad = event.Clone()
		bad.EventID = "forgery"
		bad.EventSourceID = "external"
		if _, err := repo.CreateEvent(t.Context(), bad); err == nil {
			t.Fatal("external origin accepted")
		}
	})
}
