# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""IMC 普通拉取同时处理触发与恢复，查询边界不依赖 Django。"""

import unittest
from unittest.mock import Mock, patch

from linkd_runtime.executor import Executor
from linkd_runtime.plugins.imc import AlarmClient


class IMCTest(unittest.TestCase):
    def setUp(self):
        self.request = {"version": 1, "request_id": "r", "bk_tenant_id": "tenant", "source_id": "imc", "plugin": "imc", "mode": "pull", "received_at": "2026-11-14T12:00:00Z", "configs": {"host": "http://imc.invalid", "user": "user", "password": "password"}}
        self.active = {"bk_tenant_id": "tenant", "source_id": "imc", "event_id": "old", "fingerprint": "old", "level": "warning"}
        self.new = {"id": "new", "faultTimeDesc": "2026-11-14T10:00:00Z", "alarmCategoryDesc": "设备异常"}

    def test_trigger_filter_and_recovery(self):
        reader = Mock(return_value=[self.active])
        pages = [{"alarm": [{"id": "old"}, self.new]}, {"alarm": [{"id": "old"}]}]
        with patch.object(AlarmClient, "_imc_api_get", side_effect=pages):
            result = Executor(active_alerts=reader).execute(self.request)
        self.assertEqual([e["fingerprint"] for e in result["events"]], ["new", "old"])
        self.assertEqual(result["events"][1]["occurred_at"], self.request["received_at"])
        self.assertEqual(result["events"][1]["evaluations"][0]["action"], "resolved")
        for call in reader.call_args_list:
            self.assertEqual(call.args[:2], ("tenant", "imc"))

    def test_missing_reader_is_failure(self):
        with patch.object(AlarmClient, "_imc_api_get", return_value={"alarm": [self.new]}), self.assertRaises(RuntimeError):
            Executor().execute(self.request)

    def test_recovery_scope_mismatch_is_failure(self):
        reader = Mock(return_value=[{**self.active, "bk_tenant_id": "other"}])
        with patch.object(AlarmClient, "_imc_api_get", side_effect=[{"alarm": []}, {"alarm": [{"id": "old"}]}]), self.assertRaises(ValueError):
            Executor(active_alerts=reader).execute(self.request)

    def test_recovery_failure_does_not_report_partial_success(self):
        reader = Mock(return_value=[self.active])
        with patch.object(AlarmClient, "_imc_api_get", side_effect=[{"alarm": [self.new]}, TimeoutError()]), self.assertRaises(TimeoutError):
            Executor(active_alerts=reader).execute(self.request)
