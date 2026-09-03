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

	"linkd/internal/domain"
)

const bucketDateLayout = "20060102"

var bucketEpoch = time.Date(1970, time.January, 5, 0, 0, 0, 0, time.UTC)

// BucketConfig 定义时间桶周期、Event 允许的未来偏移和可选的新索引副本数。
type BucketConfig struct {
	EventBucketDays        int
	AlertHistoryBucketDays int
	AlertLogBucketDays     int
	MaxFutureSkew          time.Duration
	// NumberOfReplicas 非 nil 时写入模板，仅影响之后新建的索引。
	NumberOfReplicas *int
}

// BucketRouter 根据结构化 EventID/AlertID 把逻辑对象稳定路由到时间桶和 alias。
type BucketRouter struct {
	prefix                 string
	eventBucketDays        int
	alertBucketDays        int
	alertLogBucketDays     int
	maxFutureSkew          time.Duration
	numberOfReplicas       int
	replicaCountConfigured bool
	now                    func() time.Time
}

// NewBucketRouter 创建生产时间桶 Router。
func NewBucketRouter(prefix string, config BucketConfig) (*BucketRouter, error) {
	return newBucketRouter(prefix, config, time.Now)
}

func newBucketRouter(prefix string, config BucketConfig, now func() time.Time) (*BucketRouter, error) {
	if strings.TrimSpace(prefix) != prefix || prefix == "" {
		return nil, fmt.Errorf("create elasticsearch bucket router: index prefix must not be empty")
	}
	if config.EventBucketDays < 1 || config.AlertHistoryBucketDays < 1 || config.AlertLogBucketDays < 1 {
		return nil, fmt.Errorf("create elasticsearch bucket router: bucket days must be positive")
	}
	if config.MaxFutureSkew < 0 {
		return nil, fmt.Errorf("create elasticsearch bucket router: max future skew must not be negative")
	}
	if config.NumberOfReplicas != nil && *config.NumberOfReplicas < 0 {
		return nil, fmt.Errorf("create elasticsearch bucket router: number of replicas must not be negative")
	}
	if now == nil {
		return nil, fmt.Errorf("create elasticsearch bucket router: clock must not be nil")
	}
	router := &BucketRouter{
		prefix: prefix, eventBucketDays: config.EventBucketDays,
		alertBucketDays: config.AlertHistoryBucketDays, alertLogBucketDays: config.AlertLogBucketDays,
		maxFutureSkew: config.MaxFutureSkew, now: now,
	}
	if config.NumberOfReplicas != nil {
		router.numberOfReplicas = *config.NumberOfReplicas
		router.replicaCountConfigured = true
	}
	for _, target := range []string{
		router.eventReadAlias(), router.alertReadAlias(), router.activeAlertAlias(),
		router.alertHistoryReadAlias(), router.alertLogReadAlias(), router.activeAlertWriteAlias(),
	} {
		if err := validateTarget("bucket alias", target); err != nil {
			return nil, err
		}
	}
	return router, nil
}

// SchemaConfig 返回时间桶与 Active Alert 的模板配置。
func (r *BucketRouter) SchemaConfig() SchemaConfig {
	return SchemaConfig{
		Event: TemplateSpec{
			Name: r.prefix + "-events-template", IndexPatterns: []string{r.prefix + "-events-*"},
			Priority: 200, Settings: r.templateSettings(), Entity: entityEvent, Role: "bucket", BucketDays: r.eventBucketDays,
		},
		Alert: TemplateSpec{
			Name: r.prefix + "-alerts-active-template", IndexPatterns: []string{r.prefix + "-alerts-active-*"},
			Priority: 200, Settings: r.templateSettings(), Entity: entityAlert, Role: "active",
		},
		AlertHistory: TemplateSpec{
			Name: r.prefix + "-alert-history-template", IndexPatterns: []string{r.prefix + "-alert-history-*"},
			Priority: 200, Settings: r.templateSettings(), Entity: entityAlertHistory, Role: "history", BucketDays: r.alertBucketDays,
		},
		AlertLog: TemplateSpec{
			Name: r.prefix + "-alert-logs-template", IndexPatterns: []string{r.prefix + "-alert-logs-*"},
			Priority: 200, Settings: r.templateSettings(), Entity: entityAlertLog, Role: "bucket", BucketDays: r.alertLogBucketDays,
		},
	}
}

