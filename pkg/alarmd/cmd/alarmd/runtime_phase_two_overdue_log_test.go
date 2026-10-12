// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// An object whose reads are held longer than its period is overdue every
// Slot by design, at INFO; one overdue with no hold, or a hold not known, has
// nothing to explain it and is WARN. Each beginning is counted either way.
func TestAnOverdueEpisodeHeldByItsReadHoldIsInfoAndAnUnexplainedOneIsWarn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		episode fleet.OverdueEpisode
		level   string
	}{
		{"held longer than its period", fleet.OverdueEpisode{ReadHoldKnown: true, ReadHoldMillis: 117_573}, "INFO"},
		{"no hold", fleet.OverdueEpisode{ReadHoldKnown: true}, "WARN"},
		{"hold not known", fleet.OverdueEpisode{}, "WARN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			observe := overdueEpisodeObserver(observability.New("alarmd", &output), metric.NewRecorder(metric.BuildInfo{}))
			episode := tc.episode
			episode.QueryGroup, episode.DueAt, episode.IntervalSeconds = "qg-a", time.Unix(1_000, 0), 30
			observe(episode, true)
			observe(episode, false)
			lines := strings.Split(strings.TrimSpace(output.String()), "\n")
			if len(lines) != 2 {
				t.Fatalf("lines=%d, want the beginning and the end: %s", len(lines), output.String())
			}
			for _, raw := range lines {
				var line map[string]any
				if err := json.Unmarshal([]byte(raw), &line); err != nil {
					t.Fatal(err)
				}
				if line["level"] != tc.level || line["hold"] != episode.HoldClass() {
					t.Fatalf("line=%v, want %s with hold %s", line, tc.level, episode.HoldClass())
				}
			}
		})
	}
}
