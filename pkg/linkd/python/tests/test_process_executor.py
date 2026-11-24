# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""真实 spawn 进程的复用、崩溃、超时和退出回归。"""

import os
import time
import unittest
import threading
from concurrent.futures import ThreadPoolExecutor

from linkd_runtime.process_executor import ProcessExecutor, ExecutorBusy, _encode_result


class TestExecutor:
    def __init__(self):
        self.barrier = threading.Barrier(2)

    def execute(self, request):
        if request.get("mode") == "parallel":
            self.barrier.wait(timeout=2)
            return {"pid": os.getpid(), "thread": threading.get_ident()}
        if request.get("mode") == "large":
            return {"value": "x" * 1024}
        if request.get("mode") == "unserializable":
            return {"value": object()}
        if request.get("mode") == "hang":
            time.sleep(10)
        if request.get("mode") == "crash":
            os._exit(1)
        return {"pid": os.getpid()}


def hanging_factory():
    time.sleep(10)
    return TestExecutor()


class ProcessTest(unittest.TestCase):
    def test_precanceled_call_does_not_start_or_consume_slot(self):
        executor = ProcessExecutor(timeout=2, factory=TestExecutor)
        canceled = threading.Event()
        canceled.set()
        try:
            with self.assertRaises(InterruptedError):
                executor.execute({}, cancel_event=canceled)
            self.assertIsNone(executor.process)
            canceled.clear()
            self.assertIn("pid", executor.execute({}, cancel_event=canceled))
        finally:
            executor.close()

    def test_startup_timeout_is_reaped(self):
        executor = ProcessExecutor(timeout=0.3, factory=hanging_factory)
        try:
            with self.assertRaises(TimeoutError):
                executor.start()
            self.assertIsNone(executor.process)
        finally:
            executor.close()
    def test_timeout_fails_siblings_and_recovers(self):
        executor = ProcessExecutor(timeout=0.5, factory=TestExecutor, threads=2)
        executor.execute({})
        try:
            with ThreadPoolExecutor(max_workers=2) as callers:
                futures = [callers.submit(executor.execute, {"mode": "hang"}) for _ in range(2)]
                for future in futures:
                    with self.assertRaises((TimeoutError, RuntimeError)):
                        future.result(timeout=3)
            self.assertIn("pid", executor.execute({}))
        finally:
            executor.close()
    def test_two_threads_execute_in_same_process(self):
        executor = ProcessExecutor(timeout=3, factory=TestExecutor, threads=2)
        executor.start()
        try:
            with ThreadPoolExecutor(max_workers=2) as callers:
                first = callers.submit(executor.execute, {"mode": "parallel"})
                second = callers.submit(executor.execute, {"mode": "parallel"})
                a, b = first.result(), second.result()
            self.assertEqual(a["pid"], b["pid"])
            self.assertNotEqual(a["thread"], b["thread"])
        finally:
            executor.close()
    def test_output_budget_and_bad_serialization_do_not_stall(self):
        executor = ProcessExecutor(timeout=2, factory=TestExecutor, max_result_bytes=128)
        try:
            result = executor.execute({"mode": "large"})
            self.assertEqual(result["error"]["type"], "OverflowError")
            result = executor.execute({"mode": "unserializable"})
            self.assertEqual(result["error"]["type"], "TypeError")
            self.assertIn("pid", executor.execute({}))
        finally:
            executor.close()

    def test_budget_counts_utf8_bytes(self):
        expected = '"告警"'.encode()
        self.assertEqual(_encode_result("告警", len(expected)), expected)
        with self.assertRaises(OverflowError):
            _encode_result("告警", len(expected) - 1)

    def test_reuse_and_timeout_replacement(self):
        executor = ProcessExecutor(timeout=0.5, factory=TestExecutor)
        try:
            first = executor.execute({})["pid"]
            self.assertEqual(first, executor.execute({})["pid"])
            with self.assertRaises(TimeoutError):
                executor.execute({"mode": "hang"})
            self.assertIsNone(executor.process)
            self.assertNotEqual(first, executor.execute({})["pid"])
        finally:
            executor.close()
        with self.assertRaises(RuntimeError):
            executor.execute({})

    def test_crash_releases_slot(self):
        executor = ProcessExecutor(timeout=2, factory=TestExecutor)
        try:
            with self.assertRaises(RuntimeError):
                executor.execute({"mode": "crash"})
            self.assertIn("pid", executor.execute({}))
        finally:
            executor.close()

    def test_busy_is_immediate(self):
        executor = ProcessExecutor(factory=TestExecutor)
        executor.lock.acquire()
        executor.slots.acquire()
        try:
            with self.assertRaises(ExecutorBusy):
                executor.execute({})
            with self.assertRaises(ExecutorBusy):
                executor.close()
        finally:
            executor.lock.release()
            executor.slots.release()
            executor.close()
