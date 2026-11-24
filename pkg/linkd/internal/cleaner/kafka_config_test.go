// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"testing"
	"time"

	"linkd/internal/config"
)

func TestKafkaConfigPropagatesFetchWait(t *testing.T) {
	source := config.EventSource{}
	if got := kafkaConfig(source).FetchMaxWait; got != 100*time.Millisecond {
		t.Fatalf("default wait=%s", got)
	}
	source.Storage.Kafka.FetchMaxWaitMilliseconds = 5000
	if got := kafkaConfig(source).FetchMaxWait; got != 5*time.Second {
		t.Fatalf("explicit wait=%s", got)
	}
}
