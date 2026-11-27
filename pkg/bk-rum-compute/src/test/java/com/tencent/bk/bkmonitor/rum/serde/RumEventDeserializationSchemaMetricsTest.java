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

import java.nio.charset.StandardCharsets;
import org.apache.flink.api.common.serialization.DeserializationSchema.InitializationContext;
import org.apache.flink.metrics.Counter;
import org.apache.flink.metrics.MetricGroup;
import org.apache.flink.metrics.SimpleCounter;
import org.apache.flink.metrics.groups.UnregisteredMetricsGroup;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/** 验证坏消息与坏 item 的计数口径，以及坏数据之后正常消息仍可解析。 */
class RumEventDeserializationSchemaMetricsTest {

  private RecordingMetricGroup serdeGroup;
  private RumEventDeserializationSchema schema;

  @BeforeEach
  void setUp() throws Exception {
    serdeGroup = new RecordingMetricGroup();
    // 根 MetricGroup:自身不记录,只把 addGroup("serde") 指向 serdeGroup。
    MetricGroup root =
        new UnregisteredMetricsGroup() {
          @Override
          public MetricGroup addGroup(String name) {
            if ("serde".equals(name)) {
              return serdeGroup;
            }
            return this;
          }
        };
    schema = new RumEventDeserializationSchema();
    schema.open(new SimpleInitializationContext(root));
  }

  @Test
  void malformedRecordsAreCountedAndDoNotBlockFollowingRecords() throws Exception {
    assertThat(schema.deserializeMany(null)).isEmpty();
    for (String message : new String[] {"", "not-json", "[]", "{\"items\":{}}", "{}"}) {
      assertThat(schema.deserializeMany(message.getBytes(StandardCharsets.UTF_8))).isEmpty();
    }
    assertThat(schema.deserializeMany(
        "{\"items\":[{\"span_name\":\"valid\"}]}".getBytes(StandardCharsets.UTF_8)))
        .hasSize(1);

    assertThat(serdeGroup.counters.get("malformed_records").getCount()).isEqualTo(6L);
    assertThat(serdeGroup.counters.get("malformed_items").getCount()).isZero();
  }

  @Test
  void malformedItemsAreCountedWithoutDiscardingValidSiblings() throws Exception {
    String message = "{\"items\":[null,7,{\"attributes\":\"invalid\"},"
        + "{\"span_name\":\"valid\"}]}";

    assertThat(schema.deserializeMany(message.getBytes(StandardCharsets.UTF_8)))
        .singleElement()
        .satisfies(event -> assertThat(event.getSpan().getSpanName()).isEqualTo("valid"));

    assertThat(serdeGroup.counters.get("malformed_records").getCount()).isZero();
    assertThat(serdeGroup.counters.get("malformed_items").getCount()).isEqualTo(3L);
  }

  @Test
  void validEnvelopeAndFlatRecordsDoNotCountAsMalformed() throws Exception {
    assertThat(schema.deserializeMany("{\"items\":[]}".getBytes(StandardCharsets.UTF_8)))
        .isEmpty();
    assertThat(schema.deserializeMany(
        "{\"items\":[{\"span_name\":\"a\"},{\"span_name\":\"b\"}]}"
            .getBytes(StandardCharsets.UTF_8)))
        .hasSize(2);
    assertThat(schema.deserializeMany(
        "{\"span_name\":\"flat\"}".getBytes(StandardCharsets.UTF_8)))
        .hasSize(1);

    assertThat(serdeGroup.counters.get("malformed_records").getCount()).isZero();
    assertThat(serdeGroup.counters.get("malformed_items").getCount()).isZero();
  }

  private static final class RecordingMetricGroup extends UnregisteredMetricsGroup {
    final java.util.Map<String, Counter> counters = new java.util.HashMap<>();

    @Override
    public Counter counter(String name) {
      Counter counter = new SimpleCounter();
      counters.put(name, counter);
      return counter;
    }
  }

  /** 最小的 InitializationContext:只暴露 metricGroup,其他字段返回无害 stub。 */
  private static final class SimpleInitializationContext implements InitializationContext {
    private final MetricGroup metricGroup;

    SimpleInitializationContext(MetricGroup metricGroup) {
      this.metricGroup = metricGroup;
    }

    @Override
    public MetricGroup getMetricGroup() {
      return metricGroup;
    }

    @Override
    public org.apache.flink.util.UserCodeClassLoader getUserCodeClassLoader() {
      // 用一个 do-nothing 的匿名实现,因为 schema.open() 不会真的去加载用户代码类。
      return new org.apache.flink.util.UserCodeClassLoader() {
        @Override
        public ClassLoader asClassLoader() {
          return getClass().getClassLoader();
        }

        @Override
        public void registerReleaseHookIfAbsent(String name, Runnable releaseHook) {}
      };
    }
  }
}
