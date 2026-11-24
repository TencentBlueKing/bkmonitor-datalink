# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""通过 KAC 只读宿主服务查询指标，运行时不导入 Django 或旧告警源插件。"""

from dataclasses import dataclass
import json
from pathlib import Path
from urllib.request import Request

from .query_transport import open_query


@dataclass(frozen=True)
class MetricQueries:
    """部署显式注入完整接口地址和已签发 JWT 文件，业务输入不能选择服务地址。"""

    endpoint: str
    token_file: str
    timeout: float = 10
    max_bytes: int = 8 << 20

    def recovery_fields(self, tenant, alarm_id, record_id):
        return self._call(tenant, "recovery_fields", {"alarm_id": alarm_id, "record_id": record_id})

    def query_builtin(self, tenant, alarm, start, end):
        return self._call(tenant, "query_builtin", {"alarm": alarm, "start_time": start.isoformat(), "end_time": end.isoformat()})

    def query_points(self, tenant, query):
        return self._call(tenant, "query_points", {"query": query})

    def _call(self, tenant, operation, params):
        token = Path(self.token_file).read_text().strip()
        if not token:
            raise ValueError("metric query token is required")
        header = token if token.startswith("Bearer ") else "Bearer " + token
        body = json.dumps({"operation": operation, "bk_tenant_id": tenant, "params": params}).encode()
        request = Request(self.endpoint, data=body, headers={"Internal-Token": header, "Content-Type": "application/json"})
        with open_query(request, timeout=self.timeout) as response:
            raw = response.read(self.max_bytes + 1)
        if len(raw) > self.max_bytes:
            raise ValueError("metric query response exceeds byte budget")
        # 只有显式 data=null 才表示无历史引用；失败/缺字段均传播给执行边界。
        return json.loads(raw)["data"]
