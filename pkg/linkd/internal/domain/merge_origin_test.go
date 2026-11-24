// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain_test

import (
	"strings"
	"testing"

	"linkd/internal/domain"
)

func TestMergeOriginIsReservedAndImmutableEventFact(t *testing.T) {
	event := validEvent()
	origin := &domain.MergeOrigin{OperationID: strings.Repeat("a", 64), Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("b", 64)}, WindowID: strings.Repeat("c", 64), MembersDigest: strings.Repeat("d", 64), MemberCount: 2, AlarmTags: []int64{2, 9}}
	event.MergeOrigin = origin
	if err := event.Validate(); err == nil {
		t.Fatal("external event forged internal origin")
	}
	event.EventSourceID = domain.BuiltinMergeEventSourceID
	normalized, err := event.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	copy := normalized.Clone()
	copy.MergeOrigin.MemberCount = 3
	if normalized.MergeOrigin.MemberCount != 2 {
		t.Fatal("origin clone shares pointer")
	}
	if err := domain.ValidateEventRedelivery(copy, normalized); err == nil {
		t.Fatal("origin changed on redelivery")
	}
	copy = normalized.Clone()
	copy.MergeOrigin = nil
	if err := copy.Validate(); err == nil {
		t.Fatal("builtin event missing origin accepted")
	}
}

func TestMergeOriginTagsAreBoundedAndCanonical(t *testing.T) {
	event := validEvent()
	event.EventSourceID = domain.BuiltinMergeEventSourceID
	event.MergeOrigin = &domain.MergeOrigin{OperationID: strings.Repeat("a", 64), Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("b", 64)}, WindowID: strings.Repeat("c", 64), MembersDigest: strings.Repeat("d", 64), MemberCount: 2}
	event.MergeOrigin.AlarmTags = []int64{}
	normalized, err := event.Normalize()
	if err != nil || normalized.MergeOrigin.AlarmTags != nil {
		t.Fatal("empty origin tags not normalized", err)
	}
	for _, tags := range [][]int64{{0}, {-1}, {1 << 53}, {2, 1}, {1, 1}, make([]int64, 129)} {
		event.MergeOrigin.AlarmTags = tags
		if err := event.Validate(); err == nil {
			t.Fatal("invalid origin tags accepted")
		}
	}
}
