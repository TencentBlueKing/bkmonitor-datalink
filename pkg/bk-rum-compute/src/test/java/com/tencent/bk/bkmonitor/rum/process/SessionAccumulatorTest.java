// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.process;

import static org.assertj.core.api.Assertions.assertThat;

import com.tencent.bk.bkmonitor.rum.model.Resource;
import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import com.tencent.bk.bkmonitor.rum.model.SessionEvent;
import com.tencent.bk.bkmonitor.rum.model.SessionEventDocument;
import com.tencent.bk.bkmonitor.rum.model.Span;
import com.tencent.bk.bkmonitor.rum.model.SpanLink;
import com.tencent.bk.bkmonitor.rum.model.SpanStatus;
import java.util.HashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;

class SessionAccumulatorTest {
  @Test
  void aggregatesNestedRumFieldsAndOutOfOrderTimes() {
    SessionEventDocument accumulator = new SessionEventDocument();
    accumulator.add(event("view", "home.view", 2_000L, "https://example.test/home", 0));
    accumulator.add(event("action", "button.click", 4_000L, "https://example.test/cart", 0));
    accumulator.add(event("resource", "script.resource", 3_000L, "https://example.test/home", 0));
    accumulator.add(event("error", "js.error", 5_000L, "https://example.test/cart", 1));

    SessionEvent summary = accumulator.toSummary(14_999L);
    assertThat(summary.getMinStartTime()).isEqualTo(2_000L);
    assertThat(summary.getMaxEndTime()).isEqualTo(5_000L);
    assertThat(summary.getDuration()).isEqualTo(3_000L);
    assertThat(summary.getActionCount()).isEqualTo(1);
    assertThat(summary.getResourceCount()).isEqualTo(1);
    assertThat(summary.getErrorCount()).isEqualTo(1);
    assertThat(summary.getUpdatedAtTs()).isEqualTo(14_999_000L);
  }

  @Test
  void countsSpanLinksWithoutDeduplication() {
    SessionEventDocument accumulator = new SessionEventDocument();
    RumEvent first = event("action", "action.click", 1_000L, null, 0);
    SpanLink a = link("0123456789abcdef0123456789abcdef");
    SpanLink duplicate = link("0123456789ABCDEF0123456789ABCDEF");
    first.getSpan().setLinks(java.util.Arrays.asList(a, duplicate));
    RumEvent second = event("action", "action.custom", 2_000L, null, 0);
    second.getSpan().setTraceId("ffffffffffffffffffffffffffffffff");
    second.getSpan().setLinks(java.util.Collections.singletonList(link("fedcba9876543210fedcba9876543210")));
    accumulator.add(first);
    accumulator.add(second);
    assertThat(accumulator.toSummary(3_000L).getTraceCount()).isEqualTo(3);
  }

  @Test
  void countsSpanLinksWithoutReadingTraceIds() {
    SessionEventDocument accumulator = new SessionEventDocument();
    RumEvent event = event("action", "action.custom", 1_000L, null, 0);
    event.getSpan().setLinks(java.util.Collections.singletonList(link("trace-id-from-sdk")));

    accumulator.add(event);

    assertThat(accumulator.toSummary(2_000L).getTraceCount()).isEqualTo(1);
  }

  private SpanLink link(String traceId) {
    SpanLink link = new SpanLink();
    link.setTraceId(traceId);
    return link;
  }
  @Test
  void countsErrorsFromFormalFields() {
    RumEvent resourceError = event("error", "browser.resource_error", 1_000L, null, 0);
    resourceError.getSpan().getAttributes().put("error.source", "resource");
    SessionEventDocument accumulator = new SessionEventDocument();
    accumulator.add(resourceError);
    SessionEvent summary = accumulator.toSummary(11_000L);

    assertThat(summary.getErrorCount()).isEqualTo(1);
  }


  @Test
  void classifiesFailedRequestAndActionFrustration() {
    SessionEventDocument accumulator = new SessionEventDocument();
    RumEvent failedRequest = event("resource", "browser.resource.fetch", 1_000L, null, 2);
    failedRequest.getSpan().getAttributes().put("resource.type", "fetch");
    failedRequest.getSpan().getAttributes().put("outcome.type", "error");
    RumEvent action = event("action", "browser.action", 2_000L, null, 0);
    action.getSpan().getAttributes().put("action.frustration.type", "rage_click");

    accumulator.add(failedRequest);
    accumulator.add(action);
    SessionEvent summary = accumulator.toSummary(3_000L);

    assertThat(summary.getResourceCount()).isEqualTo(1L);
    assertThat(summary.getRequestCount()).isEqualTo(1);
    assertThat(summary.getRequestErrorCount()).isEqualTo(1);
    assertThat(summary.getErrorCount()).isEqualTo(1L);
    assertThat(summary.getActionCount()).isEqualTo(1L);
    assertThat(summary.getFrustrationCount()).isEqualTo(1L);
  }

