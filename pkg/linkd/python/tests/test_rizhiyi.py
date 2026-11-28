# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""日志易历史窗口和查询错误回归。"""

from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

from linkd_runtime.base import Source
from linkd_runtime.plugins.rizhiyi import AlarmClient


class RizhiyiTest(unittest.TestCase):
    def client(self, latest):
        reader = Mock()
        reader.latest.return_value = latest
        return AlarmClient(Source("logs", "日志易", "tenant", active_alerts=reader))

    def response(self):
        response = Mock(status_code=200)
        response.json.return_value = {"result": {"sheets": {"rows": [{"time": "2026-11-14:12:00:00.000", "alert_name": "cpu"}, {"time": "2026-11-14:12:02:00.000", "alert_name": "cpu"}]}}}
        return response

    def test_first_pull_has_no_history(self):
        client = self.client(None)
        with patch.dict("sys.modules", requests=SimpleNamespace(get=Mock(return_value=self.response()))):
            result = client.get_log_simple_normal_alarm()
        self.assertEqual(len(result), 2)
        client.alarm_source_obj.active_alerts.latest.assert_called_once_with("tenant", "logs")

    def test_last_alarm_has_one_minute_overlap(self):
        client = self.client({"alarm_time": "2026-11-14 12:02:00"})
        with patch.dict("sys.modules", requests=SimpleNamespace(get=Mock(return_value=self.response()))):
            result = client.get_log_simple_normal_alarm()
        self.assertEqual(len(result), 1)
        self.assertEqual(result[0]["alarm_time"], "2026-11-14 12:02:00")

    def test_history_failure_is_not_first_pull(self):
        client = self.client(None)
        client.alarm_source_obj.active_alerts.latest.side_effect = RuntimeError("reader down")
        with patch.dict("sys.modules", requests=SimpleNamespace(get=Mock(return_value=self.response()))), self.assertRaises(RuntimeError):
            client.get_log_simple_normal_alarm()

    def test_recovery_request_failure_is_not_empty(self):
        client = self.client(None)
        with patch.dict("sys.modules", requests=SimpleNamespace(get=Mock(side_effect=TimeoutError()))), self.assertRaises(TimeoutError):
            client.get_log_simple_recovery()
