// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"reflect"
	"testing"
	"time"
)

func TestComparatorServiceConfigFreezesThreeDistinctInputs(t *testing.T) {
	t.Parallel()

	config := ComparatorServiceConfig{
		Brokers:             []string{"127.0.0.1:9092"},
		DetectInputTopic:    "detect-input",
		GoDecisionTopic:     "go-decision",
		PythonDecisionTopic: "python-decision",
		GroupID:             "alarmd-comparator",
		ClientID:            "alarmd-comparator",
		BrokerVersion:       "2.6.0",
		MaxEntries:          1000,
		CoverageTimeout:     time.Minute,
		BarrierInterval:     time.Second,
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	want := []string{"detect-input", "go-decision", "python-decision"}
	if got := config.Topics(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Topics() = %v, want %v", got, want)
	}

	config.PythonDecisionTopic = config.GoDecisionTopic
	if err := config.Validate(); err == nil {
		t.Fatal("Validate() accepted duplicate input topics")
	}
}
