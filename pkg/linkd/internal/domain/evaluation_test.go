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
	"fmt"
	"math"
	"reflect"
	"testing"

	"linkd/internal/domain"
)

func TestEventValuesValidationAndIsolation(t *testing.T) {
	for name, body := range map[string]string{"string": `{"cpu":"2"}`, "null": `{"cpu":null}`, "bool": `{"cpu":true}`, "nested": `{"cpu":{}}`, "array": `{"cpu":[]}`, "overflow": `{"cpu":1e999}`, "empty key": `{"":1}`} {
		t.Run(name, func(t *testing.T) {
			var values domain.EventValues
			if json.Unmarshal([]byte(body), &values) == nil {
				t.Fatal("invalid number accepted")
			}
		})
	}
	for _, number := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if (domain.EventValues{"cpu": number}).Validate() == nil {
			t.Fatal("non-finite value accepted")
		}
	}
	many := domain.EventValues{}
	for i := 0; i <= domain.MaxEventValues; i++ {
		many[fmt.Sprint(i)] = float64(i)
	}
	if many.Validate() == nil {
		t.Fatal("unbounded values accepted")
	}
	event := validEvent()
	event.Values = domain.EventValues{"cpu": 0, "load": 2.5}
	normalized, err := event.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	event.Values["load"] = 99
	if normalized.Values["load"] != 2.5 || normalized.Values["cpu"] != 0 {
		t.Fatal("values lost or aliased")
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip domain.Event
	if err := json.Unmarshal(encoded, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalized.Values, roundtrip.Values) {
		t.Fatal("values round trip changed")
	}
	altered := normalized.Clone()
	altered.Values["load"] = 3
	if domain.ValidateEventReplacement(normalized, altered) == nil {
		t.Fatal("stored event values were mutable")
	}
}

func TestEvaluationsCanonicalizeOrderAndRejectDuplicates(t *testing.T) {
	event := validEvent()
	event.Evaluations = []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionTriggered}, {Severity: "critical", Action: domain.EventActionResolved}}
	normalized, err := event.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	reversed := event.Clone()
	reversed.Evaluations[0], reversed.Evaluations[1] = reversed.Evaluations[1], reversed.Evaluations[0]
	reversed, err = reversed.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalized, reversed) {
		t.Fatal("evaluation order changed source identity")
	}
	event.Evaluations[0].Severity = "info"
	if normalized.Evaluations[1].Severity != "warning" {
		t.Fatal("evaluations aliased")
	}
	for name, items := range map[string][]domain.EventEvaluation{"empty": nil, "duplicate": {{Severity: "warning", Action: domain.EventActionTriggered}, {Severity: "warning", Action: domain.EventActionResolved}}, "invalid action": {{Severity: "warning", Action: "bad"}}} {
		t.Run(name, func(t *testing.T) {
			candidate := validEvent()
			candidate.Evaluations = items
			if candidate.Validate() == nil {
				t.Fatal("invalid evaluations accepted")
			}
		})
	}
}
