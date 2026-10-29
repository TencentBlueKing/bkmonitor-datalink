// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

// A stop says, as its drain begins, how many Slots it waits on - written
// whatever the repeated-line budget has spent, though its result is a
// routine start - and says nothing yet of how the wait ends: the shutdown
// line says that, and a process killed before it has only this one.
func TestAStopsDrainIsWrittenAsItBegins(t *testing.T) {
	var output bytes.Buffer
	observer := withheldObserver(t, &output)
	for stop := 0; stop < 2; stop++ {
		output.Reset()
		observer.Observe(context.Background(), Observation{Component: ComponentRuntime, Stage: StageShutdownDrainBegan,
			Result: ResultStarted, SlotDrain: &SlotDrainFacts{Site: SlotDrainSiteShutdown, Waited: 3}})
		var event map[string]any
		if err := json.Unmarshal(output.Bytes(), &event); err != nil {
			t.Fatalf("stop %d: %v; log=%q", stop, err, output.String())
		}
		if event["stage"] != StageShutdownDrainBegan || event["drain_site"] != SlotDrainSiteShutdown || event["drain_waited"] != float64(3) {
			t.Fatalf("stop %d: line %#v, want the shutdown's drain begun on 3 Slots", stop, event)
		}
		for _, ended := range []string{"drain_outcome", "drain_cancelled", "drain_wait_ms"} {
			if _, said := event[ended]; said {
				t.Fatalf("stop %d: line %#v says %s before the drain has ended", stop, event, ended)
			}
		}
	}
}