  @Test
  void aggregatesSessionExperienceMetrics() {
    SessionEventDocument accumulator = new SessionEventDocument();
    RumEvent loadingA = event("view", "browser.view", 1_000L, null, 0);
    loadingA.getSpan().getAttributes().put("view.loading_time", 100L);
    RumEvent loadingB = event("view", "browser.view", 2_000L, null, 0);
    loadingB.getSpan().getAttributes().put("view.loading_time", 300L);
    RumEvent lcp = event("vital", "browser.web_vital", 3_000L, null, 0);
    lcp.getSpan().getAttributes().put("vital.metric", "lcp");
    lcp.getSpan().getAttributes().put("vital.value", 123.4D);
    RumEvent inp = event("vital", "browser.web_vital", 4_000L, null, 0);
    inp.getSpan().getAttributes().put("vital.metric", "inp");
    inp.getSpan().getAttributes().put("vital.value", 45.6D);
    RumEvent cls = event("vital", "browser.web_vital", 5_000L, null, 0);
    cls.getSpan().getAttributes().put("vital.metric", "cls");
    cls.getSpan().getAttributes().put("vital.value", 0.12D);

    accumulator.add(loadingA);
    accumulator.add(loadingB);
    accumulator.add(lcp);
    accumulator.add(inp);
    accumulator.add(cls);
    SessionEvent summary = accumulator.toSummary(6_000L);

    assertThat(summary.getAvgLoadTimeUs()).isEqualTo(200_000L);
    assertThat(summary.getMaxLcpUs()).isEqualTo(123_400L);
    assertThat(summary.getMaxInpUs()).isEqualTo(45_600L);
    assertThat(summary.getMaxCls()).isEqualTo(0.12D);
  }

  @Test
  void usesEndTimeAndKeepsFirstIndexDate() {
    SessionEventDocument accumulator = new SessionEventDocument();
    RumEvent first = event("view", "browser.view", 86_399_000_000L, null, 0);
    first.setEndTime(86_401_000_000L);
    RumEvent earlier = event("view", "browser.view", 1_000_000L, null, 0);
    earlier.setEndTime(2_000_000L);

    accumulator.add(first);
    accumulator.add(earlier);
    SessionEvent summary = accumulator.toSummary(1L, false);

    assertThat(summary.getMinStartTime()).isEqualTo(1_000_000L);
    assertThat(summary.getMaxEndTime()).isEqualTo(86_401_000_000L);
    assertThat(summary.getDuration()).isEqualTo(86_400_000_000L);
    assertThat(summary.getDate()).isEqualTo("1970-01-01");
  }

  @Test
  void handlesNullNestedFields() {
    RumEvent event = new RumEvent();
    event.setSessionId("session-1");
    event.setEventTime(1_000L);
    SessionEventDocument accumulator = new SessionEventDocument();
    accumulator.add(event);

    SessionEvent summary = accumulator.toSummary(2_000L);
    assertThat(summary.getSessionId()).isEqualTo("session-1");
    assertThat(summary.getMinStartTime()).isEqualTo(1_000L);
  }

  /**
   * 字符串字段仍走 maxStringChars 截断,与原 prefer(bound(...)) 等价。
   * 字段规则抽象后由 setter 侧的 bound(v) 调用承担,本测试是 watch-dog:
   * 一旦 FIELD_RULES 里的 setter 退化为裸 ::setUserId,本用例立即失败。
   */
  @Test
  void preservesCompleteRoutingIdentityWhenDisplayFieldsAreTruncated() {
    String appName = " app-" + "a".repeat(600);
    String sessionId = " session-" + "s".repeat(600);
    RumEvent input = event("action", "click", 1_000L, "/", 0);
    input.setSessionId(sessionId);
    input.getSpan().setAppName(appName);
    SessionEventDocument accumulator = new SessionEventDocument(8);

    accumulator.add(input);
    SessionEvent summary = accumulator.toSummary(2_000L);

    assertThat(summary.getAppName()).isEqualTo(appName);
    assertThat(summary.getSessionId()).isEqualTo(sessionId);
  }

  @Test
  void truncatesStringFieldsToMaxChars() {
    String longUserId = "u".repeat(1024);
    RumEvent e = new RumEvent();
    e.setSessionId("s1");
    e.setEventTime(1_000L);
    Span span = new Span();
    Map<String, Object> attrs = new HashMap<>();
    attrs.put("user.id", longUserId);
    span.setAttributes(attrs);
    e.setSpan(span);

    SessionEventDocument accumulator = new SessionEventDocument();
    accumulator.add(e);

    assertThat(
        accumulator.getUserId().length())
        .withFailMessage("user.id 应被截断到 maxStringChars(默认 512)")
        .isEqualTo(512);
  }

  private RumEvent event(String type, String name, long eventTime, String url, int statusCode) {
    Map<String, Object> attributes = new HashMap<>();
    attributes.put("span_type", type);
    attributes.put("view.id", "view-1");
    if (url != null) {
      attributes.put("view.url", url);
    }
    attributes.put("user.id", "user-1");
    Span spans = new Span();
    spans.setAttributes(attributes);
    spans.setSpanName(name);
    spans.setAppName("rum-app");
    spans.setBkBizId(42);

    Resource resource = new Resource();
    resource.setDeviceType("desktop");
    resource.setServiceVersion("1.0");
    spans.setResource(resource);

    if (statusCode != 0) {
      SpanStatus spanStatus = new SpanStatus();
      spanStatus.setCode(statusCode);
      spanStatus.setMessage("failed");
      spans.setStatus(spanStatus);
    }

    RumEvent event = new RumEvent();
    event.setSessionId("session-1");
    event.setEventTime(eventTime);
    event.setEndTime(eventTime);
    event.setSpanType(type);
    event.setSpan(spans);
    event.setResource(resource);
    return event;
  }
}
