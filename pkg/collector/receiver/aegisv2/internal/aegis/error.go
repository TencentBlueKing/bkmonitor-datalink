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
	"strings"

	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/aegisv2/internal/rum"
)

// 错误专属 Span 名称（OTel Span.Name）。
const (
	spanError              = "browser.error"
	spanUnhandledRejection = "browser.unhandledrejection"
	spanResourceError      = "browser.resource_error"
)

func (c *spanConverter) appendError(e aegisEvent) {
	source := e.errorSource()
	name := spanError
	switch source {
	case sourceUnhandledRejection:
		name = spanUnhandledRejection
	case sourceResource:
		name = spanResourceError
	}
	span := c.newEventSpan(e, rum.Error, name)
	attrs := span.Attributes()
	attrs.PutString(attrErrorSource, source)
	if handled, ok := extractBool(e.msg.raw, "handled"); ok {
		attrs.PutBool(rum.ErrorHandledKey, handled)
	}
	attrs.PutString(attrCodeFilepath, firstNonEmptyString(e.msg.raw, "filename", "fileName"))
	for _, field := range []struct{ source, target string }{{"lineno", attrCodeLineno}, {"colno", attrCodeColumn}} {
		if value, ok := extractInt64(e.msg.raw, field.source); ok && value >= 0 {
			attrs.PutInt(field.target, value)
		}
	}
	if source == sourceResource {
		putTargetURLAttrs(attrs, firstNonEmptyString(e.msg.raw, "url"))
	}
	putSpanException(span, e)
}

func putSpanException(span ptrace.Span, e aegisEvent) {
	message := firstNonEmptyString(e.msg.raw, "errorMsg", "msg")
	span.Status().SetCode(ptrace.StatusCodeError)
	span.Status().SetMessage(message)
	span.Attributes().PutString(rum.OutcomeTypeKey, outcomeError)
	span.Attributes().PutString(attrErrorMessage, message)
	event := span.Events().AppendEmpty()
	event.SetName(eventNameException)
	event.SetTimestamp(span.EndTimestamp())
	event.Attributes().PutString(attrExceptionType, e.exceptionType())
	event.Attributes().PutString(attrExceptionMessage, message)
	event.Attributes().PutString(attrExceptionStacktrace, firstNonEmptyString(e.msg.raw, "stack", "stacktrace"))
}

func (e aegisEvent) isError() bool {
	if e.record.Fields.Plugin == pluginError || e.record.Fields.Type == eventTypeError {
		return true
	}
	if isErrorLevel(e.record.Fields.Level) {
		return true
	}
	if e.isWebsocket() {
		if success, ok := extractBool(e.msg.raw, "successFlag"); ok && !success {
			return true
		}
	}
	if isErr, ok := extractBool(e.msg.raw, "isErr"); ok && isErr {
		return true
	}
	return false
}

func (e aegisEvent) errorSource() string {
	switch strings.ToLower(strings.TrimSpace(e.record.Fields.Level)) {
	case levelPromiseError, levelUnhandledRejection:
		return sourceUnhandledRejection
	case levelResourceError, levelAssetError:
		return sourceResource
	}
	return sourceWindowError
}

func (e aegisEvent) exceptionType() string {
	if name := firstNonEmptyString(e.msg.raw, "errorName", "errorType"); name != "" {
		return name
	}
	switch e.errorSource() {
	case sourceUnhandledRejection:
		return exceptionTypeUnhandledRejection
	case sourceResource:
		return exceptionTypeResource
	default:
		return exceptionTypeDefault
	}
}

func isErrorLevel(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case levelError, levelFatal, levelCritical, levelPanic, levelException,
		levelPromiseError, levelUnhandledRejection, levelResourceError, levelAssetError,
		levelCustomError:
		return true
	default:
		return false
	}
}
