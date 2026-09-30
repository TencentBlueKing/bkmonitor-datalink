// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.aggregation;

import com.tencent.bk.bkmonitor.rum.model.Resource;
import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import com.tencent.bk.bkmonitor.rum.model.SessionEventDocument;
import com.tencent.bk.bkmonitor.rum.model.Span;
import com.tencent.bk.bkmonitor.rum.model.SpanLink;
import com.tencent.bk.bkmonitor.rum.utils.FieldAggregation;
import com.tencent.bk.bkmonitor.rum.utils.FieldAggregation.FieldRule;
import com.tencent.bk.bkmonitor.rum.utils.SpanAttributeUtils;
import com.tencent.bk.bkmonitor.rum.utils.TimeUtils;
import java.util.List;
import java.util.Map;
import javax.annotation.Nullable;

/**
 * Session 事件的业务聚合逻辑。
 *
 * <p>该类只修改 {@link SessionEventDocument} 中的聚合状态，不感知 Flink keyed state、timer、TTL 或输出
 * 节奏。字段新增和聚合策略调整集中在这里，窗口算子不需要同步修改。
 */
public final class SessionAggregator {
  private static final List<FieldRule<SessionEventDocument>> FIELD_RULES =
      List.of(
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) -> event.getSessionId(),
              SessionEventDocument::getSessionId,
              SessionEventDocument::setSessionId),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  event.getSpan() == null ? null : event.getSpan().getAppName(),
              SessionEventDocument::getAppName,
              SessionEventDocument::setAppName),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getServiceName(),
              SessionEventDocument::getServiceName,
              (document, value) -> document.setServiceName(bound(document, value))),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getTelemetrySdkName(),
              SessionEventDocument::getSdkName,
              (document, value) -> document.setSdkName(bound(document, value))),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getTelemetrySdkLanguage(),
              SessionEventDocument::getSdkLang,
              (document, value) -> document.setSdkLang(bound(document, value))),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getTelemetrySdkVersion(),
              SessionEventDocument::getSdkVersion,
              (document, value) -> document.setSdkVersion(bound(document, value))),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getUserAgentVersion(),
              SessionEventDocument::getBrowserVersion,
              (document, value) -> document.setBrowserVersion(bound(document, value))),
          FieldAggregation.firstNonNullBoxed(
              (event, resource, attributes) ->
                  event.getSpan() == null ? null : event.getSpan().getBkBizId(),
              SessionEventDocument::getBkBizId,
              SessionEventDocument::setBkBizId),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getDeploymentEnvironmentName(),
              SessionEventDocument::getEnvironment,
              (document, value) -> document.setEnvironment(bound(document, value))),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getServiceVersion(),
              SessionEventDocument::getServiceVersion,
              (document, value) -> document.setServiceVersion(bound(document, value))),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) -> text(attributes, "user.id"),
              SessionEventDocument::getUserId,
              (document, value) -> document.setUserId(bound(document, value))),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getDeviceType(),
              SessionEventDocument::getDeviceType,
              (document, value) -> document.setDeviceType(bound(document, value))),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getUserAgentName(),
              SessionEventDocument::getBrowser,
              (document, value) -> document.setBrowser(bound(document, value))),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getUserAgentOsName(),
              SessionEventDocument::getOs,
              (document, value) -> document.setOs(bound(document, value))),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) -> text(attributes, "network.effective_type"),
              SessionEventDocument::getNetworkEffectiveType,
              (document, value) -> document.setNetworkEffectiveType(bound(document, value))));

  private SessionAggregator() {}

  /** 把一条事件按 Session 规则折叠到当前 keyed state。 */
  public static void add(SessionEventDocument document, @Nullable RumEvent event) {
    if (event == null) {
      return;
    }

    Span span = event.getSpan();
    Map<String, Object> attributes = attributesOf(event);
    Resource resource = resourceOf(event);

    FieldAggregation.applyAll(FIELD_RULES, document, event, resource, attributes);

    long eventTime = event.getEventTime();
    long endTime = event.getEndTime() > 0L ? event.getEndTime() : eventTime;
    boolean validEventTime = eventTime > 0L;
    boolean startsSession =
        validEventTime
            && (document.getMinStartTime() == 0L || eventTime < document.getMinStartTime());
    boolean latestStart = validEventTime && eventTime >= document.getLastEventTs();
    if (startsSession) {
      document.setMinStartTime(eventTime);
    }
    if (endTime > document.getMaxEndTime()) {
      document.setMaxEndTime(endTime);
    }
    if (latestStart) {
      document.setLastEventTs(eventTime);
    }
    if (document.getIndexDate() == null && validEventTime) {
      document.setIndexDate(TimeUtils.toUtcDate(eventTime));
    }

    if (span != null && span.getLinks() != null) {
      for (SpanLink link : span.getLinks()) {
        if (link == null) {
          continue;
        }
        document.setTraceCount(saturatingIncrement(document.getTraceCount()));
      }
    }

    String viewId = text(attributes, "view.id");
    if (viewId != null) {
      document.setLastViewBucket(viewBucket(viewId));
    }

    String viewUrlTemplate = text(attributes, "view.url_template");
    if (viewUrlTemplate != null) {
      if (document.getEnterUrlTemplate() == null || startsSession) {
        document.setEnterUrlTemplate(bound(document, viewUrlTemplate));
      }
      if (document.getExitUrlTemplate() == null || latestStart) {
        document.setExitUrlTemplate(bound(document, viewUrlTemplate));
      }
    }

    updateExperienceMetrics(document, attributes);
    countEvent(document, RumEventClassification.classify(event, attributes));
    if ("end".equals(text(attributes, "session.phase"))) {
      document.setNormalCloseSeen(true);
    }
  }

  private static void countEvent(SessionEventDocument document, int classification) {
    if (RumEventClassification.isAction(classification)) {
      document.setActionCount(saturatingIncrement(document.getActionCount()));
    }
    if (RumEventClassification.isResource(classification)) {
      document.setResourceCount(saturatingIncrement(document.getResourceCount()));
    }
    if (RumEventClassification.isRequest(classification)) {
      document.setRequestCount(saturatingIncrement(document.getRequestCount()));
    }
    if (RumEventClassification.isError(classification)) {
      document.setErrorCount(saturatingIncrement(document.getErrorCount()));
    }
    if (RumEventClassification.isRequestError(classification)) {
      document.setRequestErrorCount(saturatingIncrement(document.getRequestErrorCount()));
    }
    if (RumEventClassification.isFrustration(classification)) {
      document.setFrustrationCount(saturatingIncrement(document.getFrustrationCount()));
    }
    if (RumEventClassification.isLongTask(classification)) {
      document.setLongTaskCount(saturatingIncrement(document.getLongTaskCount()));
    }
  }

  private static void updateExperienceMetrics(
      SessionEventDocument document, @Nullable Map<String, Object> attributes) {
    Long loadingTimeMs = SpanAttributeUtils.getAttributeLong(attributes, "view.loading_time");
    if (loadingTimeMs != null && loadingTimeMs >= 0L) {
      long loadingTimeUs = millisecondsToMicros(loadingTimeMs);
      document.setTotalLoadTimeUs(saturatingAdd(document.getTotalLoadTimeUs(), loadingTimeUs));
      document.setLoadTimeSampleCount(saturatingIncrement(document.getLoadTimeSampleCount()));
    }

    String metric =
        SpanAttributeUtils.normalizeKey(
            SpanAttributeUtils.getAttributeStringTrimmed(attributes, "vital.metric"));
    Double value = doubleAttribute(attributes, "vital.value");
    if (value == null || value < 0D) {
      return;
    }
    if ("lcp".equals(metric)) {
      document.setMaxLcpUs(Math.max(document.getMaxLcpUs(), millisecondsToMicros(value)));
    } else if ("inp".equals(metric)) {
      document.setMaxInpUs(Math.max(document.getMaxInpUs(), millisecondsToMicros(value)));
    } else if ("cls".equals(metric)) {
      document.setMaxCls(Math.max(document.getMaxCls(), value));
    }
  }

  @Nullable
  private static Double doubleAttribute(
      @Nullable Map<String, Object> attributes, String name) {
    Object value = attributes == null ? null : attributes.get(name);
    if (value instanceof Number) {
      return ((Number) value).doubleValue();
    }
    if (value == null) {
      return null;
    }
    try {
      return Double.parseDouble(value.toString().trim());
    } catch (NumberFormatException ignored) {
      return null;
    }
  }

  private static long millisecondsToMicros(long value) {
    return value > Long.MAX_VALUE / TimeUtils.MS_TO_US
        ? Long.MAX_VALUE
        : value * TimeUtils.MS_TO_US;
  }

  private static long millisecondsToMicros(double value) {
    double micros = value * TimeUtils.MS_TO_US;
    return micros >= Long.MAX_VALUE ? Long.MAX_VALUE : Math.round(micros);
  }

  private static long saturatingAdd(long left, long right) {
    return left >= Long.MAX_VALUE - right ? Long.MAX_VALUE : left + right;
  }

  /** 把 view.id 映射到 16384 个固定桶，Session 算子用独立 keyed state 保存桶位图。 */
  public static int viewBucket(String viewId) {
    int hash = viewId.hashCode();
    hash ^= hash >>> 16;
    hash *= 0x7feb352d;
    hash ^= hash >>> 15;
    return hash & 0x3fff;
  }

  @Nullable
  private static Resource resourceOf(RumEvent event) {
    if (event.getResource() != null) {
      return event.getResource();
    }
    Span span = event.getSpan();
    return span == null ? null : span.getResource();
  }

  @Nullable
  private static Map<String, Object> attributesOf(RumEvent event) {
    Span span = event.getSpan();
    return span == null ? null : span.getAttributes();
  }

  @Nullable
  private static String text(@Nullable Map<String, Object> attributes, String... names) {
    if (attributes == null) {
      return null;
    }
    for (String name : names) {
      Object value = attributes.get(name);
      if (value == null) {
        continue;
      }
      String result = nonBlank(String.valueOf(value));
      if (result != null) {
        return result;
      }
    }
    return null;
  }

  @Nullable
  private static String bound(SessionEventDocument document, @Nullable String value) {
    String normalized = nonBlank(value);
    if (normalized == null || normalized.length() <= document.getMaxStringChars()) {
      return normalized;
    }
    return normalized.substring(0, document.getMaxStringChars());
  }

  @Nullable
  private static String nonBlank(@Nullable String value) {
    if (value == null || value.trim().isEmpty()) {
      return null;
    }
    return value.trim();
  }

  private static int saturatingIncrement(int value) {
    return value == Integer.MAX_VALUE ? value : value + 1;
  }

  private static long saturatingIncrement(long value) {
    return value == Long.MAX_VALUE ? value : value + 1;
  }
}
