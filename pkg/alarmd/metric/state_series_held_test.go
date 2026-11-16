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
	"strings"
	"testing"
)

// The series held are read at scrape time from the source the replica binds,
// and are zero before it is bound.
func TestTheSeriesHeldAreReadFromTheirSourceAtScrape(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	if scraped := scrape(t, recorder); !strings.Contains(scraped, "alarmd_state_series_held 0") {
		t.Fatalf("series held before a source is bound is not zero:\n%s", scraped)
	}
	held := uint64(42)
	recorder.SetStateSeriesHeldSource(func() uint64 { return held })
	if scraped := scrape(t, recorder); !strings.Contains(scraped, "alarmd_state_series_held 42") {
		t.Fatalf("series held = not 42 from the source:\n%s", scraped)
	}
	held = 7
	if scraped := scrape(t, recorder); !strings.Contains(scraped, "alarmd_state_series_held 7") {
		t.Fatalf("series held not read again at scrape:\n%s", scraped)
	}
}
