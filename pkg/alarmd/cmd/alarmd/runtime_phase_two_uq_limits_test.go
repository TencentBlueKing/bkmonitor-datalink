package main

import (
	"net/http"
	"testing"

	accessuq "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
)

// The UQ limits have no bound on a whole answer, however large the pool: an
// answer is read a value at a time, so only a series is bounded - by the
// pool, and by the per-series default - and the counts are what the pool
// holds at the smallest a series and a record encode.
func TestPhaseTwoUQLimitsBoundASeriesAndNotTheAnswer(t *testing.T) {
	defaults := accessuq.DefaultLimits()
	for _, test := range []struct {
		name          string
		retained      uint64
		wantSeriesMax int64
	}{
		{name: "a pool smaller than a series bounds the series", retained: 4 << 20, wantSeriesMax: 4 << 20},
		{name: "a 4 GiB container's pool", retained: 896 << 20, wantSeriesMax: defaults.MaxSeriesBytes},
		{name: "a 16 GiB container's pool", retained: 3584 << 20, wantSeriesMax: defaults.MaxSeriesBytes},
		{name: "the default pool", retained: config.Default().PhaseTwo.Coordinator.MaxRetainedBytes, wantSeriesMax: defaults.MaxSeriesBytes},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.PhaseTwo.Coordinator.MaxRetainedBytes = test.retained
			limits := phaseTwoUQLimits(cfg)
			wantSeries, wantRecords := test.retained/config.MinimumSeriesBytes, test.retained/config.MinimumRecordBytes
			if limits.MaxSeriesBytes != test.wantSeriesMax || limits.MaxSeries != wantSeries || limits.MaxRecords != wantRecords {
				t.Fatalf("phaseTwoUQLimits(retained=%d) = %+v, want series_bytes=%d series=%d records=%d",
					test.retained, limits, test.wantSeriesMax, wantSeries, wantRecords)
			}
			if _, err := accessuq.NewClientWithLimits("http://uq.invalid", "alarmd-test", &http.Client{}, limits); err != nil {
				t.Fatalf("derived limits rejected by the UQ client: %v", err)
			}
		})
	}
}
