// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.sink;

import static org.apache.flink.util.ExceptionUtils.firstOrSuppressed;

import java.io.Serializable;
import org.apache.flink.connector.elasticsearch.sink.BulkResponseInspector;
import org.apache.flink.metrics.Counter;
import org.apache.flink.metrics.MetricGroup;
import org.apache.flink.util.FlinkRuntimeException;
import org.elasticsearch.action.bulk.BulkItemResponse;
import org.elasticsearch.action.bulk.BulkRequest;
import org.elasticsearch.action.bulk.BulkResponse;

/**
 * 统计预计算 ES sink 已收到 bulk 响应的 action 总数及其中的失败数。
 *
 * <p>{@code es.bulk_records_total} 包含响应中的成功与失败 action，区别于 connector 在
 * action 加入发送缓冲时递增的 {@code numRecordsSend}。{@code es.bulk_records_failed_total}
 * 统计 item 级失败；无响应的网络错误由 connector 的失败日志与作业重启信号定位。
 *
 * <p>统计完全部失败项后仍抛出异常，保留 connector 的故障恢复语义，防止 checkpoint
 * 确认尚未成功写入的记录。指标只覆盖当前 writer 生命周期，不是持久化业务账本。
 */
public class PrecalculateBulkResponseInspectorFactory
    implements BulkResponseInspector.BulkResponseInspectorFactory, Serializable {
  private static final long serialVersionUID = 1L;

  @Override
  public BulkResponseInspector apply(
      BulkResponseInspector.BulkResponseInspectorFactory.InitContext initContext) {
    MetricGroup group = initContext.metricGroup().addGroup("es");
    return createInspector(
        group.counter("bulk_records_total"),
        group.counter("bulk_records_failed_total"));
  }

  /**
   * 构造真正的 inspector。独立成方法便于在单元测试中传入测试替身,绕开 MetricGroup 注册链路。
   */
  static BulkResponseInspector createInspector(
      Counter recordsTotal, Counter recordsFailedTotal) {
    return (BulkRequest request, BulkResponse response) -> {
      recordsTotal.inc(request.numberOfActions());
      if (response.hasFailures()) {
        FlinkRuntimeException failures = null;
        for (BulkItemResponse item : response) {
          if (item.isFailed()) {
            recordsFailedTotal.inc();
            BulkItemResponse.Failure failure = item.getFailure();
            failures = firstOrSuppressed(
                new FlinkRuntimeException(
                    String.format(
                        "Elasticsearch bulk item %d failed (index=%s, id=%s, status=%s).",
                        item.getItemId(), failure.getIndex(), failure.getId(), failure.getStatus()),
                    failure.getCause()),
                failures);
          }
        }
        if (failures != null) {
          throw failures;
        }
      }
    };
  }
}
