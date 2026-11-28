# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""调试 CLI 的真实子进程预算、输出和退出行为。"""

import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest


class DebugLimitsTest(unittest.TestCase):
    def command(self, root, body, *options):
        plugin = root / "plugin.py"
        plugin.write_text('''import os
import time
from pathlib import Path
from linkd_runtime.base import BaseClientV2
MODES = ("pull",)
class AlarmClient(BaseClientV2):
    def pull_raw_events(self):
''' + "\n".join("        " + line for line in body.splitlines()) + "\n")
        request = root / "request.json"
        request.write_text(json.dumps({"version": 1, "request_id": "debug", "bk_tenant_id": "tenant", "source_id": "source", "plugin": "custom", "mode": "pull", "configs": {"marker": str(root / "started")}}))
        return [sys.executable, "-S", "-m", "linkd_runtime", "debug", "--request", str(request), "--plugin-file", str(plugin), *options]

    def test_timeout_kills_execution(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            command = self.command(root, 'Path(self.alarm_source_config_dict["marker"]).write_text(str(os.getpid()))\ntime.sleep(60)\nreturn []', "--execution-timeout", "1")
            result = subprocess.run(command, capture_output=True, text=True, timeout=5)
            self.assertTrue((root / "started").exists(), "plugin never entered execution")
            self.assertEqual(result.returncode, 1)
            self.assertEqual(result.stdout, "")
            self.assertEqual(json.loads(result.stderr)["error"]["type"], "TimeoutError")

    def test_input_and_result_budgets(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            body = 'Path(self.alarm_source_config_dict["marker"]).write_text("started")\nreturn []'
            result = subprocess.run(self.command(root, body, "--max-body-bytes", "16"), capture_output=True, text=True, timeout=5)
            self.assertEqual(result.returncode, 1)
            self.assertEqual(json.loads(result.stderr)["error"]["type"], "OverflowError")
            self.assertFalse((root / "started").exists())
            result = subprocess.run(self.command(root, body, "--max-result-bytes", "16"), capture_output=True, text=True, timeout=5)
            self.assertEqual(result.returncode, 1)
            self.assertTrue((root / "started").exists())
            self.assertEqual(result.stdout, "")
            self.assertEqual(json.loads(result.stderr)["error"]["type"], "OverflowError")

    def test_plugin_print_does_not_corrupt_stdout(self):
        with tempfile.TemporaryDirectory() as directory:
            command = self.command(Path(directory), 'print("plugin-note")\nreturn []')
            result = subprocess.run(command, capture_output=True, text=True, timeout=5)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads(result.stdout)["events"], [])
            self.assertIn("plugin-note", result.stderr)

    @unittest.skipUnless(os.name == "posix", "requires POSIX process signals")
    def test_sigterm_reaps_debug_child(self):
        for during_import in (False, True):
            with self.subTest(during_import=during_import):
                self.check_sigterm(during_import)

    def check_sigterm(self, during_import):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            command = self.command(root, 'Path(self.alarm_source_config_dict["marker"]).write_text(str(os.getpid()))\ntime.sleep(60)\nreturn []')
            if during_import:
                (root / "plugin.py").write_text("import os, time\nfrom pathlib import Path\nPath(" + repr(str(root / "started")) + ").write_text(str(os.getpid()))\ntime.sleep(60)\n")
            with subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) as process:
                try:
                    deadline = time.monotonic() + 5
                    while not (root / "started").exists() and process.poll() is None and time.monotonic() < deadline:
                        time.sleep(0.01)
                    self.assertTrue((root / "started").exists())
                    child = int((root / "started").read_text())
                    process.send_signal(signal.SIGTERM)
                    stdout, stderr = process.communicate(timeout=5)
                    self.assertEqual(process.returncode, 128 + signal.SIGTERM, stderr)
                    self.assertEqual(stdout, "")
                    with self.assertRaises(ProcessLookupError):
                        os.kill(child, 0)
                finally:
                    if process.poll() is None:
                        process.kill()
                        process.communicate(timeout=5)