func (r *BucketRouter) templateSettings() map[string]any {
	if !r.replicaCountConfigured {
		return nil
	}
	return map[string]any{"number_of_replicas": r.numberOfReplicas}
}

func (r *BucketRouter) EventRoute(_ context.Context, eventID string) (Route, error) {
	parsed, err := domain.ParseEventID(eventID)
	if err != nil {
		return Route{}, err
	}
	if parsed.Timestamp.After(r.now().UTC().Add(r.maxFutureSkew)) {
		return Route{}, fmt.Errorf("event id timestamp exceeds allowed future skew: %s", parsed.Timestamp.Format(time.RFC3339))
	}
	alias := r.eventWriteAlias(parsed.Timestamp)
	return Route{WriteTarget: alias, ReadTargets: []string{alias}, RequireAlias: true}, nil
}

func (r *BucketRouter) EventScanTargets(context.Context, time.Time) ([]string, error) {
	return []string{r.eventReadAlias()}, nil
}

func (r *BucketRouter) EventRangeTargets(_ context.Context, receivedFrom, receivedTo time.Time) ([]string, error) {
	if receivedFrom.IsZero() || receivedTo.IsZero() || receivedTo.Before(receivedFrom) {
		return nil, fmt.Errorf("event range route requires an ordered received_at range")
	}
	start := bucketStart(receivedFrom, r.eventBucketDays)
	end := bucketStart(receivedTo, r.eventBucketDays)
	result := make([]string, 0, int(end.Sub(start)/(24*time.Hour))/r.eventBucketDays+1)
	for value := start; !value.After(end); value = value.AddDate(0, 0, r.eventBucketDays) {
		result = append(result, r.eventWriteAlias(value))
	}
	return result, nil
}

func (r *BucketRouter) AlertRoute(_ context.Context, alertID string) (Route, error) {
	parsed, err := domain.ParseAlertID(alertID)
	if err != nil {
		return Route{}, err
	}
	return Route{
		WriteTarget:  r.activeAlertWriteAlias(),
		ReadTargets:  []string{r.activeAlertAlias(), r.alertHistoryWriteAlias(parsed.Timestamp)},
		RequireAlias: true,
	}, nil
}

func (r *BucketRouter) AlertHistoryRoute(_ context.Context, alertID string) (Route, error) {
	parsed, err := domain.ParseAlertID(alertID)
	if err != nil {
		return Route{}, err
	}
	alias := r.alertHistoryWriteAlias(parsed.Timestamp)
	return Route{WriteTarget: alias, ReadTargets: []string{alias}, RequireAlias: true}, nil
}

func (r *BucketRouter) ActiveAlertTargets(context.Context) ([]string, error) {
	return []string{r.activeAlertAlias()}, nil
}

func (r *BucketRouter) TerminalAlertTargets(context.Context) ([]string, error) {
	return []string{r.alertReadAlias()}, nil
}

func (r *BucketRouter) AlertLogWriteRoute(_ context.Context, alertID, _ string) (Route, error) {
	parsed, err := domain.ParseAlertID(alertID)
	if err != nil {
		return Route{}, err
	}
	alias := r.alertLogWriteAlias(parsed.Timestamp)
	return Route{WriteTarget: alias, ReadTargets: []string{alias}, RequireAlias: true}, nil
}

func (r *BucketRouter) AlertLogReadRoute(_ context.Context, alertID string) (Route, error) {
	parsed, err := domain.ParseAlertID(alertID)
	if err != nil {
		return Route{}, err
	}
	alias := r.alertLogWriteAlias(parsed.Timestamp)
	return Route{WriteTarget: alias, ReadTargets: []string{alias}, RequireAlias: true}, nil
}

