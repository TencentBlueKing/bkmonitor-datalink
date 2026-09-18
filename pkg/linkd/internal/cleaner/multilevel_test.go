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
	"context"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/consume"
)

func TestCleanerValuesAndMappedEvaluationCollision(t *testing.T) {
	source := testSource()
	source.SeverityMapping = map[string]string{"P1": "critical", "P2": "warning", "P3": "warning"}
	mapper, err := NewMapper(source, config.DefaultSeverityConfig())
	if err != nil {
		t.Fatal(err)
	}
	message := consume.Message{ID: "record", TenantID: "tenant-1", EnqueuedAt: time.Now().UTC(), Body: []byte(`{"alert_id":"source-alert","values":{"cpu":92.5},"evaluations":[{"severity":"P1","action":"triggered"},{"severity":"P2","action":"resolved"}]}`)}
	event, err := mapper.MapMessage(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if event.Values["cpu"] != 92.5 || len(event.Evaluations) != 2 || event.Evaluations[0].Severity != "critical" {
		t.Fatalf("event=%+v", event)
	}
	message.Body = []byte(`{"alert_id":"source-alert","evaluations":[{"severity":"P2","action":"triggered"},{"severity":"P3","action":"resolved"}]}`)
	if _, err := mapper.MapMessage(context.Background(), message); err == nil {
		t.Fatal("mapped severity collision accepted")
	}
}
