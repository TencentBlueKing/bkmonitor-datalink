// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.serde;

import static org.assertj.core.api.Assertions.assertThat;

import com.tencent.bk.bkmonitor.rum.model.RumEvent;
import java.nio.charset.StandardCharsets;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

class RumEventDeserializationSchemaTest {

  @ParameterizedTest
  @ValueSource(strings = {
      "\"bk_biz_id\":[]",
      "\"bk_biz_id\":{\"nested\":[{}]}",
      "\"bk_biz_id\":\"invalid\"",
      "\"status\":{\"code\":[]}",
      "\"attributes\":\"invalid\""
  })
  void consumesEntireMalformedItemBeforeReadingNextSibling(String invalidField) throws Exception {
    String validItem =
        "{\"span_name\":\"%s\",\"start_time\":1785847595000000,"
            + "\"attributes\":{\"session.id\":\"session\",\"span_type\":\"click\"}}";
    String json =
        "{\"items\":["
            + String.format(validItem, "first")
            + ",{\"span_name\":\"bad\"," + invalidField
            + ",\"resource\":{\"device.type\":\"mobile\"},\"links\":[{}]},"
            + String.format(validItem, "last")
            + "],\"bk_biz_id\":42,\"app_name\":\"root-app\"}";

    List<RumEvent> events =
        new RumEventDeserializationSchema().deserializeMany(json.getBytes(StandardCharsets.UTF_8));

    assertThat(events).extracting(event -> event.getSpan().getSpanName())
        .containsExactly("first", "last");
    assertThat(events).allSatisfy(event -> {
      assertThat(event.getSpan().getBkBizId()).isEqualTo(42);
      assertThat(event.getSpan().getAppName()).isEqualTo("root-app");
    });
  }

  @Test
  void expandsSpanEnvelopeItemsIntoRumEvents() throws Exception {
    String json =
        "{"
            + "\"bizid\":1,"
            + "\"bk_biz_id\":1,"
            + "\"datetime\":\"2026-08-04 20:46:35\","
            + "\"items\":[{"
            + "\"app_name\":\"ot-demo\","
            + "\"attributes\":{"
            + "\"session.id\":\"1fbc2a4587a7a4200fdb5cb03adff965\","
            + "\"span_type\":\"view\","
            + "\"user.id\":\"user_1784637610515_5aa4e4df\","
            + "\"view.referrer\":\"http://localhost:8089/\","
            + "\"view.url\":\"http://localhost:8089/\","
            + "\"view.cls\":0.123,"
            + "\"view.lcp\":1200"
            + "},"
            + "\"bk_biz_id\":2,"
            + "\"end_time\":1785241902220000,"
            + "\"resource\":{"
            + "\"deployment.environment.name\":\"production\","
            + "\"device.type\":\"desktop\","
            + "\"service.version\":\"1.0.0\""
            + "},"
            + "\"span_id\":\"65d098884e12b153\","
            + "\"span_name\":\"browser.view\","
            + "\"start_time\":1785241902220000,"
            + "\"status\":{\"code\":0,\"message\":\"\"},"
            + "\"trace_id\":\"8a9c04cff257c21fd7cb2e37ec0f75a9\""
            + "}],"
            + "\"time\":1785847595,"
            + "\"utctime\":\"2026-08-04 12:46:35\""
            + "}";

    RumEventDeserializationSchema schema = new RumEventDeserializationSchema();
    List<RumEvent> events = schema.deserializeMany(json.getBytes(StandardCharsets.UTF_8));

    assertThat(events.size()).isEqualTo(1);
    RumEvent event = events.get(0);

    assertThat(event.getEventTime()).isEqualTo(1785241902220000L);
  }

  @Test
  void expandsMultipleSpanEnvelopeItemsInOrder() throws Exception {
    String json =
        "{"
            + "\"bk_biz_id\":1,"
            + "\"app_name\":\"envelope-app\","
            + "\"datetime\":\"2026-08-04 20:46:35\","
            + "\"items\":["
            + "{\"app_name\":\"ot-demo\",\"bk_biz_id\":2,\"trace_id\":\"trace-1\",\"span_id\":\"span-1\","
            + "\"span_name\":\"browser.view\",\"start_time\":1785241902220000,\"end_time\":1785241902222000,"
            + "\"attributes\":{\"session.id\":\"sess-1\",\"span_type\":\"view\"},"
            + "\"status\":{\"code\":0,\"message\":\"\"}},"
            + "{\"trace_id\":\"trace-2\",\"span_id\":\"span-2\",\"span_name\":\"browser.click\","
            + "\"start_time\":1785241902400000,"
            + "\"attributes\":{\"session.id\":\"sess-1\",\"span_type\":\"click\"}},"
            + "{\"app_name\":\"ot-demo\",\"bk_biz_id\":2,\"trace_id\":\"trace-3\",\"span_id\":\"span-3\","
            + "\"span_name\":\"browser.error\",\"start_time\":1785241902600000,"
            + "\"attributes\":{\"session.id\":\"sess-1\"},"
            + "\"status\":{\"code\":2,\"message\":\"boom\"}}"
            + "]}";

    List<RumEvent> events =
        new RumEventDeserializationSchema().deserializeMany(json.getBytes(StandardCharsets.UTF_8));

    assertThat(events).hasSize(3);
    assertThat(events)
        .extracting(event -> event.getSpan().getSpanName())
        .containsExactly("browser.view", "browser.click", "browser.error");
    assertThat(events.get(0).getEventTime()).isEqualTo(1785241902220000L);
    assertThat(events.get(0).getEndTime()).isEqualTo(1785241902222000L);
    assertThat(events.get(1).getEndTime()).isEqualTo(events.get(1).getEventTime());
    assertThat(events.get(2).getStatus().getCode()).isEqualTo(2);
  }

