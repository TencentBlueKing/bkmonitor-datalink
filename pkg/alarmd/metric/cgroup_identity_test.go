// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// /metrics says which memcg and usage file the memory gauges read, and on
// v1 each counter another reader may be comparing against, so one read
// says where two tools' numbers part.
func TestTheMemoryGaugesSayWhichCgroupAndCounterTheyRead(t *testing.T) {
	files := map[string]string{
		"/proc/self/cgroup":                               "12:cpu,cpuacct:/kubepods/pod1/c1\n11:memory:/kubepods/burstable/pod1/c1\n",
		"/sys/fs/cgroup/memory/memory.usage_in_bytes":     "2254857830\n",
		"/sys/fs/cgroup/memory/memory.max_usage_in_bytes": "2400000000\n",
		"/sys/fs/cgroup/memory/memory.stat":               "cache 1100000000\nrss 1073741824\ntotal_rss 1080000000\n",
	}
	c := newProcessMemoryCollector()
	c.read = func(name string) ([]byte, error) {
		if body, ok := files[name]; ok {
			return []byte(body), nil
		}
		return nil, errors.New("not here")
	}
	c.stat = func() (uint64, uint64) { return 0, 0 }
	registry := prometheus.NewRegistry()
	registry.MustRegister(c)
	served := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(served, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := served.Body.String()
	for _, want := range []string{
		`alarmd_container_memory_cgroup_info{memory_cgroup="11:memory:/kubepods/burstable/pod1/c1",usage_file="/sys/fs/cgroup/memory/memory.usage_in_bytes",version="v1"} 1`,
		`alarmd_container_memory_v1_bytes{counter="rss"} 1.073741824e+09`,
		`alarmd_container_memory_v1_bytes{counter="total_rss"} 1.08e+09`,
		`alarmd_container_memory_v1_bytes{counter="usage_in_bytes"} 2.25485783e+09`,
		`alarmd_container_memory_v1_bytes{counter="max_usage_in_bytes"} 2.4e+09`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/metrics lacks %s:\n%s", want, body)
		}
	}
	// No swap accounting here: its counter is absent, not zero.
	if strings.Contains(body, `counter="memsw_usage_in_bytes"`) {
		t.Fatalf("/metrics serves memsw with no memsw file:\n%s", body)
	}
}

// On cgroup v2 the gauges say the unified file and the process's one line of
// /proc/self/cgroup, and serve no v1 counter: the v1 files are not read there.
func TestOnCgroupV2TheMemoryGaugesSayTheUnifiedFile(t *testing.T) {
	files := map[string]string{
		"/proc/self/cgroup":                           "0::/kubepods/burstable/pod1/c1\n",
		"/sys/fs/cgroup/memory.current":               "2254857830\n",
		"/sys/fs/cgroup/memory.stat":                  "anon 1073741824\nfile 1100000000\n",
		"/sys/fs/cgroup/memory/memory.usage_in_bytes": "1\n",
	}
	c := newProcessMemoryCollector()
	c.read = func(name string) ([]byte, error) {
		if body, ok := files[name]; ok {
			return []byte(body), nil
		}
		return nil, errors.New("not here")
	}
	c.stat = func() (uint64, uint64) { return 0, 0 }
	registry := prometheus.NewRegistry()
	registry.MustRegister(c)
	served := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(served, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := served.Body.String()
	if want := `alarmd_container_memory_cgroup_info{memory_cgroup="0::/kubepods/burstable/pod1/c1",usage_file="/sys/fs/cgroup/memory.current",version="v2"} 1`; !strings.Contains(body, want) {
		t.Fatalf("/metrics lacks %s:\n%s", want, body)
	}
	if strings.Contains(body, "alarmd_container_memory_v1_bytes{") {
		t.Fatalf("/metrics serves v1 counters on cgroup v2:\n%s", body)
	}
}
