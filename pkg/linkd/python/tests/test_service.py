# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""实际 CLI、HTTP、常驻执行进程及 SIGTERM 退出链路。"""

import http.client
import json
from pathlib import Path
import select
import subprocess
import sys
import tempfile
import unittest


class ServiceTest(unittest.TestCase):
    def test_cli_executes_and_stops(self):
        with tempfile.TemporaryDirectory() as folder:
            token = Path(folder) / "token"
            token.write_text("local-test")
            process = subprocess.Popen([sys.executable, "-S", "-m", "linkd_runtime", "serve", "--port", "0", "--processes", "2", "--execution-timeout", "2", "--token-file", str(token)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                ready, _, _ = select.select([process.stdout], [], [], 5)
                self.assertTrue(ready, "service did not become ready")
                address = json.loads(process.stdout.readline())
                connection = http.client.HTTPConnection(address["host"], address["port"], timeout=5)
                try:
                    request = {"version": 1, "request_id": "r", "bk_tenant_id": "t", "source_id": "s", "plugin": "rest_api_push", "mode": "push", "data": {"action": "heartbeat"}}
                    connection.request("POST", "/execute", json.dumps(request), {"Authorization": "Bearer local-test"})
                    response = connection.getresponse()
                    self.assertEqual(response.status, 200)
                    self.assertEqual(json.loads(response.read())["events"], [])
                finally:
                    connection.close()
                process.terminate()
                stdout, stderr = process.communicate(timeout=5)
                self.assertEqual(process.returncode, 0, stderr)
                self.assertNotIn("resource_tracker", stderr)
            finally:
                if process.poll() is None:
                    process.kill()
                process.communicate(timeout=5)