  @Test
  void acceptsSpanStatusAlias() throws Exception {
    String json =
        "{\"items\":[{\"start_time\":1785847595000000,"
            + "\"attributes\":{\"session.id\":\"s\",\"span_type\":\"resource\"},"
            + "\"spanStatus\":{\"code\":2,\"message\":\"boom\"}}]}";

    RumEvent event =
        new RumEventDeserializationSchema()
            .deserializeMany(json.getBytes(StandardCharsets.UTF_8))
            .get(0);

    assertThat(event.getStatus().getCode()).isEqualTo(2);
    assertThat(event.getStatus().getMessage()).isEqualTo("boom");
  }

  @Test
  void mapsResourceSpansToResourceEventType() throws Exception {
    // 同一 session 下两条资源 span:一条成功加载(-> resource),一条加载失败(-> resource_error)。
    String json =
        "{"
            + "\"items\":["
            // 成功的静态资源加载:span_type=resource,status.code=0 -> resource
            + "{\"trace_id\":\"t1\",\"span_id\":\"s1\",\"span_name\":\"browser.resource.fetch\","
            + "\"attributes\":{\"session.id\":\"sess\",\"span_type\":\"resource\","
            + "\"view.url\":\"http://app/home\"},\"status\":{\"code\":0}},"
            // 失败的静态资源加载:status.code!=0 且 span_name 含 resource -> resource_error
            + "{\"trace_id\":\"t2\",\"span_id\":\"s2\",\"span_name\":\"browser.resource.load\","
            + "\"attributes\":{\"session.id\":\"sess\",\"view.url\":\"http://app/home\","
            + "\"error.message\":\"404 not found\"},"
            + "\"status\":{\"code\":1,\"message\":\"404 not found\"}}"
            + "]}";

    List<RumEvent> events =
        new RumEventDeserializationSchema().deserializeMany(json.getBytes(StandardCharsets.UTF_8));

    assertThat(events).hasSize(2);
    assertThat(events.get(0).getSpanType()).isEqualTo("resource");
    assertThat(events.get(0).getStatus().getCode()).isZero();
    assertThat(events.get(1).getStatus().getCode()).isEqualTo(1);
    assertThat(events.get(1).getSpan().getSpanName()).contains("resource");
  }

  @Test
  void readsRootFallbackFieldsAfterItemsArray() throws Exception {
    // 生产 envelope 的 root 字段顺序不固定；items 后面的时间和维度也必须参与回退。
    String json =
        "{"
            + "\"items\":[{\"span_name\":\"browser.click\","
            + "\"attributes\":{\"session.id\":\"s-after\",\"span_type\":\"click\"}}],"
            + "\"time\":1785847595,\"app_name\":\"root-app\",\"bk_biz_id\":42"
            + "}";

    List<RumEvent> events =
        new RumEventDeserializationSchema().deserializeMany(json.getBytes(StandardCharsets.UTF_8));

    assertThat(events.size()).isEqualTo(1);
    assertThat(events.get(0).getEventTime()).isEqualTo(1785847595000000L);
    assertThat(events.get(0).getSpan().getAppName()).isEqualTo("root-app");
    assertThat(events.get(0).getSpan().getBkBizId()).isEqualTo(42);
  }

  @Test
  void parsesFlattenedLegacyEvent() throws Exception {
    String json =
        "{"
            + "\"session_id\":\"legacy-session\","
            + "\"event_time\":1785847595,"
            + "\"span_type\":\"click\","
            + "\"span_name\":\"browser.click\","
            + "\"attributes\":{\"view.id\":\"legacy-view\"},"
            + "\"resource\":{\"device.type\":\"mobile\"},"
            + "\"status\":{\"code\":0}"
            + "}";

    List<RumEvent> events =
        new RumEventDeserializationSchema().deserializeMany(json.getBytes(StandardCharsets.UTF_8));

    assertThat(events.size()).isEqualTo(1);
    RumEvent event = events.get(0);
    assertThat(event.getSessionId()).isEqualTo("legacy-session");
    assertThat(event.getSpanType()).isEqualTo("click");
    assertThat(event.getEventTime()).isEqualTo(1785847595000000L);
    assertThat(event.getSpan().getSpanName()).isEqualTo("browser.click");
    assertThat(event.getResource().getDeviceType()).isEqualTo("mobile");
  }

