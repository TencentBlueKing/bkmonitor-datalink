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
import com.tencent.bk.bkmonitor.rum.precalculate.PrecalculateFamily;
import com.tencent.bk.bkmonitor.rum.sink.PrecalculateDocumentAdapter;
import com.tencent.bk.bkmonitor.rum.utils.RumEntityKey;

import java.util.LinkedHashMap;
import java.util.Map;

/** Session 聚合文档到预计算 ES 字段的适配器。 */
public final class SessionPrecalculateDocumentAdapter
    implements PrecalculateDocumentAdapter<SessionEvent> {

  private static final long serialVersionUID = 1L;
  static final SessionPrecalculateDocumentAdapter INSTANCE =
      new SessionPrecalculateDocumentAdapter();

  private SessionPrecalculateDocumentAdapter() {}

  @Override
  public PrecalculateFamily family() {
    return PrecalculateFamily.SESSION;
  }

  @Override
  public boolean hasRequiredRoutingFields(SessionEvent document) {
    return document.getBkBizId() != null
        && document.getAppName() != null
        && !document.getAppName().isBlank()
        && document.getSessionId() != null
        && !document.getSessionId().isBlank()
        && document.getWindowId() != null
        && !document.getWindowId().isBlank()
        && document.getDate() != null
        && !document.getDate().isBlank();
  }

  @Override
  public long bkBizId(SessionEvent document) {
    return document.getBkBizId();
  }

  @Override
  public String appName(SessionEvent document) {
    return document.getAppName();
  }

  @Override
  public String documentId(SessionEvent document) {
    return RumEntityKey.documentId(
        document.getBkBizId(), document.getAppName(), document.getSessionId(), document.getWindowId());
  }

  @Override
  public String indexDate(SessionEvent document) {
    return document.getDate();
  }

  @Override
  public Map<String, Object> toDocumentFields(SessionEvent s) {
    return toDocument(s);
  }

  /**
   * 把 SessionEvent 映射为 ES 文档字段，与 {@code rum-session-precalculate-auto} 模板对齐。
   *
   * <p>通用的预计算处理时刻 {@code time} 由 emitter 统一补充。
   */
  static Map<String, Object> toDocument(SessionEvent s) {
    Map<String, Object> document = new LinkedHashMap<>();
    document.put("bk_biz_id", s.getBkBizId());
    document.put("app_name", s.getAppName());
    document.put("attributes.session.id", s.getSessionId());
    document.put("window_id", s.getWindowId());
    document.put("attributes.user.id", s.getUserId());
    document.put("attributes.session.has_replay", s.isHasReplay());
    document.put("resource.service.name", s.getServiceName());
    document.put("resource.service.version", s.getServiceVersion());
    document.put("resource.deployment.environment.name", s.getEnvironment());
    document.put("resource.telemetry.sdk.name", s.getSdkName());
    document.put("resource.telemetry.sdk.language", s.getSdkLang());
    document.put("resource.telemetry.sdk.version", s.getSdkVersion());
    document.put("resource.user_agent.name", s.getBrowser());
    document.put("resource.user_agent.version", s.getBrowserVersion());
    document.put("resource.user_agent.os.name", s.getOs());
    document.put("resource.device.type", s.getDeviceType());
    document.put("attributes.network.effective_type", s.getNetworkEffectiveType());
    document.put("min_start_time", s.getMinStartTime());
    document.put("max_end_time", s.getMaxEndTime());
    document.put("date", s.getDate());
    document.put("view_count", s.getViewCount());
    document.put("action_count", s.getActionCount());
    document.put("resource_count", s.getResourceCount());
    document.put("error_count", s.getErrorCount());
    document.put("request_count", s.getRequestCount());
    document.put("long_task_count", s.getLongTaskCount());
    document.put("frustration_count", s.getFrustrationCount());
    document.put("trace_count", s.getTraceCount());
    document.put("close_reason", s.getEndReason());
    document.put("duration", s.getDuration());
    document.put("enter_url_template", s.getEnterUrlTemplate());
    document.put("exit_url_template", s.getExitUrlTemplate());
    document.put("attributes.view.loading_time", s.getAvgLoadTimeUs());
    document.put("web_vitals.lcp", s.getMaxLcpUs());
    document.put("web_vitals.inp", s.getMaxInpUs());
    document.put("web_vitals.cls", s.getMaxCls());
    document.put("request_error_count", s.getRequestErrorCount());
    document.put("updated_at_ts", s.getUpdatedAtTs());
    document.put("close_time", s.getClosTim());
    document.put("closed", s.isClosed());
    document.put("is_active", s.isActive());
    return document;
  }
}
