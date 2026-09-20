// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.sink;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.assertj.core.api.Assertions.catchThrowable;

import java.util.Map;
import org.apache.flink.connector.elasticsearch.sink.BulkResponseInspector;
import org.apache.flink.metrics.SimpleCounter;
import org.apache.flink.util.FlinkRuntimeException;
import org.elasticsearch.action.DocWriteRequest;
import org.elasticsearch.action.bulk.BulkItemResponse;
import org.elasticsearch.action.bulk.BulkRequest;
import org.elasticsearch.action.bulk.BulkResponse;
import org.elasticsearch.action.index.IndexRequest;
import org.elasticsearch.action.index.IndexResponse;
import org.elasticsearch.index.shard.ShardId;
import org.elasticsearch.rest.RestStatus;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.EnumSource;

/** 使用真实 bulk 请求和响应验证指标计数及单条失败的传播。 */
class PrecalculateBulkResponseInspectorFactoryTest {

  private SimpleCounter recordsTotal;
  private SimpleCounter recordsFailedTotal;
  private BulkResponseInspector inspector;

  @BeforeEach
  void setUp() {
    recordsTotal = new SimpleCounter();
    recordsFailedTotal = new SimpleCounter();
    inspector =
        PrecalculateBulkResponseInspectorFactory.createInspector(
            recordsTotal, recordsFailedTotal);
  }

  @Test
  void inspectSuccessfulBulkIncrementsRecordsTotal() {
    inspector.inspect(request(3), successfulResponse(3));

    assertThat(recordsTotal.getCount()).isEqualTo(3L);
    assertThat(recordsFailedTotal.getCount()).isEqualTo(0L);
  }

  @ParameterizedTest
  @EnumSource(value = RestStatus.class, names = {"BAD_REQUEST", "TOO_MANY_REQUESTS"})
  void failedItemIsCountedAndPropagated(RestStatus status) {
    RuntimeException cause = new IllegalStateException("write rejected");
    BulkResponse response =
        new BulkResponse(new BulkItemResponse[] {failedItem(0, status, cause)}, 0L);

    assertThatThrownBy(() -> inspector.inspect(request(1), response))
        .isInstanceOf(FlinkRuntimeException.class)
        .hasRootCause(cause);

    assertThat(recordsTotal.getCount()).isEqualTo(1L);
    assertThat(recordsFailedTotal.getCount()).isEqualTo(1L);
  }

  @Test
  void partialFailureCountsAllFailedItemsBeforeThrowing() {
    RuntimeException firstCause = new IllegalArgumentException("mapping rejected");
    RuntimeException secondCause = new IllegalStateException("retries exhausted");
    BulkResponse response = new BulkResponse(new BulkItemResponse[] {
        successfulItem(0),
        failedItem(1, RestStatus.BAD_REQUEST, firstCause),
        failedItem(2, RestStatus.TOO_MANY_REQUESTS, secondCause)
    }, 0L);

    Throwable failure = catchThrowable(() -> inspector.inspect(request(3), response));

    assertThat(failure).isInstanceOf(FlinkRuntimeException.class).hasRootCause(firstCause);
    assertThat(failure.getSuppressed()).hasSize(1);
    assertThat(failure.getSuppressed()[0]).hasRootCause(secondCause);
    assertThat(recordsTotal.getCount()).isEqualTo(3L);
    assertThat(recordsFailedTotal.getCount()).isEqualTo(2L);
  }

  @Test
  void inspectTracksActionCountAcrossMultipleFlushes() {
    inspector.inspect(request(10), successfulResponse(10));
    inspector.inspect(request(800), successfulResponse(800));

    assertThat(recordsTotal.getCount()).isEqualTo(810L);
  }

  private static BulkRequest request(int actionCount) {
    BulkRequest request = new BulkRequest();
    for (int i = 0; i < actionCount; i++) {
      request.add(new IndexRequest("rum-test-index").id("doc-" + i).source(Map.of("count", i)));
    }
    return request;
  }

  private static BulkResponse successfulResponse(int actionCount) {
    BulkItemResponse[] items = new BulkItemResponse[actionCount];
    for (int i = 0; i < actionCount; i++) {
      items[i] = successfulItem(i);
    }
    return new BulkResponse(items, 0L);
  }

  private static BulkItemResponse successfulItem(int itemId) {
    IndexResponse response = new IndexResponse(
        new ShardId("rum-test-index", "uuid", 0), "_doc", "doc-" + itemId,
        itemId, 1L, 1L, true);
    return new BulkItemResponse(itemId, DocWriteRequest.OpType.INDEX, response);
  }

  private static BulkItemResponse failedItem(int itemId, RestStatus status, Exception cause) {
    return new BulkItemResponse(itemId, DocWriteRequest.OpType.INDEX,
        new BulkItemResponse.Failure("rum-test-index", "_doc", "doc-" + itemId, cause, status));
  }
}