func (r *BucketRouter) eventReadAlias() string { return r.prefix + "-events" }

func (r *BucketRouter) alertReadAlias() string { return r.prefix + "-alerts" }

func (r *BucketRouter) activeAlertAlias() string { return r.prefix + "-alerts-active" }

func (r *BucketRouter) alertHistoryReadAlias() string { return r.prefix + "-alert-history" }

func (r *BucketRouter) alertLogReadAlias() string { return r.prefix + "-alert-logs" }

func (r *BucketRouter) activeAlertWriteAlias() string { return r.prefix + "-alerts-write" }

func (r *BucketRouter) activeAlertIndex() string { return r.prefix + "-alerts-active-000001" }

func (r *BucketRouter) eventIndex(value time.Time) string {
	return r.prefix + "-events-" + bucketStart(value, r.eventBucketDays).Format(bucketDateLayout)
}

func (r *BucketRouter) eventWriteAlias(value time.Time) string {
	return r.prefix + "-events-write-" + bucketStart(value, r.eventBucketDays).Format(bucketDateLayout)
}

func (r *BucketRouter) alertHistoryIndex(value time.Time) string {
	return r.prefix + "-alert-history-" + bucketStart(value, r.alertBucketDays).Format(bucketDateLayout)
}

func (r *BucketRouter) alertHistoryWriteAlias(value time.Time) string {
	return r.prefix + "-alert-history-write-" + bucketStart(value, r.alertBucketDays).Format(bucketDateLayout)
}

func (r *BucketRouter) alertLogIndex(value time.Time) string {
	return r.prefix + "-alert-logs-" + bucketStart(value, r.alertLogBucketDays).Format(bucketDateLayout)
}

func (r *BucketRouter) alertLogWriteAlias(value time.Time) string {
	return r.prefix + "-alert-logs-write-" + bucketStart(value, r.alertLogBucketDays).Format(bucketDateLayout)
}

func bucketStart(value time.Time, days int) time.Time {
	value = value.UTC()
	date := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	deltaDays := int(date.Sub(bucketEpoch) / (24 * time.Hour))
	quotient := deltaDays / days
	if deltaDays < 0 && deltaDays%days != 0 {
		quotient--
	}
	return bucketEpoch.AddDate(0, 0, quotient*days)
}

func validateBucketEventIdentity(router Router, event domain.Event) error {
	if _, ok := router.(*BucketRouter); !ok {
		return nil
	}
	parsed, err := domain.ParseEventID(event.EventID)
	if err != nil {
		return err
	}
	if parsed.BKTenantID != event.BKTenantID || parsed.EventSourceID != event.EventSourceID ||
		!parsed.Timestamp.Equal(event.ReceivedAt.UTC().Truncate(time.Second)) {
		return fmt.Errorf("event id route does not match bk_tenant_id, event_source_id, or received_at")
	}
	return nil
}

func validateBucketAlertIdentity(router Router, alert domain.Alert) error {
	if _, ok := router.(*BucketRouter); !ok {
		return nil
	}
	parsed, err := domain.ParseAlertID(alert.AlertID)
	if err != nil {
		return err
	}
	if parsed.BKTenantID != alert.BKTenantID || parsed.EventSourceID != alert.EventSourceID ||
		!parsed.Timestamp.Equal(alert.CreateAt.UTC().Truncate(time.Second)) {
		return fmt.Errorf("alert id route does not match bk_tenant_id, event_source_id, or create_at")
	}
	return nil
}

func validateBucketAlertReference(router Router, bkTenantID, alertID string) error {
	if _, ok := router.(*BucketRouter); !ok {
		return nil
	}
	parsed, err := domain.ParseAlertID(alertID)
	if err != nil {
		return err
	}
	if parsed.BKTenantID != bkTenantID {
		return fmt.Errorf("alert id route does not match bk_tenant_id")
	}
	return nil
}

var _ Router = (*BucketRouter)(nil)
