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
			// The counts in one answer are what its body can hold at the
			// smallest a series and a record encode.
			wantSeries, wantRecords := uint64(test.wantBody)/config.MinimumSeriesBytes, uint64(test.wantBody)/config.MinimumRecordBytes
			if limits.MaxBodyBytes != test.wantBody || limits.MaxSeriesBytes != test.wantSeriesMax ||
				limits.MaxSeries != wantSeries || limits.MaxRecords != wantRecords {
				t.Fatalf("phaseTwoUQLimits(retained=%d) = %+v, want body=%d series_bytes=%d series=%d records=%d",
					test.retained, limits, test.wantBody, test.wantSeriesMax, wantSeries, wantRecords)
			}
			if _, err := accessuq.NewClientWithLimits("http://uq.invalid", "alarmd-test", &http.Client{}, limits); err != nil {
				t.Fatalf("derived limits rejected by the UQ client: %v", err)
			}
		})
	}
}
