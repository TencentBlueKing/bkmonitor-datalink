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
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/aegisv2/internal/rum"
)

// buildResourceAttrs 生成 resource 的属性集：先应用 BK-RUM 默认值，再补充 SDK 与环境上下文。
func buildResourceAttrs(attrs pcommon.Map, payload collectPayload) {
	rum.ApplyResourceDefaults(attrs)

	// 基础SDK信息
	attrs.PutString(attrTelemetrySDKName, sdkNameAegis)
	attrs.PutString(attrTelemetrySDKLang, sdkLangWebJS)
	if payload.V != "" {
		attrs.PutString(attrTelemetrySDKVersion, payload.V)
	}
	if payload.Bean.Version != "" {
		attrs.PutString(attrServiceVersion, payload.Bean.Version)
	}
	if payload.Bean.Env != "" {
		attrs.PutString(attrDeploymentEnvName, payload.Bean.Env)
	}

	// platform 的已知操作系统值可直接映射，其他值不猜测设备类型。
	switch payload.Bean.Platform {
	case platformMacOS, platformWindows, platformLinux, platformAndroid, platformIOS:
		attrs.PutString(attrUserAgentOSName, payload.Bean.Platform)
	}
}

// putCollectorScope 设置 instrumentation scope 信息
func putCollectorScope(scope pcommon.InstrumentationScope, version string) {
	scope.SetName(scopeName)
	if version != "" {
		scope.SetVersion(version)
	}
}

// recordPageURL 获取页面URL：优先取ViewURL，兜底取From字段
func recordPageURL(record d2Record) string {
	if record.Fields.View.ViewURL != "" {
		return record.Fields.View.ViewURL
	}
	return record.Fields.From
}

// putCommonSpanAttrs 填充span通用RUM属性：会话、用户、设备、视图、动作信息
// 空字符串不写入属性；nil指针字段跳过属性注入
func putCommonSpanAttrs(attrs pcommon.Map, payload collectPayload, record d2Record) {
	session := record.Fields.Session
	view := record.Fields.View
	action := record.Fields.Action
	bean := payload.Bean

	if session.ID != "" {
		attrs.PutString(rum.SessionIDKey, session.ID)
	}
	if replay := session.HasReplay; replay != nil {
		attrs.PutBool(attrSessionHasReplay, *replay)
	}
	if bean.UID != "" {
		attrs.PutString(attrUserID, bean.UID)
	}
	if bean.AID != "" {
		attrs.PutString(attrDeviceID, bean.AID)
	}

	if action.ID != "" {
		attrs.PutString(attrActionID, action.ID)
	}
	if view.ID != "" {
		attrs.PutString(attrViewID, view.ID)
	}
	if view.ViewName != "" {
		attrs.PutString(attrViewName, view.ViewName)
	}
	if view.LoadingType != "" {
		attrs.PutString(attrViewLoadingType, view.LoadingType)
	}

	pageURL := recordPageURL(record)
	if pageURL != "" {
		attrs.PutString(attrViewURL, pageURL)
		urlTpl := urlTemplate(pageURL)
		if urlTpl != "" {
			attrs.PutString(attrViewURLTemplate, urlTpl)
		}
	}

	putBrowserContextAttrs(attrs, payload)
}

// putBrowserContextAttrs 注入浏览器网络相关属性
func putBrowserContextAttrs(attrs pcommon.Map, payload collectPayload) {
	bean := payload.Bean

	netType := strings.ToLower(strings.TrimSpace(bean.NetType))
	switch netType {
	case netWired:
		attrs.PutString(attrNetworkConnectionType, netEthernet)
	case netGenSlow2G, netGen2G, netGen3G, netGen4G, netGen5G, netGen6G:
		attrs.PutString(attrNetworkConnectionType, netCellular)
		if netType != netGen5G && netType != netGen6G {
			attrs.PutString(attrNetworkEffectiveType, netType)
		}
	case netBluetooth, netCellular, netEthernet, netMixed, netNone, netOther, netUnknown, netWifi, netWimax:
		attrs.PutString(attrNetworkConnectionType, netType)
	}
	// 连接类型并不能证明在线；只有明确的 none 表示断网。
	if netType == netNone {
		attrs.PutString(attrNetworkStatus, netNotConnected)
	}

	putViewportAttrs(attrs, bean.VP, attrBrowserViewportWidth, attrBrowserViewportHeight)
	putViewportAttrs(attrs, bean.SR, attrScreenWidth, attrScreenHeight)
}

// dynamicPathSegment 匹配动态路由片段：纯数字、16位以上十六进制、UUIDv4
var dynamicPathSegment = regexp.MustCompile(`(?i)^(?:[0-9]+|[0-9a-f]{16,}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

// urlTemplate 将URL路径中动态id替换为 :id，用于维度聚合，降低基数
// 规则：纯数字、长十六进制串、UUIDv4 替换为 :id；其他路径段保留原样
func urlTemplate(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	path := parsed.EscapedPath()
	if path == "" && parsed.Host != "" {
		path = urlTemplateRoot
	}
	segments := strings.Split(path, "/")

	for i, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err == nil && dynamicPathSegment.MatchString(decoded) {
			segments[i] = urlTemplateID
		}
	}
	return strings.Join(segments, "/")
}

// putViewportAttrs 解析视口/屏幕尺寸字符串，如 "1920*1080" "1920x1080"
// rawValue: 原始宽高字符串；wKey/hKey 对应属性key
// 解析失败、数值<=0 直接跳过，不写入属性
func putViewportAttrs(attrs pcommon.Map, rawValue, wKey, hKey string) {
	parts := strings.FieldsFunc(rawValue, func(r rune) bool {
		return r == '*' || r == 'x' || r == '×'
	})
	if len(parts) != 2 {
		return
	}

	width, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || width <= 0 {
		return
	}
	height, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil || height <= 0 {
		return
	}

	attrs.PutInt(wKey, width)
	attrs.PutInt(hKey, height)
}
