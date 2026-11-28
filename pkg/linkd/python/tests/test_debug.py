# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""实际子进程运行开发者示例与失败入口。"""

import json
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest
from urllib.parse import parse_qs, urlparse


class DebugTest(unittest.TestCase):
    def test_example(self):
        root = Path(__file__).resolve().parents[1]
        result = subprocess.run([sys.executable, "-S", "-m", "linkd_runtime", "debug", "--request", str(root / "examples/push_request.json"), "--plugin-file", str(root / "examples/push_plugin.py")], capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        body = json.loads(result.stdout)
        self.assertEqual(body["events"][0]["fingerprint"], "alert-1")
        self.assertEqual(body["events"][0]["bk_tenant_id"], "demo")

    def test_missing_file(self):
        result = subprocess.run([sys.executable, "-S", "-m", "linkd_runtime", "debug", "--request", "/nonexistent/linkd-request.json"], capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")
        self.assertEqual(json.loads(result.stderr)["error"]["type"], "FileNotFoundError")

    def test_recovery_debug_uses_configured_query_service(self):
        observed = []
        page = {"items": [{"bk_tenant_id": "tenant", "event_source_id": "source", "status": "active", "fingerprint": "original-id", "alert_id": "internal-id", "severity": "warning", "begin_at": "2026-11-14T12:00:00Z", "title": "alarm"}], "next": ""}

        class Handler(BaseHTTPRequestHandler):
            status = 200

            def do_GET(self):
                observed.append((self.path, self.headers.get("Internal-Token")))
                body = json.dumps(page).encode()
                self.send_response(self.status)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *_args):
                pass

        server = HTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01})
        thread.start()
        try:
            with tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root / "token").write_text("query-secret\n")
                (root / "plugin.py").write_text('''from linkd_runtime.base import BaseClientV2
MODES = ("recovery",)
class AlarmClient(BaseClientV2):
    def pull_recovery_events(self):
        self.match_alarm_resolved(self.query_alarm_source_not_close_alarm(self.alarm_source_obj))
        return self._terminal_events
''')
                request = {"version": 1, "request_id": "debug-recovery", "bk_tenant_id": "tenant", "source_id": "source", "plugin": "test-recovery", "mode": "recovery", "received_at": "2026-11-14T13:00:00Z"}
                (root / "request.json").write_text(json.dumps(request))
                command = [sys.executable, "-S", "-m", "linkd_runtime", "debug", "--request", str(root / "request.json"), "--plugin-file", str(root / "plugin.py"), "--linkd-url", "http://127.0.0.1:%d" % server.server_port, "--linkd-token-file", str(root / "token")]
                result = subprocess.run(command, capture_output=True, text=True, timeout=10)
                self.assertEqual(result.returncode, 0, result.stderr)
                event = json.loads(result.stdout)["events"][0]
                self.assertEqual(event["fingerprint"], "original-id")
                self.assertEqual(event["evaluations"][0]["action"], "resolved")
                self.assertEqual(event["occurred_at"], request["received_at"])
                path, authorization = observed[0]
                self.assertEqual(authorization, "Bearer query-secret")
                query = parse_qs(urlparse(path).query)
                self.assertEqual(query["bk_tenant_id"], ["tenant"])
                self.assertEqual(query["event_source_id"], ["source"])
                self.assertEqual(query["status"], ["active"])
                for status in (401, 503):
                    Handler.status = status
                    result = subprocess.run(command, capture_output=True, text=True, timeout=10)
                    self.assertEqual(result.returncode, 1)
                    self.assertEqual(result.stdout, "")
                    self.assertEqual(json.loads(result.stderr)["error"]["type"], "HTTPError")
                    self.assertNotIn("query-secret", result.stderr)
                # 缺少查询配置必须失败，不能调试出一个假成功的空恢复批次。
                result = subprocess.run(command[:-4], capture_output=True, text=True, timeout=10)
                self.assertEqual(result.returncode, 1)
                self.assertEqual(json.loads(result.stderr)["error"]["type"], "RuntimeError")
                self.assertEqual(len(observed), 3)
        finally:
            server.shutdown()
            thread.join(timeout=2)
            server.server_close()

    def test_debug_uses_deployment_manifest_and_file_override(self):
        examples = Path(__file__).resolve().parents[1] / "examples"
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "plugins").mkdir()
            (root / "plugins" / "demo.py").write_text((examples / "push_plugin.py").read_text())
            manifest = root / "manifest.json"
            manifest.write_text(json.dumps({"demo": "plugins/demo.py"}))
            command = [sys.executable, "-S", "-m", "linkd_runtime", "debug", "--request", str(examples / "push_request.json"), "--plugin-manifest", str(manifest)]
            result = subprocess.run(command, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            original = json.loads(result.stdout)
            self.assertEqual(original["events"][0]["fingerprint"], "alert-1")
            manifest.write_text(json.dumps({"demo": "missing.py"}))
            result = subprocess.run(command, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 1)
            self.assertEqual(result.stdout, "")
            result = subprocess.run(command + ["--plugin-file", str(examples / "push_plugin.py")], capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads(result.stdout), original)
