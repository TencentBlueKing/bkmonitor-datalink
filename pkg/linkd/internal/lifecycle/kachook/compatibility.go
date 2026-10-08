// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kachook

import (
	"linkd/internal/domain"
	"linkd/internal/enrich/kingeye"
	"linkd/internal/enrich/view"
	"linkd/internal/lifecycle"
	"linkd/internal/projection"
)

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
	// 投影快照不携带复查调度时间；仅为领域校验补足，绝不向兼容文档输出此元数据。
	if a.Shield.Active && a.Shield.NextCheckAt == nil {
		t := a.UpdateAt
		a.Shield.NextCheckAt = &t
	}
	input := lifecycle.FinalHookInput{Alert: a, Outcome: outcome, Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSystemOperation, ID: a.AlertID}}
	m, err := convertFieldsWithLevel(input, level, false)
	if err != nil {
		return m, err
	}
	m.AlarmID, err = projection.AlarmID(a.BKTenantID, a.AlertID)
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
