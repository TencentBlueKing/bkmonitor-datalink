# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""vCenter 事件转换和查询失败时资源释放。"""

from datetime import datetime, timezone
from types import SimpleNamespace
import unittest
import ssl
from unittest.mock import Mock, patch

from linkd_runtime.base import Source, source_time
from linkd_runtime.plugins.vcenter import AlarmClient, VcenterAlarmsApi


class VcenterTest(unittest.TestCase):
    def test_recovery_keeps_trigger_identity(self):
        client = AlarmClient(Source("vc", "VC", "tenant"))
        alarm = {"entity_name": "host", "alarm": "disk", "createdTime": "2026-11-14T12:00:00Z", "fullFormattedMessage": "disk to Red"}
        trigger = client._standard_event(alarm)
        recovery = client._standard_event({**alarm, "fullFormattedMessage": "disk to Green"})
        self.assertEqual(trigger["fingerprint"], recovery["fingerprint"])
        self.assertEqual(recovery["evaluations"], [{"action": "resolved", "severity": "warning"}])

    def test_connection_negotiates_tls_and_bounds_network_wait(self):
        source = Source("vc", "VC", "tenant", configs={"ip": "vc.example", "user": "user", "password": "password"})
        connect = Mock()
        with patch.dict("sys.modules", {"pyVim": SimpleNamespace(), "pyVim.connect": SimpleNamespace(SmartConnect=connect)}):
            self.assertIs(AlarmClient(source).connect_vc(), connect.return_value)
        connect.assert_called_once()
        arguments = connect.call_args.kwargs
        context = arguments["sslContext"]
        self.assertEqual(context.protocol, ssl.PROTOCOL_TLS_CLIENT)
        self.assertEqual(context.verify_mode, ssl.CERT_NONE)
        self.assertFalse(context.check_hostname)
        self.assertEqual(arguments["httpConnectionTimeout"], 30)
        self.assertEqual(arguments["connectionPoolTimeout"], 60)

    def test_connection_failure_preserves_cause_without_credentials(self):
        cause = OSError("private-password")
        connect = Mock(side_effect=cause)
        source = Source("vc", "VC", "tenant", configs={"ip": "vc.example", "user": "user", "password": "password"})
        with patch.dict("sys.modules", {"pyVim": SimpleNamespace(), "pyVim.connect": SimpleNamespace(SmartConnect=connect)}):
            with self.assertRaises(RuntimeError) as failure:
                AlarmClient(source).connect_vc()
        self.assertIs(failure.exception.__cause__, cause)
        self.assertNotIn("private-password", str(failure.exception))
        connect.assert_called_once()

    def test_collector_destroyed_after_read_failure(self):
        collector = Mock()
        collector.ReadNextEvents.side_effect = RuntimeError("query failed")
        content = Mock()
        content.eventManager.CreateCollectorForEvents.return_value = collector
        vim = SimpleNamespace(event=SimpleNamespace(EventFilterSpec=SimpleNamespace), EventFilterSpecByTime=SimpleNamespace)
        with patch.dict("sys.modules", pyVmomi=SimpleNamespace(vim=vim)), self.assertRaises(RuntimeError):
            VcenterAlarmsApi(source_time).get_alarms_all(content, 5, ["alarm"], datetime.now(timezone.utc))
        collector.DestroyCollector.assert_called_once()

    def test_disconnect_after_query_failure(self):
        connection = Mock()
        connection.content.alarmManager.GetAlarm.side_effect = RuntimeError("query failed")
        disconnect = Mock()
        client = AlarmClient(Source("vc", "VC", "tenant"))
        with patch.dict("sys.modules", {"pyVim": SimpleNamespace(), "pyVim.connect": SimpleNamespace(Disconnect=disconnect)}), patch.object(client, "connect_vc", return_value=connection), self.assertRaises(RuntimeError):
            client.get_vcenter_alarms()
        disconnect.assert_called_once_with(connection)
