// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package operator

import (
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func (c *Operator) handlePrometheusRuleAdd(obj interface{}) {
	promRule, ok := obj.(*promv1.PrometheusRule)
	if !ok {
		logger.Errorf("expected PrometheusRule type, got %T", obj)
		return
	}

	c.promsliController.UpdatePrometheusRule(promRule)
}

func (c *Operator) handlePrometheusRuleUpdate(_ interface{}, obj interface{}) {
	promRule, ok := obj.(*promv1.PrometheusRule)
	if !ok {
		logger.Errorf("expected PrometheusRule type, got %T", obj)
		return
	}

	c.promsliController.UpdatePrometheusRule(promRule)
}

func (c *Operator) handlePrometheusRuleDelete(obj interface{}) {
	promRule, ok := obj.(*promv1.PrometheusRule)
	if !ok {
		logger.Errorf("expected PrometheusRule type, got %T", obj)
		return
	}

	c.promsliController.DeletePrometheusRule(promRule)
}
