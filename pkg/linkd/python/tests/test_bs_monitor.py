# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""BS_MONITOR 辅助查询协议，不连接真实指标平台。"""

import unittest
from unittest.mock import Mock

from linkd_runtime.executor import Executor


class BSMonitorTest(unittest.TestCase):
    def request(self, alarm=None, mode="metric"):
        return {"version": 1, "request_id": "r", "bk_tenant_id": "tenant", "source_id": "built_in_bk", "plugin": "bs_monitor", "mode": mode, "configs": {"username": "operator"}, "data": {"alarm": alarm or {"source_id": "built_in_bk", "metric_name": "cpu"}, "start_time": "2026-11-14T12:00:00Z", "end_time": "2026-11-14T13:00:00Z"}}

    def test_fields_do_not_query(self):
        result = Executor().execute(self.request(mode="fields"))
        self.assertEqual(result["data"], ["close_reason"])
        self.assertNotIn("events", result)

    def test_historical_result_has_priority(self):
        reader = Mock()
        reader.query_builtin.return_value = [{"historical": True}]
        result = Executor(metrics=reader).execute(self.request())
        self.assertEqual(result["data"], [{"historical": True}])
        reader.query_points.assert_not_called()

    def test_history_failure_does_not_fallback(self):
        reader = Mock()
        reader.query_builtin.side_effect = RuntimeError("history unavailable")
        with self.assertRaises(RuntimeError):
            Executor(metrics=reader).execute(self.request())
        reader.query_points.assert_not_called()

    def test_legacy_query_and_tenant(self):
        reader = Mock()
        reader.query_builtin.return_value = None
        reader.query_points.return_value = {"series": []}
        result = Executor(metrics=reader).execute(self.request())
        self.assertEqual(result["data"], [{"series": []}])
        tenant, query = reader.query_points.call_args.args
        self.assertEqual(tenant, "tenant")
        self.assertEqual(query["metric_name"], "cpu")
        self.assertEqual(query["bk_username"], "operator")

    def test_recovery_query_restores_exact_event(self):
        reader = Mock()
        reader.recovery_fields.return_value = {"source_id": "built_in_bk", "event_id": "stored-event"}
        reader.query_builtin.return_value = []
        alarm = {"source_id": "built_in_bk", "alarm_id": "a", "event_id": "requested-event", "recovery_record_id": "recovery"}
        Executor(metrics=reader).execute(self.request(alarm))
        self.assertEqual(reader.query_builtin.call_args.args[1]["event_id"], "requested-event")
        self.assertEqual(reader.recovery_fields.return_value["event_id"], "stored-event")

    def test_missing_host_capability_fails(self):
        with self.assertRaises(RuntimeError):
            Executor().execute(self.request())
