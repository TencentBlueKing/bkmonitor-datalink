# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""执行宿主同步请求；只计算，不调度、不保存进度、不发布 Kafka。"""

from dataclasses import dataclass, replace
from datetime import datetime

from .base import BaseClientV2, Source
from .source_alerts import ActiveAlerts
from .plugins import bs_monitor
from .plugins import aliyuncms_customized, rest_api_push, tcop, skywalking_graphql
from .plugins import imc
from .plugins import esight, uc, prometheus, solarwinds, vcenter, zabbix, tingyun, qcloud_monitor, rizhiyi, bk_monitor


@dataclass(frozen=True)
class Plugin:
    """插件入口和真实支持的调用模式。"""

    client: type
    modes: tuple[str, ...]


@dataclass(frozen=True)
class ExecutorFactory:
    """可跨 spawn 传递的宿主能力装配，不在全局变量保存连接或租户。"""

    active_alerts: object = None
    plugin_files: object = None
    metrics: object = None

    def __call__(self):
        from .loader import load_plugin
        plugins = builtin_plugins()
        for name, path in (self.plugin_files or {}).items():
            plugins[name] = load_plugin(name, path)
        return Executor(plugins, active_alerts=self.active_alerts, metrics=self.metrics)


def builtin_plugins():
    """返回已迁入插件；未迁入的名称必须失败，不能静默回退 standard。"""
    plugins = {
        "bs_monitor": Plugin(bs_monitor.AlarmClient, ("metric", "fields")),
        "rest_api_push": Plugin(rest_api_push.AlarmClient, ("push",)),
        "aliyuncms_customized": Plugin(aliyuncms_customized.AlarmClient, ("push",)),
        "tcop": Plugin(tcop.AlarmClient, ("push",)),
        "skywalking_graphql": Plugin(skywalking_graphql.AlarmClient, ("pull",)),
        "imc": Plugin(imc.AlarmClient, ("pull",)),
        "esight": Plugin(esight.AlarmClient, ("pull",)),
        "uc": Plugin(uc.AlarmClient, ("pull", "recovery")),
        "prometheus": Plugin(prometheus.AlarmClient, ("push",)),
        "solarwinds": Plugin(solarwinds.AlarmClient, ("pull",)),
        "vcenter": Plugin(vcenter.AlarmClient, ("pull",)),
        "zabbix": Plugin(zabbix.AlarmClient, ("push",)),
        "tingyun": Plugin(tingyun.AlarmClient, ("pull",)),
        "rizhiyi": Plugin(rizhiyi.AlarmClient, ("pull",)),
        "bk_monitor": Plugin(bk_monitor.AlarmClient, ("pull", "recovery")),
        "qcloud_monitor": Plugin(qcloud_monitor.AlarmClient, ("pull", "recovery")),
    }
    # 旧 KAC 基类为所有来源提供辅助查询，空返回值不等于插件具备真实指标取数能力。
    for name, plugin in plugins.items():
        modes = plugin.modes + tuple(mode for mode in ("fields", "metric") if mode not in plugin.modes)
        plugins[name] = Plugin(plugin.client, modes)
    return plugins


class Executor:
    """固定插件注册表，每个请求创建独立实例，避免请求数据残留。"""

    def __init__(self, plugins=None, active_alerts=None, metrics=None):
        self.plugins = dict(builtin_plugins() if plugins is None else plugins)
        self.active_alerts = active_alerts
        self.metrics = metrics

    def execute(self, request):
        """成功返回事件，异常交给运输层分类；request_id 由宿主提供并跨重投保持。"""
        if request.get("version") != 1:
            raise ValueError("unsupported execution protocol version")
        for key in ("request_id", "bk_tenant_id", "source_id", "plugin", "mode"):
            if not isinstance(request.get(key), str) or not request[key]:
                raise ValueError(f"{key} is required")
        plugin = self.plugins.get(request["plugin"])
        if plugin is None:
            raise ValueError("unknown plugin")
        mode = request["mode"]
        if mode not in plugin.modes:
            raise ValueError("unsupported plugin mode")
        reader = self.active_alerts
        if isinstance(reader, ActiveAlerts):
            reader = replace(reader, timezone=request.get("timezone", "Asia/Shanghai"))
        source = Source(
            id=request["source_id"],
            plugin_source_id=request.get("plugin_source_id", ""),
            name=request.get("source_name", request["source_id"]),
            bk_tenant_id=request["bk_tenant_id"],
            configs=request.get("configs", {}),
            timezone=request.get("timezone", "Asia/Shanghai"),
            meta_info=request.get("state", {}).get("meta_info"),
            activate_time=request.get("state", {}).get("activate_time"),
            received_at=request.get("received_at"),
            active_alerts=reader,
            metrics=self.metrics,
            celery_task_time_interval=request.get("interval_minutes", 0),
        )
        if mode in ("metric", "fields"):
            if mode == "fields":
                data = plugin.client.get_custom_msg_fields()
            else:
                payload = request["data"]
                start = datetime.fromisoformat(payload["start_time"].replace("Z", "+00:00"))
                end = datetime.fromisoformat(payload["end_time"].replace("Z", "+00:00"))
                if end <= start:
                    raise ValueError("metric query window must be positive")
                if plugin.client.get_metric is BaseClientV2.get_metric:
                    # 推送构造函数可能要求真实业务正文；默认空能力查询无需解析伪造输入。
                    data = {}
                else:
                    data = plugin.client(source).get_metric(payload["alarm"], start, end)
            return {"version": 1, "request_id": request["request_id"], "bk_tenant_id": source.bk_tenant_id, "source_id": source.id, "data": data}
        if mode == "push":
            data = request["data"]
            inputs = data if isinstance(data, list) else [data]
            business_inputs = [item for item in inputs if not isinstance(item, dict) or item.get("action") != "heartbeat"]
            # 与 KAC V2 接收入口一致：只有顶层心跳被剔除，业务批次保留原顺序与形态。
            if len(business_inputs) != len(inputs):
                if not business_inputs:
                    return {"version": 1, "request_id": request["request_id"], "bk_tenant_id": source.bk_tenant_id, "source_id": source.id, "events": []}
                data = business_inputs if isinstance(data, list) else business_inputs[0]
            client = plugin.client(source, data)
        else:
            client = plugin.client(source)
        events = client.execute(recovery=mode == "recovery")
        result = {
            "version": 1,
            "request_id": request["request_id"],
            "bk_tenant_id": source.bk_tenant_id,
            "source_id": source.id,
            "events": events,
        }
        state = client.proposed_state()
        if state is not None:
            result["proposed_state"] = state
        return result
