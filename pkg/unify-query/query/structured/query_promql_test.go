// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"context"
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func TestQueryPromQLExpr(t *testing.T) {
	log.InitTestLogger()

	testCases := map[string]struct {
		q string
		r string
	}{
		"q1": {
			q: `sum(floor(rate(custom:usage{}[1m]))) by (bk_biz_id)`,
			r: `sum by (bk_biz_id) (floor(rate(custom:usage[1m])))`,
		},
		"q2": {
			q: `sum(rate(usage{}[1m])) by (bk_biz_id)`,
			r: `sum by (bk_biz_id) (rate(bkmonitor:usage[1m]))`,
		},
		"q3": {
			q: `sum(floor(rate(usage{}[10s:20s]))) by (bk_biz_id)`,
			r: `sum by (bk_biz_id) (floor(rate(bkmonitor:usage[10s:20s])))`,
		},
		"q4": {
			q: `sum(floor(rate(usage{tag="value"}[10s:20s] @ end() ))) by (bk_biz_id)`,
			r: `sum by (bk_biz_id) (floor(rate(bkmonitor:usage{tag="value"}[10s:20s] @ end())))`,
		},
		"q5": {
			q: `sum(rate(usage{}[1m] @ end() )) by (bk_biz_id)`,
			r: `sum by (bk_biz_id) (rate(bkmonitor:usage[1m] @ end()))`,
		},
		"test @ modifier range-vector": {
			q: `sum(rate(metric_good_1{label="value"}[1m] @end()))`,
			r: `sum(rate(bkmonitor:metric_good_1{label="value"}[1m] @ end()))`,
		},
		"test @ modifier vector": {
			q: `topk(3, metric_good_1 @1609746000)`,
			r: `topk(3, bkmonitor:metric_good_1 @ 1609746000.000)`,
		},
		"test chinese and jisuan": {
			q: `topk(10, ((sum(bkmonitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_total{world_name="Dev \u670d"}) by (proto_name) - sum(bkmonitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_total{world_name="Dev \u670d"} offset 5m) by (proto_name)) / sum(bkmonitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_role_num_5m{world_name="Dev \u670d"}) by (proto_name)))`,
			r: `topk(10, ((sum by (proto_name) (bkmonitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_total{world_name="Dev 服"}) - sum by (proto_name) (bkmonitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_total{world_name="Dev 服"} offset 5m)) / sum by (proto_name) (bkmonitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_role_num_5m{world_name="Dev 服"})))`,
		},
		"test chinese": {
			q: `sum(count_over_time(bk_monitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_total{world_name=~"Dev 服"}[1m]))`,
			r: `sum(count_over_time(bk_monitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_total{world_name=~"Dev 服"}[1m]))`,
		},
		"test chinese with count sum": {
			q: `1 / count(metric_good_1{world_name="Dev 中文验证"} == 1) + sum(metric_bad_2222{a=~"你瞅啥??"})`,
			r: `1 / count(bkmonitor:metric_good_1{world_name="Dev 中文验证"} == 1) + sum(bkmonitor:metric_bad_2222{a=~"你瞅啥??"})`,
		},
		"group right and vector": {
			q: `(100 - (sum(rate(a[1m])) / on(ip) group_right() sum(rate(a[1m]))) * 100) OR on() vector(100)`,
			r: `(100 - (sum(rate(bkmonitor:a[1m])) / on (ip) group_right () sum(rate(bkmonitor:a[1m]))) * 100) or on () vector(100)`,
		},
		"on vector(0)": {
			q: `(100 - (sum(rate(a[1m]))/sum(rate(a[1m]))) * 100) OR on() vector(100)`,
			r: `(100 - (sum(rate(bkmonitor:a[1m])) / sum(rate(bkmonitor:a[1m]))) * 100) or on () vector(100)`,
		},
		"group": {
			q: `group(custom:datalabel:container_cpu_load_average_10s)`,
			r: `group(custom:datalabel:container_cpu_load_average_10s)`,
		},
		"std var": {
			q: `stdvar(datalabel:container_cpu_load_average_10s{tag="2"}) by (pod)`,
			r: `stdvar by (pod) (bkmonitor:datalabel:container_cpu_load_average_10s{tag="2"})`,
		},
		"std dev": {
			q: `stddev(container_cpu_load_average_10s{tag!="2"}) without (pod)`,
			r: `stddev without (pod) (bkmonitor:container_cpu_load_average_10s{tag!="2"})`,
		},
		"topk": {
			q: `topk(5, container_cpu_load_average_10s) by (tag)`,
			r: `topk by (tag) (5, bkmonitor:container_cpu_load_average_10s)`,
		},
		"count values": {
			q: `count_values("pod", container_cpu_load_average_10s) by (tag)`,
			r: `count_values by (tag) ("pod", bkmonitor:container_cpu_load_average_10s)`,
		},
		"avg without": {
			q: `avg(container_cpu_load_average_10s{container=~"alertmanager"}) without (condition)`,
			r: `avg without (condition) (bkmonitor:container_cpu_load_average_10s{container=~"alertmanager"})`,
		},
		"avg by and without": {
			q: `sum(avg without (pod) (container_cpu_load_average_10s{container=~"alertmanager"})) by (condition)`,
			r: `sum by (condition) (avg without (pod) (bkmonitor:container_cpu_load_average_10s{container=~"alertmanager"}))`,
		},
		"avg avg_over_time": {
			q: `avg by (tag1, tag2) (avg_over_time(bkmonitor:metric{tag!="abc"}[1m]))`,
			r: `avg by (tag1, tag2) (avg_over_time(bkmonitor:metric{tag!="abc"}[1m]))`,
		},
		"sum count_over_time": {
			q: `sum(count_over_time(bkmonitor:db:metric{tag!="abc"}[1m])) by (tag1, tag2)`,
			r: `sum by (tag1, tag2) (count_over_time(bkmonitor:db:metric{tag!="abc"}[1m]))`,
		},
		"many func": {
			q: `sum(label_join(round(quantile_over_time(0.9, container_cpu_load_average_10s[1m]), 100), "pod1", "pod2", "pod3")) by (pod1, pod2) + histogram_quantile(0.5, count(irate(container_cpu_load_average_10s[1m])) by (pod1, pod2))`,
			r: `sum by (pod1, pod2) (label_join(round(quantile_over_time(0.9, bkmonitor:container_cpu_load_average_10s[1m]), 100), "pod1", "pod2", "pod3")) + histogram_quantile(0.5, count by (pod1, pod2) (irate(bkmonitor:container_cpu_load_average_10s[1m])))`,
		},
		"avg rate": {
			q: `avg by (tag1, tag2) (avg_over_time(bkmonitor:metric{tag!="abc"}[15s:15s]))`,
			r: `avg by (tag1, tag2) (avg_over_time(bkmonitor:metric{tag!="abc"}[15s:15s]))`,
		},
		"sum with special": {
			q: `avg by (__ext__bk_46__container) (avg_over_time(bkmonitor:metric__bk_46__container{tag__bk_46__container!="abc__bk_46__container"}[15s:15s]))`,
			r: `avg by (__ext__bk_46__container) (avg_over_time(bkmonitor:metric__bk_46__container{tag__bk_46__container!="abc__bk_46__container"}[15s:15s]))`,
		},
	}

	for n, c := range testCases {
		t.Run(n, func(t *testing.T) {
			sp := NewQueryPromQLExpr(c.q)
			ts, err := sp.QueryTs()
			assert.Nil(t, err)
			if ts != nil {
				promExprOpt := &PromExprOption{}

				promExprOpt.ReferenceNameMetric = make(map[string]string, len(ts.QueryList))
				promExprOpt.ReferenceNameLabelMatcher = make(map[string][]*labels.Matcher, len(ts.QueryList))
				for _, q := range ts.QueryList {
					router, _ := q.ToRouter()
					promExprOpt.ReferenceNameMetric[q.ReferenceName] = router.RealMetricName()
					labelsMatcher, _, _ := q.Conditions.ToProm()
					promExprOpt.ReferenceNameLabelMatcher[q.ReferenceName] = labelsMatcher
				}

				result, err := ts.ToPromExpr(context.TODO(), promExprOpt)
				assert.Nil(t, err)
				assert.Equal(t, c.r, result.String())
			}
		})
	}
}
