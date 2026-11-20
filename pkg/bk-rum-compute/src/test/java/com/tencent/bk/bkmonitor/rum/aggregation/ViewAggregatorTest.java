// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.aggregation;

import static org.assertj.core.api.Assertions.assertThat;

import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import com.tencent.bk.bkmonitor.rum.model.Span;
import com.tencent.bk.bkmonitor.rum.model.SpanLink;
import com.tencent.bk.bkmonitor.rum.model.SpanStatus;
import com.tencent.bk.bkmonitor.rum.model.ViewEventDocument;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

class ViewAggregatorTest {

  @Test
  void aggregatesFieldsCountsAndSpanLinksWithoutWindowState() {
    ViewEventDocument state = new ViewEventDocument();
    state.setViewId("view-1");

    ViewAggregator.add(
        state,
        event(
            "click",
            1_000L,
            Map.of(
                "view.name", "home",
                "network.effective_type", "4g"),
            List.of(link("0123456789abcdef0123456789abcdef"))));
    ViewAggregator.add(
        state,
        event(
            "resource",
            2_000L,
            Map.of("network.effective_type", "3g", "resource.type", "fetch"),
            List.of(link("0123456789abcdef0123456789abcdef"))));

    assertThat(state.getViewName()).isEqualTo("home");
    assertThat(state.getNetworkType()).isEqualTo("4g");
    assertThat(state.getActionCount()).isEqualTo(1L);
    assertThat(state.getResourceCount()).isEqualTo(1L);
    assertThat(state.getRequestCount()).isEqualTo(1L);
    assertThat(state.getTraceCount()).isEqualTo(2);
    assertThat(state.getLastEventTs()).isEqualTo(2_000L);
}

  @Test
  void classifiesFailedRequestFromResourceFieldsAndStatus() {
    ViewEventDocument state = new ViewEventDocument();
    RumEvent event =
        event(
            "resource",
            1_000L,
            Map.of("resource.type", "fetch", "outcome.type", "error"),
            List.of());
    SpanStatus status = new SpanStatus();
    status.setCode(2);
    event.getSpan().setStatus(status);

    ViewAggregator.add(state, event);

    assertThat(state.getResourceCount()).isEqualTo(1L);
    assertThat(state.getRequestCount()).isEqualTo(1L);
    assertThat(state.getRequestErrorCount()).isEqualTo(1L);
    assertThat(state.getErrorCount()).isEqualTo(1L);
  }

  @Test
  void keepsClsUnitlessAndConvertsTimeVitalsToMicros() {
    ViewEventDocument state = new ViewEventDocument();

    ViewAggregator.add(
        state,
        event(
            "vital",
            1_000L,
            Map.of("vital.metric", "cls", "vital.value", 0.12D),
            List.of()));
    ViewAggregator.add(
        state,
        event(
            "vital",
            2_000L,
            Map.of("vital.metric", "lcp", "vital.value", 123.4D),
            List.of()));

    assertThat(state.getCls()).isEqualTo(0.12D);
    assertThat(state.getLcp()).isEqualTo(123_400L);
  }

  @Test
  void usesEndTimeForMaxEndTimeAndKeepsFirstIndexDate() {
    ViewEventDocument state = new ViewEventDocument();
    RumEvent first = event("view", 86_399_000_000L, Map.of(), List.of());
    first.setEndTime(86_401_000_000L);
    RumEvent outOfOrder = event("view", 1_000_000L, Map.of(), List.of());
    outOfOrder.setEndTime(2_000_000L);

    ViewAggregator.add(state, first);
    ViewAggregator.add(state, outOfOrder);

    assertThat(state.getMaxEndTime()).isEqualTo(86_401_000_000L);
    assertThat(state.getIndexDate()).isEqualTo("1970-01-01");
    assertThat(state.toSummary(1L, false).getDate()).isEqualTo("1970-01-01");
  }

  @Test
  void recordsNormalCloseFromViewPhase() {
    ViewEventDocument state = new ViewEventDocument();
    state.setViewId("view-1");

    ViewAggregator.add(
        state,
        event("view", 1_000L, Map.of("view.phase", "end"), List.of()));

    assertThat(state.isNormalCloseSeen()).isTrue();
  }

  @Test
  void countsSpanLinksWithoutDeduplication() {
    ViewEventDocument state = new ViewEventDocument();

    ViewAggregator.add(
        state,
        event(
            "view",
            1_000L,
            Map.of(),
            java.util.Arrays.asList(link("TRACE-ID"), link("TRACE-ID"), link(null))));

    assertThat(state.getTraceCount()).isEqualTo(3);
  }

  private static RumEvent event(
      String spanType, long eventTime, Map<String, Object> attributes, List<SpanLink> links) {
    Map<String, Object> eventAttributes = new HashMap<>(attributes);
    eventAttributes.put("span_type", spanType);
    Span span = new Span();
    span.setAttributes(eventAttributes);
    span.setLinks(links);

    RumEvent event = new RumEvent();
    event.setSpan(span);
    event.setSpanType(spanType);
    event.setEventTime(eventTime);
    event.setEndTime(eventTime);
    return event;
  }

  private static SpanLink link(String traceId) {
    SpanLink link = new SpanLink();
    link.setTraceId(traceId);
    return link;
  }
}
