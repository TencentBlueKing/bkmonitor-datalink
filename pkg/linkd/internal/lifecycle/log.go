// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"linkd/internal/domain"
)

func eventAlertLog(event domain.Event, alert domain.Alert, operation domain.OperationKind,
	reasonCode string, createdAt time.Time, extra map[string]string) (domain.AlertLog, error) {
	values := map[string]string{"event_id": event.EventID, "reason_code": reasonCode}
	for key, value := range extra {
		values[key] = value
	}
	params, err := encodeLogParams(values)
	if err != nil {
		return domain.AlertLog{}, err
	}
	return domain.AlertLog{
		LogID: eventAlertLogID(event, alert.AlertID, operation), BKTenantID: event.BKTenantID,
		AlertID: alert.AlertID, OperatorKind: domain.OperatorKindSource, OperationKind: operation,
		Params: params, CreatedTime: createdAt,
	}, nil
}

func operationCloseLog(command CloseAlertCommand, alert domain.Alert) (domain.AlertLog, error) {
	params, err := encodeLogParams(map[string]string{
		"operation_id": command.OperationID, "operator_id": command.OperatorID, "reason": command.Reason,
	})
	if err != nil {
		return domain.AlertLog{}, err
	}
	return domain.AlertLog{
		LogID: operationAlertLogID(command), BKTenantID: command.BKTenantID, AlertID: command.AlertID,
		OperatorKind: command.OperatorKind, OperationKind: domain.OperationKindClose,
		Params: params, CreatedTime: alert.UpdateAt,
	}, nil
}

func finalHookLog(cause AlertChangeCause, alert domain.Alert, outcome ProcessOutcome,
	result FinalHookResult, reasonCode string) (domain.AlertLog, error) {
	params, err := encodeLogParams(map[string]string{
		"cause_type": cause.Type, "cause_id": cause.ID, "reason_code": reasonCode,
		"hook_name": result.Name, "transport": result.Transport, "destination": result.Destination,
		"message_id": result.MessageID, "lifecycle_outcome": string(outcome),
	})
	if err != nil {
		return domain.AlertLog{}, err
	}
	return domain.AlertLog{
		LogID: finalHookLogID(cause, alert, outcome, result, reasonCode), BKTenantID: alert.BKTenantID,
		AlertID: alert.AlertID, OperatorKind: domain.OperatorKindSystem, OperationKind: domain.OperationKindPush,
		Params: params, CreatedTime: alert.UpdateAt,
	}, nil
}

// appendAlertLogs 在一次 lifecycle 决策结束前统一提交全部日志。
// 任一逐项失败都阻止后续 Event CAS；已经成功的 create 由稳定 log_id 在重试时幂等吸收。
func (p *Processor) appendAlertLogs(ctx context.Context, logs []domain.AlertLog) error {
	if len(logs) == 0 {
		return nil
	}
	results, err := p.repository.AppendAlertLogs(ctx, logs)
	if err != nil {
		return fmt.Errorf("append %d lifecycle alert logs: %w", len(logs), err)
	}
	if len(results) != len(logs) {
		return fmt.Errorf("append lifecycle alert logs returned %d items for %d logs", len(results), len(logs))
	}
	for index, result := range results {
		if result.Err != nil {
			return fmt.Errorf("append lifecycle alert log at index %d: %w", index, result.Err)
		}
	}
	return nil
}

func encodeLogParams(values map[string]string) (domain.JSONObject, error) {
	params := make(domain.JSONObject, len(values))
	for key, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode alert log %s: %w", key, err)
		}
		params[key] = encoded
	}
	return params, nil
}
