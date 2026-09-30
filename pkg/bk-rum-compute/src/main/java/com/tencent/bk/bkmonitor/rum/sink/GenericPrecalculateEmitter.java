// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.sink;

import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateFamily;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateRecord;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateStorage;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateStorageCache;
import java.util.LinkedHashMap;
import java.util.Map;

/** 通用预计算记录的 ES emitter。 */
public final class GenericPrecalculateEmitter<T extends PrecalculateRecord>
    extends AbstractPrecalculateEmitter<T> {

  private static final long serialVersionUID = 1L;

  GenericPrecalculateEmitter(
      PrecalculateFamily family, long clusterId, int cacheMaxSize, int cacheTtlMinutes) {
    this(family, clusterId, cacheMaxSize, cacheTtlMinutes, PrecalculateStorage.DEFAULT_DISPERSED_COUNT);
  }

  GenericPrecalculateEmitter(
      PrecalculateFamily family, long clusterId, int cacheMaxSize, int cacheTtlMinutes,
      int dispersedCount) {
    super(
        new GenericPrecalculateDocumentAdapter<>(family),
        clusterId,
        cacheMaxSize,
        cacheTtlMinutes,
        dispersedCount);
  }

  /** 测试用：注入预构建缓存。 */
  static <T extends PrecalculateRecord> GenericPrecalculateEmitter<T> withCache(
      PrecalculateFamily family, PrecalculateStorageCache cache) {
    GenericPrecalculateEmitter<T> emitter = new GenericPrecalculateEmitter<>(family, 0L, 0, 0);
    emitter.setCacheForTest(cache);
    return emitter;
  }

  private static final class GenericPrecalculateDocumentAdapter<T extends PrecalculateRecord>
      implements PrecalculateDocumentAdapter<T> {
    private static final long serialVersionUID = 1L;

    private final PrecalculateFamily family;

    private GenericPrecalculateDocumentAdapter(PrecalculateFamily family) {
      this.family = family;
    }

    @Override
    public PrecalculateFamily family() {
      return family;
    }

    @Override
    public boolean hasRequiredRoutingFields(T record) {
      return record.appName() != null
          && !record.appName().isBlank()
          && record.documentId() != null
          && record.indexDate() != null
          && !record.indexDate().isBlank();
    }

    @Override
    public long bkBizId(T record) {
      return record.bkBizId();
    }

    @Override
    public String appName(T record) {
      return record.appName();
    }

    @Override
    public String documentId(T record) {
      return record.documentId();
    }

    @Override
    public String indexDate(T record) {
      return record.indexDate();
    }

    @Override
    public Map<String, Object> toDocumentFields(T record) {
      return new LinkedHashMap<>(record.toDocument());
    }
  }
}
