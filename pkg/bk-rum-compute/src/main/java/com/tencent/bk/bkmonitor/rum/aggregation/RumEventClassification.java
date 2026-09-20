// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.aggregation;

import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import com.tencent.bk.bkmonitor.rum.model.Span;
import com.tencent.bk.bkmonitor.rum.model.SpanStatus;
import com.tencent.bk.bkmonitor.rum.utils.SpanAttributeUtils;
import java.util.Map;
import javax.annotation.Nullable;

/** RUM 事件分类规则，使用 bitmask 避免在逐条记录处理路径分配分类对象。 */
public final class RumEventClassification {
  private static final int OTEL_ERROR_STATUS_CODE = 2;
  private static final int ACTION = 1;
  private static final int RESOURCE = 1 << 1;
  private static final int ERROR = 1 << 2;
  private static final int REQUEST = 1 << 3;
  private static final int REQUEST_ERROR = 1 << 4;
  private static final int LONG_TASK = 1 << 5;
  private static final int FRUSTRATION = 1 << 6;

  private RumEventClassification() {}

  public static int classify(RumEvent event, @Nullable Map<String, Object> attributes) {
    Span span = event.getSpan();
    String spanType = SpanAttributeUtils.normalizeKey(event.getSpanType());
    if (spanType.isEmpty()) {
      spanType =
          SpanAttributeUtils.normalizeKey(
              SpanAttributeUtils.getAttributeStringTrimmed(attributes, "span_type"));
    }

    String spanName =
        SpanAttributeUtils.normalizeKey(span == null ? null : span.getSpanName());
    String outcome =
        SpanAttributeUtils.normalizeKey(
            SpanAttributeUtils.getAttributeStringTrimmed(attributes, "outcome.type"));
    String resourceType =
        SpanAttributeUtils.normalizeKey(
            SpanAttributeUtils.getAttributeStringTrimmed(attributes, "resource.type"));

    boolean action =
        "action".equals(spanType)
            || "click".equals(spanType)
            || spanName.contains("click");
    boolean resource = "resource".equals(spanType) || spanName.contains("resource");
    boolean request = "fetch".equals(resourceType) || "xhr".equals(resourceType);
    boolean failedOutcome = "error".equals(outcome) || "timeout".equals(outcome);
    boolean error =
        "error".equals(spanType)
            || spanName.contains("error")
            || failedOutcome
            || hasErrorStatus(event, span);
    boolean longTask = "long_task".equals(spanType) || spanName.contains("long_task");
    boolean frustration =
        action && SpanAttributeUtils.hasAttribute(attributes, "action.frustration.type");

    int classification = 0;
    classification = action ? classification | ACTION : classification;
    classification = resource ? classification | RESOURCE : classification;
    classification = error ? classification | ERROR : classification;
    classification = request ? classification | REQUEST : classification;
    classification = request && error ? classification | REQUEST_ERROR : classification;
    classification = longTask ? classification | LONG_TASK : classification;
    return frustration ? classification | FRUSTRATION : classification;
  }

  private static boolean hasErrorStatus(RumEvent event, @Nullable Span span) {
    SpanStatus status = event.getStatus();
    if (status == null && span != null) {
      status = span.getStatus();
    }
    return status != null
        && status.getCode() != null
        && status.getCode() == OTEL_ERROR_STATUS_CODE;
  }

  public static boolean isAction(int classification) {
    return contains(classification, ACTION);
  }

  public static boolean isResource(int classification) {
    return contains(classification, RESOURCE);
  }

  public static boolean isError(int classification) {
    return contains(classification, ERROR);
  }

  public static boolean isRequest(int classification) {
    return contains(classification, REQUEST);
  }

  public static boolean isRequestError(int classification) {
    return contains(classification, REQUEST_ERROR);
  }

  public static boolean isLongTask(int classification) {
    return contains(classification, LONG_TASK);
  }

  public static boolean isFrustration(int classification) {
    return contains(classification, FRUSTRATION);
  }

  private static boolean contains(int classification, int flag) {
    return (classification & flag) != 0;
  }
}
