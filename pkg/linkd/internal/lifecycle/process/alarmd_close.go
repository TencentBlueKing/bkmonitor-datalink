// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich/rules"
)

// newAlarmdClosePreparation 只为显式配置地址的部署装配调用；空地址连身份读取都跳过。
// HTTP 客户端由该关闭入口复用，不自动重试或跟随重定向，也不携带 KAC/CLI 凭据。
func newAlarmdClosePreparation(resource *config.AlarmdResource, logger *slog.Logger) func(context.Context, domain.Alert, domain.AlertEndOperation) error {
	if resource == nil || resource.NoDataCloseEndpoint == "" {
		return nil
	}
	cfg := resource.WithDefaults()
	client := &http.Client{
		Timeout:       time.Duration(cfg.TimeoutMilliseconds) * time.Millisecond,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return func(ctx context.Context, alert domain.Alert, operation domain.AlertEndOperation) error {
		// 渠道和检测事实都来自原始 Alert，第三方来源或展示文案不能冒充 alarmd 无数据。
		if alert.EventSourceID != cfg.EventSourceID || !rules.ScalarProvided(alert.Dimensions[rules.FieldNoDataDimension]) {
			return nil
		}
		var family string
		if json.Unmarshal(alert.ExtraData["evaluation_family"], &family) != nil || family != "no_data" {
			return nil
		}
		switch operation.Source {
		case "manual", "auto_policy", "work_order", "self_heal", "system":
		default:
			return nil
		}
		fail := func(status int, code, message string, upstreamStatus int, cause error) error {
			if logger != nil {
				logger.WarnContext(ctx, "alarmd no-data close failed", "module", "lifecycle", "operation", "alarmd_no_data_close", "error", cause,
					"bk_tenant_id", alert.BKTenantID, "event_source_id", alert.EventSourceID, "alert_id", alert.AlertID,
					"operation_id", operation.ID, "upstream_status", upstreamStatus)
			}
			return &CloseError{Status: status, Code: code, Message: message, cause: cause}
		}
		strategy := rules.ScalarIdentity(alert.Labels["strategy_id"])
		strategyID, err := strconv.ParseInt(strategy, 10, 64)
		if err != nil || strategyID <= 0 || alert.SourceAlertID == "" || alert.BKTenantID == "" {
			return fail(400, "alarmd_identity_missing", "alarmd no-data close requires tenant, positive strategy_id and source_alert_id", 0,
				fmt.Errorf("original alert lacks a valid alarmd close identity"))
		}
		payload, err := json.Marshal(struct {
			TenantID      string `json:"bk_tenant_id"`
			StrategyID    string `json:"strategy_id"`
			SourceAlertID string `json:"source_alert_id"`
		}{alert.BKTenantID, strconv.FormatInt(strategyID, 10), alert.SourceAlertID})
		if err != nil {
			return fail(400, "invalid_command", "alarmd close request could not be encoded", 0, err)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.NoDataCloseEndpoint, bytes.NewReader(payload))
		if err != nil {
			return fail(502, "alarmd_close_failed", "alarmd no-data close failed; retry the same operation", 0, err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return fail(502, "alarmd_close_failed", "alarmd no-data close failed; retry the same operation", 0, err)
		}
		// 接口状态已确定记录结果，释放响应体失败不应触发再次记录关闭意图。
		defer func() { _ = response.Body.Close() }()
		// 200 仅确认意图已记录，无需解析正文；其他状态的响应正文不进入错误或日志。
		if response.StatusCode != http.StatusOK {
			return fail(502, "alarmd_close_failed", "alarmd no-data close failed; retry the same operation", response.StatusCode,
				fmt.Errorf("alarmd no-data close returned HTTP %d", response.StatusCode))
		}
		return nil
	}
}
