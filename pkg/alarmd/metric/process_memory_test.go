// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"errors"
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// The process's memory reads as the resident set, the idle and released Go
// heap and the cgroup's anon and file charge, from cgroup v2 or v1; what
// cannot be read is absent, never zero, and
// counted as a failed read of its source.
func TestProcessMemoryReadsWhatTheContainerIsChargedFor(t *testing.T) {
	page := float64(os.Getpagesize())
	for _, test := range []struct {
		name     string
		files    map[string]string
		resident float64
		anon     float64
		file     float64
		cgroup   bool
	}{
		{name: "cgroup v2", files: map[string]string{"/proc/self/statm": "300 100 20 1 0 50 0\n",
			"/sys/fs/cgroup/memory.stat": "anon 4096\nfile 8192\nkernel 1\n"}, resident: 100 * page, anon: 4096, file: 8192, cgroup: true},
		{name: "cgroup v1", files: map[string]string{"/proc/self/statm": "300 7 20 1 0 50 0\n",
			"/sys/fs/cgroup/memory/memory.stat": "cache 512\nrss 256\n"}, resident: 7 * page, anon: 256, file: 512, cgroup: true},
		{name: "nothing readable", files: map[string]string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := newProcessMemoryCollector()
			c.read = func(name string) ([]byte, error) {
				if body, ok := test.files[name]; ok {
					return []byte(body), nil
				}
				return nil, errors.New("not here")
			}
			c.stat = func() (uint64, uint64) { return 3 << 20, 1 << 20 }
			registry := prometheus.NewRegistry()
			registry.MustRegister(c)
			families, err := registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			values := map[string][]*dto.Metric{}
			for _, family := range families {
				values[family.GetName()] = family.Metric
			}
			resident := values["alarmd_process_resident_bytes"]
			if test.resident == 0 && len(resident) != 0 || test.resident != 0 && (len(resident) != 1 || resident[0].GetGauge().GetValue() != test.resident) {
				t.Fatalf("resident %v, want %v", resident, test.resident)
			}
			if idle := values["alarmd_go_heap_idle_bytes"]; len(idle) != 1 || idle[0].GetGauge().GetValue() != 3<<20 {
				t.Fatalf("heap idle %v", idle)
			}
			if released := values["alarmd_go_heap_released_bytes"]; len(released) != 1 || released[0].GetGauge().GetValue() != 1<<20 {
				t.Fatalf("heap released %v", released)
			}
			failed := map[string]float64{}
			for _, m := range values["alarmd_process_memory_read_failed_total"] {
				failed[m.GetLabel()[0].GetValue()] = m.GetCounter().GetValue()
			}
			wantStatm, wantStat := 0.0, 0.0
			if test.resident == 0 {
				wantStatm = 1
			}
			if !test.cgroup {
				wantStat = 1
			}
			if len(failed) != 2 || failed["statm"] != wantStatm || failed["memory_stat"] != wantStat {
				t.Fatalf("read failures %v, want statm %v memory_stat %v", failed, wantStatm, wantStat)
			}
			kinds := map[string]float64{}
			for _, m := range values["alarmd_container_memory_stat_bytes"] {
				kinds[m.GetLabel()[0].GetValue()] = m.GetGauge().GetValue()
			}
			if !test.cgroup {
				if len(kinds) != 0 {
					t.Fatalf("an unreadable memory.stat reads %v, want absent", kinds)
				}
				return
			}
			if len(kinds) != 2 || kinds["anon"] != test.anon || kinds["file"] != test.file {
				t.Fatalf("cgroup kinds %v, want anon %v file %v", kinds, test.anon, test.file)
			}
		})
	}
}
