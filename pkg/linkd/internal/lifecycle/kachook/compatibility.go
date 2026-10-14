// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kachook

import (
	"encoding/json"

	"github.com/google/uuid"
	"linkd/internal/domain"
	"linkd/internal/enrich/kingeye"
	"linkd/internal/enrich/view"
	"linkd/internal/lifecycle"
)

// CompatibilityPayload 在固定协议字段之外按冻结的 KAC 字段目录输出有效自定义字段。
// 受保护字段在展开前校验；结果只用于兼容存储，不构成处置准入。
func CompatibilityPayload(a domain.Alert, level func(string) (string, error)) ([]byte, error) {
	message, err := CompatibilityMessage(a, level)
	if err != nil {
		return nil, err
	}
	return mappedPayload(message, a, nil)
}

// CompatibilityMessage 复用 KAC 字段转换，但使用一个 Alert 一条文档的稳定身份。
// 该纯转换不触发旧 Kafka Hook，也不代表告警已获准处置。
func CompatibilityMessage(a domain.Alert, level func(string) (string, error)) (kingeye.AlarmMessage, error) {
	outcome := lifecycle.OutcomeAlertUpdated
	switch a.Status {
	case domain.AlertStatusRecovered:
		outcome = lifecycle.OutcomeAlertRecovered
	case domain.AlertStatusClosed:
		outcome = lifecycle.OutcomeAlertClosed
	}
	input := lifecycle.FinalHookInput{Alert: a, Outcome: outcome, Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSystemOperation, ID: a.AlertID}}
	m, err := convertFieldsWithLevel(input, level, false)
	if err != nil {
		return m, err
	}
	m.AlarmID, err = AlarmID(a.BKTenantID, a.AlertID)
	if err != nil {
		return m, err
	}
	effective, err := view.EnrichedAlert(a)
	if err != nil {
		return m, err
	}
	values, err := decodeEffectiveEnrich(effective)
	if err != nil {
		return m, err
	}
	m.SourceName = firstNonEmpty(values.source.SourceName, a.EventSourceID)
	m.SourceID = firstNonEmpty(values.source.SourceID, a.EventSourceID)
	return m, nil
}

// AlarmID 由租户与 Alert ID 定位一个 KAC 告警，沿用既有文档身份。
func AlarmID(tenant, alert string) (string, error) {
	raw, err := json.Marshal([]string{"linkd:kac-alert-projection:v1", tenant, alert})
	if err != nil {
		return "", err
	}
	return "linkd-" + uuid.NewSHA1(uuid.NameSpaceURL, raw).String(), nil
}
