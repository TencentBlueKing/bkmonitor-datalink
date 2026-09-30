// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.sink;

import com.tencent.bk.bkmonitor.rum.model.SessionEvent;
import com.tencent.bk.bkmonitor.rum.model.ViewEventDocument;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateFamily;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateRecord;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateStorage;
import com.tencent.bk.bkmonitor.rum.sink.session.SessionPrecalculateEmitter;
import com.tencent.bk.bkmonitor.rum.sink.view.ViewPrecalculateEmitter;
import com.tencent.bk.bkmonitor.rum.utils.TimeUtils;
import org.apache.flink.connector.base.DeliveryGuarantee;
import org.apache.flink.connector.elasticsearch.sink.Elasticsearch7SinkBuilder;
import org.apache.flink.connector.elasticsearch.sink.ElasticsearchEmitter;
import org.apache.flink.connector.elasticsearch.sink.ElasticsearchSink;
import org.apache.flink.connector.elasticsearch.sink.FlushBackoffType;
import org.apache.flink.util.ParameterTool;
import org.apache.http.HttpHost;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * 预计算索引 Elasticsearch sink 工厂。
 *
 * <p>本类只负责 ES 连接、bulk 参数和 emitter 装配。记录路由、缓存和字段映射分别由 emitter 基类与业务适配器负责。
 * 每条记录按 {@code (bkBizId, appName)} 解析到对应分表，写入
 * {@code write_{yyyyMMdd}_rum_global_{kind}_precalculate_auto_N}。
 *
 * <p>连接 / bulk / 认证参数复用 {@code sink.es.*}（与其它 sink 共享）；预计算专属参数：
 * <ul>
 *   <li>{@code precalculate.dispersed-count} — 分表数量，默认 5
 *   <li>{@code precalculate.es.cluster-id} — 节点 key 中的 clusterId，默认 1
 *   <li>{@code precalculate.es.cache.max-size} — Caffeine 最大条目数，默认 10000
 *   <li>{@code precalculate.es.cache.ttl-minutes} — Caffeine TTL（分钟），默认 60
 * </ul>
 *
 * <p>典型用法：
 * <pre>{@code
 * stream.sinkTo(PrecalculateElasticsearchSink.forView(parameters));
 * stream.sinkTo(PrecalculateElasticsearchSink.forSession(parameters));
 * }</pre>
 */
public final class PrecalculateElasticsearchSink {

  private static final String DEFAULT_HOSTS = "http://localhost:9200";
  private static final int DEFAULT_CLUSTER_ID = 1;
  private static final int DEFAULT_CACHE_MAX_SIZE = 10_000;
  private static final int DEFAULT_CACHE_TTL_MINUTES = 60;
  private static final String DISPERSED_COUNT_PARAMETER = "precalculate.dispersed-count";
  private static final Logger LOG = LoggerFactory.getLogger(PrecalculateElasticsearchSink.class);

  private PrecalculateElasticsearchSink() {}

  /** View 文档（{@link ViewEventDocument}）→ view 预计算索引。 */
  public static ElasticsearchSink<ViewEventDocument> forView(ParameterTool parameters) {
    return build(
        parameters,
        PrecalculateFamily.VIEW,
        new ViewPrecalculateEmitter(
            parameters.getLong("precalculate.es.cluster-id", DEFAULT_CLUSTER_ID),
            parameters.getInt("precalculate.es.cache.max-size", DEFAULT_CACHE_MAX_SIZE),
            parameters.getInt("precalculate.es.cache.ttl-minutes", DEFAULT_CACHE_TTL_MINUTES),
            parameters.getInt(DISPERSED_COUNT_PARAMETER, PrecalculateStorage.DEFAULT_DISPERSED_COUNT)));
  }

  /** Session 文档（{@link SessionEvent}）→ session 预计算索引。 */
  public static ElasticsearchSink<SessionEvent> forSession(ParameterTool parameters) {
    return build(
        parameters,
        PrecalculateFamily.SESSION,
        new SessionPrecalculateEmitter(
            parameters.getLong("precalculate.es.cluster-id", DEFAULT_CLUSTER_ID),
            parameters.getInt("precalculate.es.cache.max-size", DEFAULT_CACHE_MAX_SIZE),
            parameters.getInt("precalculate.es.cache.ttl-minutes", DEFAULT_CACHE_TTL_MINUTES),
            parameters.getInt(DISPERSED_COUNT_PARAMETER, PrecalculateStorage.DEFAULT_DISPERSED_COUNT)));
  }

