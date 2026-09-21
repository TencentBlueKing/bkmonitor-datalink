// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"net/http"
	"testing"

	accessuq "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
)

// The UQ body cap follows the retained budget only up to 512 MiB: raising the
// retained budget to gigabytes must not permit multi-gigabyte single responses.
// Series, record and per-series byte limits keep their derivation.
func TestPhaseTwoUQLimitsCapBodyIndependentlyOfRetainedBudget(t *testing.T) {
	const cap = int64(512 << 20)
	defaults := accessuq.DefaultLimits()
	for _, test := range []struct {
		name          string
		retained      uint64
		wantBody      int64
		wantSeriesMax int64
	}{
		{name: "small retained budget bounds body and series bytes", retained: 4 << 20, wantBody: 4 << 20, wantSeriesMax: 4 << 20},
		{name: "legacy retained budget", retained: 64 << 20, wantBody: 64 << 20, wantSeriesMax: defaults.MaxSeriesBytes},
		{name: "default retained budget", retained: config.Default().PhaseTwo.Coordinator.MaxRetainedBytes, wantBody: int64(config.Default().PhaseTwo.Coordinator.MaxRetainedBytes), wantSeriesMax: defaults.MaxSeriesBytes},
		{name: "retained budget at the cap", retained: uint64(cap), wantBody: cap, wantSeriesMax: defaults.MaxSeriesBytes},
		{name: "gigabyte retained budget is capped", retained: 4 << 30, wantBody: cap, wantSeriesMax: defaults.MaxSeriesBytes},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.PhaseTwo.Coordinator.MaxRetainedBytes = test.retained
			limits := phaseTwoUQLimits(cfg)
			wantRecords := cfg.PhaseTwo.Coordinator.MaxSeries * cfg.Limits.Detect.MaxRecordsPerSeries
			if limits.MaxBodyBytes != test.wantBody || limits.MaxSeriesBytes != test.wantSeriesMax ||
				limits.MaxSeries != cfg.PhaseTwo.Coordinator.MaxSeries || limits.MaxRecords != wantRecords {
				t.Fatalf("phaseTwoUQLimits(retained=%d) = %+v, want body=%d series_bytes=%d series=%d records=%d",
					test.retained, limits, test.wantBody, test.wantSeriesMax, cfg.PhaseTwo.Coordinator.MaxSeries, wantRecords)
			}
			if _, err := accessuq.NewClientWithLimits("http://uq.invalid", "alarmd-test", &http.Client{}, limits); err != nil {
				t.Fatalf("derived limits rejected by the UQ client: %v", err)
			}
		})
	}
}
