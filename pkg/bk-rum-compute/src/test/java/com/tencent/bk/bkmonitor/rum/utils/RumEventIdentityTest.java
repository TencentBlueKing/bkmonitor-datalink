// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.utils;

import static org.assertj.core.api.Assertions.assertThat;

import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import com.tencent.bk.bkmonitor.rum.serde.RumEventDeserializationSchema;
import java.nio.charset.StandardCharsets;
import java.util.List;
import com.tencent.bk.bkmonitor.rum.model.Span;
import org.junit.jupiter.api.Test;

class RumEventIdentityTest {

  @Test
  void separatesDifferentComponentBoundaries() {
    String first = RumEventIdentity.of(event(2, "a", "bc", "d"));
    String second = RumEventIdentity.of(event(2, "ab", "c", "d"));

    assertThat(first).isNotEqualTo(second);
  }

  @Test
  void envelopeDimensionsAfterItemsAreUsedForIdentity() throws Exception {
    String message =
        "{\"items\":[{\"span_name\":\"browser.click\",\"start_time\":1785847595000000,"
            + "\"trace_id\":\"trace-1\",\"span_id\":\"span-1\","
            + "\"attributes\":{\"session.id\":\"s\",\"view.id\":\"v\",\"span_type\":\"action\"}}],"
            + "\"app_name\":\"app\",\"bk_biz_id\":2}";

    List<RumEvent> events =
        new RumEventDeserializationSchema()
            .deserializeMany(message.getBytes(StandardCharsets.UTF_8));

    assertThat(events).hasSize(1);
    assertThat(RumEventIdentity.of(events.get(0)))
        .isEqualTo(RumEventIdentity.of(event(2, "app", "trace-1", "span-1")));
  }

  @Test
  void rejectsMissingIdentityComponent() {
    RumEvent event = event(2, "app", "trace", "span");
    event.getSpan().setTraceId(null);

    assertThat(RumEventIdentity.of(event)).isNull();
  }

  private static RumEvent event(int bkBizId, String appName, String traceId, String spanId) {
    Span span = new Span();
    span.setBkBizId(bkBizId);
    span.setAppName(appName);
    span.setTraceId(traceId);
    span.setSpanId(spanId);
    RumEvent event = new RumEvent();
    event.setSpan(span);
    return event;
  }
}
