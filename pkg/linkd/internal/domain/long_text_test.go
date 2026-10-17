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
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"linkd/internal/domain"
	"linkd/internal/jsonpath"
)

func TestDisplayTextTruncatesAtTwoThousandCharactersAcrossDomainAndEnrich(t *testing.T) {
	for _, unit := range []string{"a", "警", "🚨"} {
		for _, count := range []int{500, 2000, 2001, 1 << 20} {
			input := strings.Repeat(unit, count)
			expected := strings.Repeat(unit, min(count, 2000))
			event, alert := validEvent(), validAlert()
			event.Title, event.SubjectName = input, input
			alert.Title, alert.SubjectName = input, input
			normalizedEvent, err := event.Normalize()
			if err != nil || normalizedEvent.Title != expected || normalizedEvent.SubjectName != expected {
				t.Fatal("event display text not truncated at character boundary", err)
			}
			normalizedAlert, err := alert.Normalize()
			if err != nil || normalizedAlert.Title != expected || normalizedAlert.SubjectName != expected {
				t.Fatal("alert display text not truncated at character boundary", err)
			}
			for _, path := range []string{"$.title", "$.subject_name"} {
				target, _ := jsonpath.ParseTarget(path)
				assignment, err := domain.NewEnrichAssignment(target, input)
				if err != nil {
					t.Fatal(err)
				}
				patch, err := assignment.Patch()
				if err != nil || patch.Validate() != nil {
					t.Fatal("persisted patch rejected", err)
				}
				var stored string
				if err := json.Unmarshal(patch.Value, &stored); err != nil || stored != expected || !utf8.ValidString(stored) {
					t.Fatal("patch lost the bounded Unicode text", err)
				}
				replay, err := domain.PrepareEnrichPatch(patch)
				if err != nil {
					t.Fatal(err)
				}
				again, err := replay.Patch()
				if err != nil || string(again.Value) != string(patch.Value) {
					t.Fatal("retry changed text", err)
				}
			}
		}
	}
}