  @Test
  void parsesAlreadyNormalizedLegacyEvent() throws Exception {
    String json =
        "{"
            + "\"sessionId\":\"normalized-session\","
            + "\"eventTime\":1785847595000,"
            + "\"spanType\":\"resource\","
            + "\"span\":{\"span_name\":\"browser.resource\","
            + "\"attributes\":{\"view.id\":\"normalized-view\"}}"
            + "}";

    List<RumEvent> events =
        new RumEventDeserializationSchema().deserializeMany(json.getBytes(StandardCharsets.UTF_8));

    assertThat(events.size()).isEqualTo(1);
    assertThat(events.get(0).getSessionId()).isEqualTo("normalized-session");
    assertThat(events.get(0).getEventTime()).isEqualTo(1785847595000000L);
    assertThat(events.get(0).getSpan().getSpanName()).isEqualTo("browser.resource");
  }

  @Test
  void skipsMalformedItemsButKeepsValidSiblingsInOrder() throws Exception {
    String json =
        "{\"items\":["
            + "{\"span_name\":\"first\",\"start_time\":1785847595000000,"
            + "\"attributes\":{\"session.id\":\"good-1\",\"span_type\":\"click\"}},"
            + "\"not-an-object\","
            + "{\"span_name\":\"bad-schema\",\"attributes\":\"must-be-an-object\"},"
            + "{\"span_name\":\"last\",\"start_time\":1785847596000000,"
            + "\"attributes\":{\"session.id\":\"good-2\",\"span_type\":\"resource\"}}"
            + "]}";

    List<RumEvent> events =
        new RumEventDeserializationSchema().deserializeMany(json.getBytes(StandardCharsets.UTF_8));

    assertThat(events.size()).isEqualTo(2);
    assertThat(events.get(0).getSessionId()).isEqualTo("good-1");
    assertThat(events.get(1).getSessionId()).isEqualTo("good-2");
  }

  @Test
  void resolvesRootFallbackTimeFromSpaceDateTimeString() throws Exception {
    // span 自身缺 start_time，回退到 root 的 datetime 字符串；验证 readScalarAsEpochMicros
    // 对 VALUE_STRING 走 timestampFromText 的路径与原 timestamp(JsonNode) 等价。
    String json =
        "{\"datetime\":\"2026-08-04 12:46:35\","
            + "\"items\":[{\"span_name\":\"browser.click\","
            + "\"attributes\":{\"session.id\":\"s-time\",\"span_type\":\"click\"}}]}";

    List<RumEvent> events =
        new RumEventDeserializationSchema().deserializeMany(json.getBytes(StandardCharsets.UTF_8));

    assertThat(events.size()).isEqualTo(1);
    // 2026-08-04 12:46:35 UTC = 1785847595 epoch sec = 1785847595000000 micros
    assertThat(events.get(0).getEventTime()).isEqualTo(1785847595000000L);
  }

  @Test
  void resolvesRootFallbackTimeFromNumericSecondsField() throws Exception {
    // root time 是秒级数值，验证 VALUE_NUMBER_INT 走 normalizeEpochMicros 路径。
    String json =
        "{\"time\":1785847595,"
            + "\"items\":[{\"span_name\":\"browser.click\","
            + "\"attributes\":{\"session.id\":\"s-num\",\"span_type\":\"click\"}}]}";

    List<RumEvent> events =
        new RumEventDeserializationSchema().deserializeMany(json.getBytes(StandardCharsets.UTF_8));

    assertThat(events.size()).isEqualTo(1);
    assertThat(events.get(0).getEventTime()).isEqualTo(1785847595000000L);
  }

  @Test
  void resolvesRootBizIdFromStringAndInheritsToItem() throws Exception {
    // bk_biz_id 根字段为字符串，验证 readScalarAsInteger 对 VALUE_STRING 的解析。
    String json =
        "{\"bk_biz_id\":\"42\","
            + "\"items\":[{\"span_name\":\"browser.click\","
            + "\"attributes\":{\"session.id\":\"s-biz\",\"span_type\":\"click\"}}]}";

    List<RumEvent> events =
        new RumEventDeserializationSchema().deserializeMany(json.getBytes(StandardCharsets.UTF_8));

    assertThat(events.size()).isEqualTo(1);
    assertThat(events.get(0).getSpan().getBkBizId()).isEqualTo(42);
  }

  @Test
  void malformedRecordIsIsolatedAsEmptyOutput() throws Exception {
    RumEventDeserializationSchema schema = new RumEventDeserializationSchema();

    assertThat(
        schema
            .deserializeMany("{\"items\":[{".getBytes(StandardCharsets.UTF_8))
            .size())
        .isEqualTo(0);
    assertThat(
        schema
            .deserializeMany("{\"items\":{}}".getBytes(StandardCharsets.UTF_8))
            .size())
        .isEqualTo(0);
    assertThat(schema.deserializeMany(new byte[0]).size()).isEqualTo(0);
  }
}
