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
		CWStrategy: testCWStrategyReader{err: wantErr}, Business: testBusinessReader{},
		Metric: testMetricReader{}, Model: testModelReader{}, AlarmSource: testAlarmSourceReader{},
		OneModel: testOneModelReader{}, CollectConfig: testCollectConfigReader{},
		CollectTopology: testCollectTopologyReader{}, Uptime: testUptimeReader{}, UptimeNode: testUptimeNodeReader{},
	})
	if _, found, err := sources.CWStrategy.GetByBKStrategyID(context.Background(), "tenant", 1); found || !errors.Is(err, wantErr) {
		t.Fatalf("cw strategy found=%t err=%v", found, err)
	}
	if isGlobal, found, err := sources.Business.IsGlobalBusiness(context.Background(), "tenant", 1); !isGlobal || !found || err != nil {
		t.Fatalf("business is_global=%t found=%t err=%v", isGlobal, found, err)
	}
	if _, found, err := sources.Metric.FindMetricLibrary(context.Background(), models.MetricLibraryQuery{}); found || err != nil {
		t.Fatalf("metric found=%t err=%v", found, err)
	}
	if _, found, err := sources.Model.GetModelByCode(context.Background(), "tenant", "cw-Host"); !found || err != nil {
		t.Fatalf("model found=%t err=%v", found, err)
	}
	if _, found, err := sources.CollectConfig.GetCollectConfig(context.Background(), "tenant", "collect-1"); !found || err != nil {
		t.Fatalf("collect config found=%t err=%v", found, err)
	}
	if _, found, err := sources.CollectTopology.FindHostTopology(context.Background(), "tenant", "101"); !found || err != nil {
		t.Fatalf("topology found=%t err=%v", found, err)
	}
	if _, found, err := sources.Uptime.GetUptimeTask(context.Background(), "tenant", "7"); !found || err != nil {
		t.Fatalf("uptime found=%t err=%v", found, err)
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
		{name: "timeout", err: context.DeadlineExceeded, want: "canceled"},
		{name: "injected failure", err: enrich.ErrInjectedTestFailure, want: "failed"},
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

type testBusinessReader struct{}

func (testBusinessReader) IsGlobalBusiness(context.Context, string, int64) (bool, bool, error) {
	return true, true, nil
}

type testMetricReader struct{}

func (testMetricReader) FindMetricLibrary(context.Context, models.MetricLibraryQuery) (models.MetricMetadata, bool, error) {
	return models.MetricMetadata{}, false, nil
}

type testModelReader struct{}

func (testModelReader) GetModelByCode(context.Context, string, string) (enrich.Model, bool, error) {
	return enrich.Model{}, true, nil
}

type testOneModelReader struct{}

func (testOneModelReader) FindInstance(context.Context, string, enrich.InstanceQuery) (enrich.Instance, bool, error) {
	return enrich.Instance{}, true, nil
}

type testCollectConfigReader struct{}

func (testCollectConfigReader) GetCollectConfig(context.Context, string, string) (models.CollectConfig, bool, error) {
	return models.CollectConfig{}, true, nil
}

type testCollectTopologyReader struct{}

func (testCollectTopologyReader) FindRelatedHost(context.Context, string, string, string, string) (enrich.Instance, bool, error) {
	return enrich.Instance{}, true, nil
}

func (testCollectTopologyReader) FindHostTopology(context.Context, string, string) (models.ResourceTopology, bool, error) {
	return models.ResourceTopology{}, true, nil
}

type testUptimeReader struct{}

func (testUptimeReader) GetUptimeTask(context.Context, string, string) (models.UptimeTask, bool, error) {
	return models.UptimeTask{}, true, nil
}

type testUptimeNodeReader struct{}

func (testUptimeNodeReader) GetUptimeNode(context.Context, string, string) (models.UptimeNode, bool, error) {
	return models.UptimeNode{}, true, nil
}
