// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.sink.view;

import com.tencent.bk.bkmonitor.rum.model.ViewEventDocument;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateStorage;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateStorageCache;
import com.tencent.bk.bkmonitor.rum.sink.AbstractPrecalculateEmitter;
import com.tencent.bk.bkmonitor.rum.utils.TimeUtils;
import java.util.Map;

/** View 聚合文档的 ES emitter，只负责绑定 View 业务适配器。 */
public final class ViewPrecalculateEmitter extends AbstractPrecalculateEmitter<ViewEventDocument> {

  private static final long serialVersionUID = 1L;

  public ViewPrecalculateEmitter(long clusterId, int cacheMaxSize, int cacheTtlMinutes) {
    this(clusterId, cacheMaxSize, cacheTtlMinutes, PrecalculateStorage.DEFAULT_DISPERSED_COUNT);
  }

  /** 分表数随 emitter 一起传到 TaskManager，恢复缓存时无需进程级配置。 */
  public ViewPrecalculateEmitter(
      long clusterId, int cacheMaxSize, int cacheTtlMinutes, int dispersedCount) {
    super(
        ViewPrecalculateDocumentAdapter.INSTANCE, clusterId, cacheMaxSize, cacheTtlMinutes,
        dispersedCount);
  }

  /** 测试用：注入预构建缓存（绕过配置）。 */
  public static ViewPrecalculateEmitter withCache(PrecalculateStorageCache cache) {
    ViewPrecalculateEmitter emitter = new ViewPrecalculateEmitter(0L, 0, 0);
    emitter.setCacheForTest(cache);
    return emitter;
  }

  /** 字段映射入口，便于直接测试 View schema。 */
  public static Map<String, Object> toDocument(ViewEventDocument document) {
    Map<String, Object> fields = ViewPrecalculateDocumentAdapter.toDocument(document);
    fields.put("time", System.currentTimeMillis() * TimeUtils.MS_TO_US);
    return fields;
  }
}