  /** 通用入口：任何 {@link PrecalculateRecord} 实现类。 */
  public static <T extends PrecalculateRecord> ElasticsearchSink<T> create(
      ParameterTool parameters, PrecalculateFamily family) {
    return build(
        parameters,
        family,
        new GenericPrecalculateEmitter<>(
            family,
            parameters.getLong("precalculate.es.cluster-id", DEFAULT_CLUSTER_ID),
            parameters.getInt("precalculate.es.cache.max-size", DEFAULT_CACHE_MAX_SIZE),
            parameters.getInt("precalculate.es.cache.ttl-minutes", DEFAULT_CACHE_TTL_MINUTES),
            parameters.getInt(DISPERSED_COUNT_PARAMETER, PrecalculateStorage.DEFAULT_DISPERSED_COUNT)));
  }

  private static <T> ElasticsearchSink<T> build(
      ParameterTool parameters,
      PrecalculateFamily family,
      ElasticsearchEmitter<T> emitter) {
    Elasticsearch7SinkBuilder<T> builder = new Elasticsearch7SinkBuilder<>();
    builder.setHosts(parseHosts(parameters.get("sink.es.hosts", DEFAULT_HOSTS)));
    builder.setEmitter(emitter);
    builder.setDeliveryGuarantee(DeliveryGuarantee.AT_LEAST_ONCE);
    builder.setBulkFlushMaxActions(parameters.getInt("sink.es.bulk.max.actions", 1000));
    builder.setBulkFlushMaxSizeMb(parameters.getInt("sink.es.bulk.max.size.mb", 5));
    builder.setBulkFlushInterval(parameters.getLong("sink.es.bulk.flush.interval.ms", 2000L));
    builder.setBulkFlushBackoffStrategy(
        FlushBackoffType.EXPONENTIAL,
        parameters.getInt("sink.es.bulk.backoff.retries", 3),
        parameters.getLong("sink.es.bulk.backoff.delay.ms", TimeUtils.SECOND_TO_MS));
    builder.setConnectionRequestTimeout(
        parameters.getInt("sink.es.connection.request.timeout.ms", 5000));
    builder.setConnectionTimeout(parameters.getInt("sink.es.connection.timeout.ms", 5000));
    builder.setSocketTimeout(
        parameters.getInt("sink.es.socket.timeout.ms", (int) TimeUtils.MINUTE_TO_MS));

    String username = parameters.get("sink.es.username", null);
    String password = parameters.get("sink.es.password", null);
    if (username != null && password != null) {
      builder.setConnectionUsername(username);
      builder.setConnectionPassword(password);
    }

    String pathPrefix = parameters.get("sink.es.path-prefix", null);
    if (pathPrefix != null && !pathPrefix.trim().isEmpty()) {
      builder.setConnectionPathPrefix(pathPrefix);
    }
    if (parameters.getBoolean("sink.es.allow-insecure", false)) {
      builder.allowInsecure();
    }

    // 统计收到 bulk 响应的 action 总数及失败数；失败项仍触发 connector 故障恢复。
    builder.setBulkResponseInspectorFactory(new PrecalculateBulkResponseInspectorFactory());

    LOG.info(
        "PrecalculateElasticsearchSink built: family={}, clusterId={}, hosts={}",
        family,
        parameters.getLong("precalculate.es.cluster-id", DEFAULT_CLUSTER_ID),
        parameters.get("sink.es.hosts", DEFAULT_HOSTS));
    return builder.build();
  }

  private static HttpHost[] parseHosts(String rawHosts) {
    String[] parts = rawHosts.split(",");
    HttpHost[] hosts = new HttpHost[parts.length];
    for (int i = 0; i < parts.length; i++) {
      hosts[i] = HttpHost.create(parts[i].trim());
    }
    return hosts;
  }
}
