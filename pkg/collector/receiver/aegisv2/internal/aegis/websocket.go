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

func (c *spanConverter) appendWebsocket(e aegisEvent) {
	span := c.newEventSpan(e, rum.Websocket, "")
	attrs := span.Attributes()
	attrs.PutString(attrNetworkProtocolName, spanNetProtocolWS)
	putTargetURLAttrs(attrs, firstNonEmptyString(e.msg.raw, "url"))
	phase := firstNonEmptyString(e.msg.raw, "phase")
	switch phase {
	case wsPhaseConnect, wsPhaseOpen, wsPhaseError, wsPhaseClose:
	default:
		phase = ""
		if success, ok := extractBool(e.msg.raw, "successFlag"); ok {
			phase = wsPhaseOpen
			if !success {
				phase = wsPhaseError
			}
		}
	}
	if e.isError() {
		phase = wsPhaseError
	}
	attrs.PutString(attrWebsocketPhase, phase)
	attrs.PutString(attrWebsocketConnID, firstNonEmptyString(e.msg.raw, "connectionId"))
	if phase == wsPhaseError {
		attrs.PutString(rum.OutcomeTypeKey, outcomeError)
		attrs.PutString(attrErrorMessage, firstNonEmptyString(e.msg.raw, "errorMsg", "msg"))
	}
}
