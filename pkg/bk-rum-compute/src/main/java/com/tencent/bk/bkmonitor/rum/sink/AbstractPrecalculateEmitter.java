// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.sink;

import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateStorage;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateStorageCache;
import com.tencent.bk.bkmonitor.rum.utils.TimeUtils;
import java.io.Serializable;
import java.time.Duration;
import java.time.LocalDate;
import java.time.format.DateTimeParseException;
import java.util.Map;
import java.util.Objects;
import javax.annotation.Nullable;
import org.apache.flink.api.connector.sink2.SinkWriter;
import org.apache.flink.connector.elasticsearch.sink.ElasticsearchEmitter;
import org.apache.flink.connector.elasticsearch.sink.RequestIndexer;
import org.elasticsearch.action.index.IndexRequest;
import org.elasticsearch.common.xcontent.XContentType;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * 预计算 ES emitter 的通用写入流程。
 *
 * <p>这里集中处理所有家族都相同的基础设施职责：缓存生命周期、存储路由、通用时间字段和
 * {@link IndexRequest} 构造。Session/View 的路由字段校验和业务字段映射通过
 * {@link PrecalculateDocumentAdapter} 注入，避免窗口写入机制依赖具体聚合模型。
 */
public abstract class AbstractPrecalculateEmitter<T> implements ElasticsearchEmitter<T>, Serializable {

  private static final long serialVersionUID = 2L;
  private static final Logger LOG = LoggerFactory.getLogger(AbstractPrecalculateEmitter.class);

  private final PrecalculateDocumentAdapter<T> adapter;
  private final long clusterId;
  private final int cacheMaxSize;
  private final int cacheTtlMinutes;
  private final int dispersedCount;

  /** Caffeine cache 不是 emitter 配置的一部分，重启后由 {@link #open()} 重建。 */
  @Nullable
  private transient PrecalculateStorageCache cache;

  protected AbstractPrecalculateEmitter(
      PrecalculateDocumentAdapter<T> adapter,
      long clusterId,
      int cacheMaxSize,
      int cacheTtlMinutes,
      int dispersedCount) {
    if (dispersedCount < 1) {
      throw new IllegalArgumentException("dispersedCount must be >= 1, got: " + dispersedCount);
    }
    this.adapter = Objects.requireNonNull(adapter, "adapter");
    this.clusterId = clusterId;
    this.cacheMaxSize = cacheMaxSize;
    this.cacheTtlMinutes = cacheTtlMinutes;
    this.dispersedCount = dispersedCount;
  }

  @Override
  public final void open() {
    if (cache != null) {
      return;
    }
    cache =
        new PrecalculateStorageCache(
            clusterId, cacheMaxSize, Duration.ofMinutes(cacheTtlMinutes), dispersedCount);
    LOG.info(
        "{} emitter cache initialized: clusterId={}, maxSize={}, ttlMinutes={}, dispersedCount={}",
        adapter.family(),
        clusterId,
        cacheMaxSize,
        cacheTtlMinutes,
        dispersedCount);
  }

  @Override
  public final void close() {}

  @Override
  public final void emit(T record, SinkWriter.Context context, RequestIndexer indexer) {
    if (!adapter.hasRequiredRoutingFields(record)) {
      LOG.warn("{} record missing routing fields, skip", adapter.family());
      return;
    }

    String appName = adapter.appName(record);
    String documentId = adapter.documentId(record);
    long bkBizId = adapter.bkBizId(record);
    PrecalculateStorage storage =
        cache.get(adapter.family(), bkBizId, appName, indexDate(record));
    Map<String, Object> document = adapter.toDocumentFields(record);
    document.put("time", System.currentTimeMillis() * TimeUtils.MS_TO_US);
    IndexRequest request =
        new IndexRequest(storage.writeAlias)
            .id(documentId)
            .source(document, XContentType.JSON);
    if (LOG.isDebugEnabled()) {
      LOG.debug(
          "write ES: family={}, index={}, docId={}, bkBizId={}, appName={}",
          adapter.family(),
          storage.writeAlias,
          documentId,
          bkBizId,
          appName);
    }
    indexer.add(request);
  }

  /** 测试用注入点，绕过配置构建缓存。 */
  protected final void setCacheForTest(PrecalculateStorageCache cache) {
    this.cache = cache;
  }

  private LocalDate indexDate(T record) {
    try {
      return LocalDate.parse(adapter.indexDate(record));
    } catch (DateTimeParseException e) {
      throw new IllegalArgumentException(
          adapter.family() + " record has invalid index date: " + adapter.indexDate(record), e);
    }
  }
}
