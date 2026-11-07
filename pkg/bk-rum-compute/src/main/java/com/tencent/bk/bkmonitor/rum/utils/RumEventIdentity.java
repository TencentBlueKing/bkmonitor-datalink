// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.utils;

import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import com.tencent.bk.bkmonitor.rum.model.Span;
import javax.annotation.Nullable;

/** Builds the stable identity used to deduplicate RUM events across checkpoint recovery. */
public final class RumEventIdentity {

  private RumEventIdentity() {}

  /**
   * Returns a collision-free encoding of {@code bk_biz_id + app_name + trace_id + span_id}.
   *
   * <p>Length prefixes keep the encoding unambiguous even when app names contain separators. A
   * missing component returns {@code null}; such an event cannot safely participate in replay
   * deduplication.
   */
  @Nullable
  public static String of(@Nullable RumEvent event) {
    if (event == null) {
      return null;
    }
    Span span = event.getSpan();
    if (span == null || span.getBkBizId() == null) {
      return null;
    }
    String appName = nonBlank(span.getAppName());
    String traceId = nonBlank(span.getTraceId());
    String spanId = nonBlank(span.getSpanId());
    if (appName == null || traceId == null || spanId == null) {
      return null;
    }
    return span.getBkBizId()
        + ":"
        + appName.length()
        + ":"
        + appName
        + traceId.length()
        + ":"
        + traceId
        + spanId.length()
        + ":"
        + spanId;
  }

  @Nullable
  private static String nonBlank(@Nullable String value) {
    if (value == null || value.trim().isEmpty()) {
      return null;
    }
    return value;
  }
}
