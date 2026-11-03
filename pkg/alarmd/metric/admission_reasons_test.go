// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
)

// Every reason the target plan filter refuses a record with is a reason the
// admission counter keeps. One it does not know is counted as none, which
// reads as a refusal with no reason - and the reasons that say the filter
// could not decide are the ones that most need their name.
func TestEveryTargetPlanRefusalReasonIsCountedByName(t *testing.T) {
	for _, reason := range []string{
		admission.TargetPlanReasonKeyMissing, admission.TargetPlanReasonOutOfTarget, admission.TargetPlanReasonUnresolved,
		admission.TargetPlanReasonSelectorUnavailable,
		admission.FactsUnavailableHostIndex, admission.FactsUnavailableServiceInstanceIndex,
	} {
		if _, known := admissionReasons[reason]; !known {
			t.Errorf("the admission counter does not keep %q: a refusal for it is counted as none", reason)
		}
	}
}
