// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package aegis

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/aegisv2/internal/rum"
)

// 初始导航时间点相对于 performance.timeOrigin，保留毫秒值，不使用阶段耗时推算。
var viewNavigationKeys = []struct{ key, attr string }{
	{"responseStart", attrViewFirstByte},
	{"domInteractive", attrViewDOMInteractive},
	{"domContentLoadedEventEnd", attrViewDOMContentLoaded},
	{"domComplete", attrViewDOMComplete},
	{"loadEventEnd", attrViewLoadEvent},
}

// View：PV/SPA 表示 start，页面性能表示 update，均使用瞬时 Span。
func (c *spanConverter) appendView(e aegisEvent, phase string) {
	span := c.newEventSpan(e, rum.View, "")
	attrs := span.Attributes()
	view := e.record.Fields.View
	attrs.PutString(attrViewPhase, phase)
	if view.LoadingType == viewLoadingTypeInitial {
		referrer := view.Referrer
		if referrer == "" {
			referrer = c.payload.Bean.Referer
		}
		attrs.PutString(attrViewReferrer, referrer)
		attrs.PutString(attrViewPrevious, "")
		attrs.PutString(attrViewPreviousURLTpl, "")
	} else if view.Referrer != "" {
		attrs.PutString(attrViewPrevious, view.Referrer)
		attrs.PutString(attrViewPreviousURLTpl, urlTemplate(view.Referrer))
	}
	if phase != spanViewUpdate || view.LoadingType != viewLoadingTypeInitial {
		return
	}
	for _, def := range viewNavigationKeys {
		if value, ok := extractFloat64(e.msg.raw, def.key); ok && value > 0 {
			attrs.PutDouble(def.attr, value)
		}
	}
}
