// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// StaticRouter 在一个配置前缀下为 Event、Alert 和 AlertLog 各使用一个固定索引。
// 它只用于存储契约测试；生产装配统一使用 BucketRouter 和 Manager。
type StaticRouter struct {
	eventIndex    string
	alertIndex    string
	alertLogIndex string
}

// NewStaticRouter 创建固定索引 Router。prefix 必须已经满足 Elasticsearch 小写索引命名约束。
func NewStaticRouter(prefix string) (*StaticRouter, error) {
	if strings.TrimSpace(prefix) != prefix || prefix == "" {
		return nil, fmt.Errorf("create elasticsearch static router: index prefix must not be empty")
	}
	router := &StaticRouter{
		eventIndex:    prefix + "-events",
		alertIndex:    prefix + "-alerts",
		alertLogIndex: prefix + "-alert-logs",
	}
	for _, target := range router.Targets() {
		if err := validateTarget("static index", target); err != nil {
			return nil, err
		}
	}
	return router, nil
}

// SchemaConfig 返回覆盖固定索引的 strict mapping 模板配置。
func (r *StaticRouter) SchemaConfig() SchemaConfig {
	return SchemaConfig{
		Event: TemplateSpec{
			Name: r.eventIndex, IndexPatterns: []string{r.eventIndex}, Priority: 100, Entity: entityEvent,
		},
		Alert: TemplateSpec{
			Name: r.alertIndex, IndexPatterns: []string{r.alertIndex}, Priority: 100, Entity: entityAlert,
		},
		AlertLog: TemplateSpec{
			Name: r.alertLogIndex, IndexPatterns: []string{r.alertLogIndex}, Priority: 100, Entity: entityAlertLog,
		},
	}
}

// Targets 返回启动时应幂等创建的全部固定索引。
func (r *StaticRouter) Targets() []string {
	return []string{r.eventIndex, r.alertIndex, r.alertLogIndex}
}

func (r *StaticRouter) EventRoute(context.Context, string) (Route, error) {
	return staticRoute(r.eventIndex), nil
}

func (r *StaticRouter) EventScanTargets(context.Context, time.Time) ([]string, error) {
	return []string{r.eventIndex}, nil
}

func (r *StaticRouter) EventRangeTargets(context.Context, time.Time, time.Time) ([]string, error) {
	return []string{r.eventIndex}, nil
}

func (r *StaticRouter) AlertRoute(context.Context, string) (Route, error) {
	return staticRoute(r.alertIndex), nil
}

func (r *StaticRouter) AlertHistoryRoute(context.Context, string) (Route, error) {
	return staticRoute(r.alertIndex), nil
}

func (r *StaticRouter) ActiveAlertTargets(context.Context) ([]string, error) {
	return []string{r.alertIndex}, nil
}

func (r *StaticRouter) TerminalAlertTargets(context.Context) ([]string, error) {
	return []string{r.alertIndex}, nil
}

func (r *StaticRouter) AlertLogWriteRoute(context.Context, string, string) (Route, error) {
	return staticRoute(r.alertLogIndex), nil
}

func (r *StaticRouter) AlertLogReadRoute(context.Context, string) (Route, error) {
	return staticRoute(r.alertLogIndex), nil
}

func staticRoute(index string) Route {
	return Route{WriteTarget: index, ReadTargets: []string{index}}
}

var _ Router = (*StaticRouter)(nil)
