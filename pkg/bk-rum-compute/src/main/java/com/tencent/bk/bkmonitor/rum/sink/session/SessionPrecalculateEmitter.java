// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package com.tencent.bk.bkmonitor.rum.sink.session;

import com.tencent.bk.bkmonitor.rum.model.SessionEvent;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateStorage;
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateStorageCache;
import com.tencent.bk.bkmonitor.rum.sink.AbstractPrecalculateEmitter;
import com.tencent.bk.bkmonitor.rum.utils.TimeUtils;
import java.util.Map;

/** Session 聚合文档的 ES emitter，只负责绑定 Session 业务适配器。 */
public final class SessionPrecalculateEmitter extends AbstractPrecalculateEmitter<SessionEvent> {

  private static final long serialVersionUID = 1L;

  public SessionPrecalculateEmitter(long clusterId, int cacheMaxSize, int cacheTtlMinutes) {
    this(clusterId, cacheMaxSize, cacheTtlMinutes, PrecalculateStorage.DEFAULT_DISPERSED_COUNT);
  }

  /** 分表数随 emitter 一起传到 TaskManager，恢复缓存时无需进程级配置。 */
  public SessionPrecalculateEmitter(
      long clusterId, int cacheMaxSize, int cacheTtlMinutes, int dispersedCount) {
    super(
        SessionPrecalculateDocumentAdapter.INSTANCE,
        clusterId,
        cacheMaxSize,
        cacheTtlMinutes,
        dispersedCount);
  }

  /** 测试用：注入预构建缓存（绕过配置）。 */
  public static SessionPrecalculateEmitter withCache(PrecalculateStorageCache cache) {
    SessionPrecalculateEmitter emitter = new SessionPrecalculateEmitter(0L, 0, 0);
    emitter.setCacheForTest(cache);
    return emitter;
  }

  /** 字段映射入口，便于直接测试 Session schema。 */
  public static Map<String, Object> toDocument(SessionEvent document) {
    Map<String, Object> fields = SessionPrecalculateDocumentAdapter.toDocument(document);
    fields.put("time", System.currentTimeMillis() * TimeUtils.MS_TO_US);
    return fields;
  }
}
