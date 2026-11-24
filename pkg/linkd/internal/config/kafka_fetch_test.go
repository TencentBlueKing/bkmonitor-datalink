// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import "testing"

func TestKafkaFetchWaitDefaultsAndValidation(t *testing.T) {
	source := validEventSource()
	source = source.WithDefaults()
	if source.Storage.Kafka.FetchMaxWaitMilliseconds != 100 {
		t.Fatal("missing fetch wait default")
	}
	for _, n := range []int{-1, 1, 9, 5001} {
		source.Storage.Kafka.FetchMaxWaitMilliseconds = n
		if source.Storage.Kafka.validate() == nil {
			t.Errorf("accepted wait=%d", n)
		}
	}
	for _, n := range []int{10, 100, 5000} {
		source.Storage.Kafka.FetchMaxWaitMilliseconds = n
		if err := source.Storage.Kafka.validate(); err != nil {
			t.Error(err)
		}
	}
}
