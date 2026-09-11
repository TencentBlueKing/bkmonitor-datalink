// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import (
	"context"
	"errors"
	"testing"

	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
)

func TestObserveEnrichSourcesPreservesResults(t *testing.T) {
	t.Parallel()
	runtime, err := Start(context.Background(), Config{}, RoleLifecycle, "test")
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("reader failed")
	sources := runtime.ObserveEnrichSources(enrich.Sources{
		CWStrategy:  testCWStrategyReader{err: wantErr},
		Metric:      testMetricReader{},
		AlarmSource: testAlarmSourceReader{},
		OneModel:    testOneModelReader{},
	})
	if _, found, err := sources.CWStrategy.GetByBKStrategyID(context.Background(), "tenant", 1); found || !errors.Is(err, wantErr) {
		t.Fatalf("cw strategy found=%t err=%v", found, err)
	}
	if _, found, err := sources.Metric.FindMetricLibrary(context.Background(), models.MetricLibraryQuery{}); found || err != nil {
		t.Fatalf("metric found=%t err=%v", found, err)
	}
	if _, found, err := sources.OneModel.FindInstance(context.Background(), "tenant", enrich.InstanceQuery{}); !found || err != nil {
		t.Fatalf("instance found=%t err=%v", found, err)
	}
}

func TestEnrichDataSourceOutcome(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		found bool
		err   error
		want  string
	}{
		{name: "found", found: true, want: "found"},
		{name: "not found", want: "not_found"},
		{name: "failed", err: errors.New("failed"), want: "failed"},
		{name: "canceled", err: context.Canceled, want: "canceled"},
		{name: "invalid response", err: enrich.ErrInvalidDataSourceResponse, want: "invalid_response"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := enrichDataSourceOutcome(test.found, test.err); got != test.want {
				t.Fatalf("outcome=%q, want %q", got, test.want)
			}
		})
	}
}

type testCWStrategyReader struct{ err error }

func (r testCWStrategyReader) GetByBKStrategyID(context.Context, string, int64) (models.CWStrategy, bool, error) {
	return models.CWStrategy{}, false, r.err
}

type testMetricReader struct{}

func (testMetricReader) FindMetricLibrary(context.Context, models.MetricLibraryQuery) (models.MetricMetadata, bool, error) {
	return models.MetricMetadata{}, false, nil
}

type testOneModelReader struct{}

func (testOneModelReader) FindInstance(context.Context, string, enrich.InstanceQuery) (enrich.Instance, bool, error) {
	return enrich.Instance{}, true, nil
}
