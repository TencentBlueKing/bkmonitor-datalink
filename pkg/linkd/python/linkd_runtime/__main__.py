# Tencent is pleased to support the open source community by making
# 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
# Copyright (C) 2026 Tencent. All rights reserved.
# Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
# You may obtain a copy of the License at http://opensource.org/licenses/MIT
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
# an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

"""本地插件计算调试入口，使用与同步 HTTP 服务相同的 Executor。"""

import argparse
import json
from pathlib import Path
import sys
import signal
import threading

from .executor import ExecutorFactory
from .loader import read_manifest


def main(argv=None):
    parser = argparse.ArgumentParser(description="Linkd 插件本地调试（不发布事件或提交进度）")
    parser.add_argument("command", choices=["debug", "serve"])
    parser.add_argument("--request", help="JSON 执行请求文件")
    parser.add_argument("--plugin-file", help="自定义插件 Python 文件，导出 AlarmClient 和 MODES")
    parser.add_argument("--token-file", help="内部认证 token 文件，不通过命令行传递凭据")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8091)
    parser.add_argument("--processes", type=int, default=4)
    parser.add_argument("--threads", type=int, default=1)
    parser.add_argument("--execution-timeout", type=float, default=30)
    parser.add_argument("--read-timeout", type=float, default=10)
    parser.add_argument("--max-body-bytes", type=int, default=1048576)
    parser.add_argument("--max-result-bytes", type=int, default=8 * 1048576)
    parser.add_argument("--linkd-url", help="活动告警查询服务地址")
    parser.add_argument("--linkd-token-file", help="Linkd 已签发 Internal-Token JWT 文件，每次查询重新读取")
    parser.add_argument("--plugin-manifest", help="可信部署插件清单 JSON 文件")
    parser.add_argument("--metric-url", help="KAC 指标宿主查询完整接口地址")
    parser.add_argument("--metric-token-file", help="指标宿主已签发 Internal-Token JWT 文件")
    args = parser.parse_args(argv)
    try:
        plugin_files = read_manifest(args.plugin_manifest) if args.plugin_manifest else {}
        metrics = None
        if args.metric_url:
            if not args.metric_token_file:
                parser.error("--metric-url requires --metric-token-file")
            from .metric_queries import MetricQueries
            metrics = MetricQueries(args.metric_url, args.metric_token_file, max_bytes=args.max_result_bytes)
        reader = None
        if args.linkd_url:
            if not args.linkd_token_file:
                parser.error("--linkd-url requires --linkd-token-file")
            from .source_alerts import ActiveAlerts
            reader = ActiveAlerts(args.linkd_url, token_file=args.linkd_token_file)
        if args.command == "serve":
            from .service import serve
            if not args.token_file:
                parser.error("serve requires --token-file")
            token = Path(args.token_file).read_text().strip()
            serve(args.host, args.port, token, processes=args.processes, execution_timeout=args.execution_timeout, read_timeout=args.read_timeout, max_body_bytes=args.max_body_bytes, max_result_bytes=args.max_result_bytes, threads=args.threads, active_alerts=reader, plugin_files=plugin_files, metrics=metrics)
            return 0
        if not args.request:
            parser.error("debug requires --request")
        if args.max_body_bytes <= 0:
            raise ValueError("positive input byte budget required")
        with Path(args.request).open("rb") as stream:
            raw = stream.read(args.max_body_bytes + 1)
        if len(raw) > args.max_body_bytes:
            raise OverflowError("debug request exceeds byte budget")
        request = json.loads(raw)
        if args.plugin_file:
            plugin_files[request["plugin"]] = args.plugin_file
        from .process_executor import ProcessExecutor
        executor = ProcessExecutor(timeout=args.execution_timeout, max_result_bytes=args.max_result_bytes,
                                   factory=ExecutorFactory(active_alerts=reader, plugin_files=plugin_files, metrics=metrics),
                                   redirect_output=True)
        canceled = threading.Event()
        received_signal = None
        def stop_debug(signum, _frame):
            # 信号处理器只发取消通知，不在执行器持锁时异步抛异常。
            nonlocal received_signal
            received_signal = signum
            canceled.set()
        previous = {signum: signal.signal(signum, stop_debug) for signum in (signal.SIGTERM, signal.SIGINT)}
        try:
            result = executor.execute(request, cancel_event=canceled)
        except InterruptedError:
            if received_signal is not None:
                return 128 + received_signal
            raise
        finally:
            try:
                executor.close()
            finally:
                for signum, handler in previous.items():
                    signal.signal(signum, handler)
        if received_signal is not None:
            return 128 + received_signal
        if result.get("error"):
            print(json.dumps({"error": {"code": "debug_failed", "type": result["error"]["type"]}}), file=sys.stderr)
            return 1
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 0
    except Exception as error:
        # 不把插件可能包含凭据的异常原文写入标准结果。
        print(json.dumps({"error": {"code": "debug_failed", "type": type(error).__name__}}), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
