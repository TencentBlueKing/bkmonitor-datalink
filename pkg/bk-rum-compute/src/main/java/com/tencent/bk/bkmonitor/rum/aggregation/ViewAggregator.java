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
import com.tencent.bk.bkmonitor.rum.model.Span;
import com.tencent.bk.bkmonitor.rum.model.SpanLink;
import com.tencent.bk.bkmonitor.rum.model.ViewEventDocument;
import com.tencent.bk.bkmonitor.rum.utils.FieldAggregation;
import com.tencent.bk.bkmonitor.rum.utils.FieldAggregation.FieldRule;
import com.tencent.bk.bkmonitor.rum.utils.SpanAttributeUtils;
import com.tencent.bk.bkmonitor.rum.utils.TimeUtils;
import java.util.List;
import java.util.Map;
import javax.annotation.Nullable;

/**
 * View 事件的业务聚合逻辑。
 *
 * <p>该类只修改 View 聚合状态，不感知 keyed state、timer、TTL 或输出节奏。新增 View 字段时，优先在
 * {@link #VIEW_FIELD_RULES} 中声明其来源和聚合策略。
 */
public final class ViewAggregator {
  private static final List<FieldRule<ViewEventDocument>> VIEW_FIELD_RULES =
      List.of(
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) -> event.getSessionId(),
              ViewEventDocument::getSessionId,
              ViewEventDocument::setSessionId),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  event.getSpan() == null ? null : event.getSpan().getAppName(),
              ViewEventDocument::getAppName,
              ViewEventDocument::setAppName),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getServiceName(),
              ViewEventDocument::getServiceName,
              ViewEventDocument::setServiceName),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getServiceVersion(),
              ViewEventDocument::getServiceVersion,
              ViewEventDocument::setServiceVersion),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getDeploymentEnvironmentName(),
              ViewEventDocument::getEnvironment,
              ViewEventDocument::setEnvironment),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getTelemetrySdkName(),
              ViewEventDocument::getSdkName,
              ViewEventDocument::setSdkName),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getTelemetrySdkLanguage(),
              ViewEventDocument::getSdkLang,
              ViewEventDocument::setSdkLang),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getTelemetrySdkVersion(),
              ViewEventDocument::getSdkVersion,
              ViewEventDocument::setSdkVersion),
          FieldAggregation.firstNonNullBoxed(
              (event, resource, attributes) ->
                  event.getSpan() == null ? null : event.getSpan().getBkBizId(),
              ViewEventDocument::getBkBizId,
              ViewEventDocument::setBkBizId),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) -> strAttr(attributes, "user.id"),
              ViewEventDocument::getUserId,
              ViewEventDocument::setUserId),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getUserAgentName(),
              ViewEventDocument::getBrowser,
              ViewEventDocument::setBrowser),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getUserAgentVersion(),
              ViewEventDocument::getBrowserVersion,
              ViewEventDocument::setBrowserVersion),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getUserAgentOsName(),
              ViewEventDocument::getOs,
              ViewEventDocument::setOs),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) ->
                  resource == null ? null : resource.getDeviceType(),
              ViewEventDocument::getDeviceType,
              ViewEventDocument::setDeviceType),
          FieldAggregation.firstNonNullBoxed(
              (event, resource, attributes) -> lngAttr(attributes, "view.version"),
              ViewEventDocument::getVersion,
              ViewEventDocument::setVersion),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) -> strAttr(attributes, "network.effective_type"),
              ViewEventDocument::getNetworkType,
              ViewEventDocument::setNetworkType),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) -> strAttr(attributes, "view.name"),
              ViewEventDocument::getViewName,
              ViewEventDocument::setViewName),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) -> strAttr(attributes, "view.url_template"),
              ViewEventDocument::getUrlTemplate,
              ViewEventDocument::setUrlTemplate),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) -> strAttr(attributes, "view.loading_type"),
              ViewEventDocument::getViewLoadingType,
              ViewEventDocument::setViewLoadingType),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) -> strAttr(attributes, "view.loading_time_source"),
              ViewEventDocument::getViewLoadingTimeSource,
              ViewEventDocument::setViewLoadingTimeSource),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) -> strAttr(attributes, "view.end_reason"),
              ViewEventDocument::getViewEndReason,
              ViewEventDocument::setViewEndReason),
          FieldAggregation.firstNonEmpty(
              (event, resource, attributes) -> strAttr(attributes, "view.previous_url_template"),
              ViewEventDocument::getPreviousUrlTemplate,
              ViewEventDocument::setPreviousUrlTemplate),
          FieldAggregation.firstNonNegativeLong(
              (event, resource, attributes) -> {
                Long value = lngAttr(attributes, "view.loading_time");
                return value == null ? null : value * TimeUtils.MS_TO_US;
              },
              ViewEventDocument::getViewLoadingTime,
              ViewEventDocument::setViewLoadingTime),
          FieldAggregation.firstNonNegativeLong(
              (event, resource, attributes) -> {
                Long value = lngAttr(attributes, "view.started_at");
                return value == null ? null : value * TimeUtils.MS_TO_US;
              },
              ViewEventDocument::getMinStartTime,
              ViewEventDocument::setMinStartTime));

  private ViewAggregator() {}

  /** 把一条事件按 View 规则折叠到当前 keyed state。 */
  public static void add(ViewEventDocument stats, RumEvent event) {
    Map<String, Object> attributes = attrs(event);
    Resource resource = resourceOf(event);
    FieldAggregation.applyAll(VIEW_FIELD_RULES, stats, event, resource, attributes);
    updateVitals(stats, attributes);
    count(stats, RumEventClassification.classify(event, attributes));
    countSpanLinks(stats, event);
    long eventTime = event.getEventTime();
    long endTime = event.getEndTime() > 0L ? event.getEndTime() : eventTime;
    stats.setLastEventTs(Math.max(stats.getLastEventTs(), eventTime));
    stats.setMaxEndTime(Math.max(stats.getMaxEndTime(), endTime));
    if (stats.getIndexDate() == null && eventTime > 0L) {
      long indexTime = stats.getMinStartTime() == null ? eventTime : stats.getMinStartTime();
      stats.setIndexDate(TimeUtils.toUtcDate(indexTime));
    }
    if ("end".equals(strAttr(attributes, "view.phase"))) {
      stats.setNormalCloseSeen(true);
    }
  }

  @Nullable
  private static Map<String, Object> attrs(@Nullable RumEvent event) {
    return event == null || event.getSpan() == null ? null : event.getSpan().getAttributes();
  }

  @Nullable
  private static Resource resourceOf(@Nullable RumEvent event) {
    if (event == null) {
      return null;
    }
    if (event.getResource() != null) {
      return event.getResource();
    }
    Span span = event.getSpan();
    return span == null ? null : span.getResource();
  }

  @Nullable
  private static String strAttr(@Nullable Map<String, Object> attributes, String key) {
    return SpanAttributeUtils.getAttributeStringTrimmed(attributes, key);
  }

  @Nullable
  private static Long lngAttr(@Nullable Map<String, Object> attributes, String key) {
    return SpanAttributeUtils.getAttributeLong(attributes, key);
  }

  private static void updateVitals(
      ViewEventDocument stats, @Nullable Map<String, Object> attributes) {
    String metric =
        SpanAttributeUtils.normalizeKey(
            SpanAttributeUtils.getAttributeString(attributes, "vital.metric"));
    if (!isSupportedVital(metric)) {
      return;
    }

    Double value = parseDouble(attributes, "vital.value");
    if (value == null) {
      return;
    }

    double valueMicros = value * (double) TimeUtils.MS_TO_US;
    switch (metric) {
      case "fcp":
        if (stats.getFcp() == null) {
          stats.setFcp(Math.round(valueMicros));
        }
        break;
      case "ttfb":
        if (stats.getTtfb() == null) {
          stats.setTtfb(Math.round(valueMicros));
        }
        break;
      case "cls":
        stats.setCls(Math.max(stats.getCls(), value));
        break;
      case "inp":
        stats.setInp(Math.max(stats.getInp(), Math.round(valueMicros)));
        break;
      case "lcp":
        stats.setLcp(Math.max(stats.getLcp(), Math.round(valueMicros)));
        break;
      default:
        break;
    }
  }

  private static boolean isSupportedVital(String metric) {
    return "fcp".equals(metric)
        || "ttfb".equals(metric)
        || "cls".equals(metric)
        || "inp".equals(metric)
        || "lcp".equals(metric);
  }

  @Nullable
  private static Double parseDouble(@Nullable Map<String, Object> attributes, String key) {
    Object value = attributes == null ? null : attributes.get(key);
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

  private static void countSpanLinks(ViewEventDocument stats, @Nullable RumEvent event) {
    if (event == null) {
      return;
    }
    Span span = event.getSpan();
    if (span == null || span.getLinks() == null) {
      return;
    }
    for (SpanLink link : span.getLinks()) {
      if (link == null) {
        continue;
      }
      stats.setTraceCount(stats.getTraceCount() + 1);
    }
  }

  private static void count(ViewEventDocument stats, int classification) {
    if (RumEventClassification.isAction(classification)) {
      stats.setActionCount(saturatingIncrement(stats.getActionCount()));
    }
    if (RumEventClassification.isResource(classification)) {
      stats.setResourceCount(saturatingIncrement(stats.getResourceCount()));
    }
    if (RumEventClassification.isRequest(classification)) {
      stats.setRequestCount(saturatingIncrement(stats.getRequestCount()));
    }
    if (RumEventClassification.isError(classification)) {
      stats.setErrorCount(saturatingIncrement(stats.getErrorCount()));
    }
    if (RumEventClassification.isRequestError(classification)) {
      stats.setRequestErrorCount(saturatingIncrement(stats.getRequestErrorCount()));
    }
    if (RumEventClassification.isFrustration(classification)) {
      stats.setFrustrationCount(saturatingIncrement(stats.getFrustrationCount()));
    }
    if (RumEventClassification.isLongTask(classification)) {
      stats.setLongTaskCount(saturatingIncrement(stats.getLongTaskCount()));
    }
  }

  private static long saturatingIncrement(long value) {
    return value == Long.MAX_VALUE ? value : value + 1L;
  }
}
