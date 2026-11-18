// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// The url label of tsdb_request_seconds keeps the backend origin only, so an address that
// carries credentials never reaches the metric output.
func TestTsDBRequestSecondURLLabelKeepsOriginOnly(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"userinfo path and query", "http://user:fake-secret@192.0.2.10:8481/select/0/prometheus?token=fake-secret", "http://192.0.2.10:8481"},
		{"userinfo without scheme", "user:fake-secret@192.0.2.11:9200/_search", "192.0.2.11:9200"},
		{"origin unchanged", "http://192.0.2.12:9200", "http://192.0.2.12:9200"},
		{"host only unchanged", "192.0.2.13", "192.0.2.13"},
		{"grpc origin unchanged", "grpc://192.0.2.14", "grpc://192.0.2.14"},
		{"empty unchanged", "", ""},
		{"unparsable is a fixed value", "http://user:fake-secret/x@192.0.2.15", "[invalid URL]"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tsdbType := fmt.Sprintf("url_origin_test_%d", i)
			TsDBRequestSecond(context.Background(), time.Millisecond, tsdbType, c.raw)
			require.Equal(t, []string{c.want}, tsdbRequestURLLabels(t, tsdbType))
		})
	}
}

func tsdbRequestURLLabels(t *testing.T, tsdbType string) []string {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	var urls []string
	for _, family := range families {
		if family.GetName() != "unify_query_tsdb_request_seconds" {
			continue
		}
		for _, m := range family.GetMetric() {
			labels := make(map[string]string)
			for _, label := range m.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["tsdb_type"] == tsdbType {
				require.NotContains(t, labels["url"], "fake-secret")
				urls = append(urls, labels["url"])
			}
		}
	}
	return urls
}
