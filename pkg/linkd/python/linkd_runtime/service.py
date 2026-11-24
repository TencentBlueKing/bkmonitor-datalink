# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""同步执行服务装配，不参与 Linkd 来源调度。"""

import json
import queue
import signal
import threading

from .http_server import RuntimeServer
from .process_executor import ProcessExecutor, ExecutorBusy
from .executor import ExecutorFactory


class ProcessPool:
    """固定进程数量，只借用空闲槽位，不排队业务请求。"""

    def __init__(self, size, timeout, max_result_bytes=8 * 1048576, threads=1, active_alerts=None, plugin_files=None, metrics=None):
        if size < 1:
            raise ValueError("positive process count required")
        self.executors = []
        self.available = queue.Queue(maxsize=size * threads)
        try:
            for _ in range(size):
                executor = ProcessExecutor(timeout=timeout, max_result_bytes=max_result_bytes, threads=threads, factory=ExecutorFactory(active_alerts, plugin_files, metrics))
                self.executors.append(executor)
                executor.start()
                for _ in range(threads):
                    self.available.put_nowait(executor)
        except BaseException:
            self.close()
            raise

    def execute(self, request):
        try:
            executor = self.available.get_nowait()
        except queue.Empty as error:
            raise ExecutorBusy("all executors are busy") from error
        try:
            return executor.execute(request)
        finally:
            self.available.put_nowait(executor)

    def close(self):
        """调用方必须先停止 HTTP 接入并等待请求线程退出。"""
        for executor in self.executors:
            executor.close()


def serve(host, port, token, *, processes=4, execution_timeout=30, read_timeout=10, max_body_bytes=1048576, max_result_bytes=8 * 1048576, threads=1, active_alerts=None, plugin_files=None, metrics=None):
    """主线程处理退出信号，服务线程负责 accept，避免信号处理器内 shutdown 死锁。"""
    pool = ProcessPool(processes, execution_timeout, max_result_bytes, threads, active_alerts, plugin_files, metrics)
    server = None
    thread = None
    stop = threading.Event()
    previous = {}
    try:
        server = RuntimeServer((host, port), pool, token, concurrency=processes * threads, max_body_bytes=max_body_bytes, read_timeout=read_timeout)
        # server_close 等待非 daemon 请求线程；执行时间由独立进程超时约束。
        server.daemon_threads = False
        for signum in (signal.SIGTERM, signal.SIGINT):
            previous[signum] = signal.signal(signum, lambda *_args: stop.set())
        thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.1})
        thread.start()
        print(json.dumps({"host": server.server_address[0], "port": server.server_address[1]}), flush=True)
        stop.wait()
    finally:
        if thread is not None:
            server.shutdown()
            thread.join()
        if server is not None:
            server.server_close()
        pool.close()
        for signum, handler in previous.items():
            signal.signal(signum, handler)
