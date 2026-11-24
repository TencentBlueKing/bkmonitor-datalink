# 独立 Python 执行器；构建上下文为 python/，不复制 KAC 或 Django。
ARG PYTHON_IMAGE=python:3.11-slim-bookworm
ARG BUILD_MODE=online
FROM ${PYTHON_IMAGE} AS online
WORKDIR /opt/runtime
RUN pip install --no-cache-dir uv==0.7.3
COPY pyproject.toml uv.lock README.md ./
COPY linkd_runtime/ ./linkd_runtime/
# 存量插件全集包含 SQL Server 和 vCenter 驱动，依赖版本取自提交的锁文件。
RUN uv sync --frozen --no-dev --all-extras --no-editable --python /usr/local/bin/python

# 离线模式使用宿主从 uv.lock 导出的依赖和已构建 wheel，不在容器内访问包仓库。
FROM ${PYTHON_IMAGE} AS offline
WORKDIR /opt/runtime
COPY wheelhouse/ /wheelhouse/
RUN python -m venv /opt/runtime/.venv \
    && /opt/runtime/.venv/bin/pip install --no-index --find-links=/wheelhouse -r /wheelhouse/requirements.txt \
    && /opt/runtime/.venv/bin/pip install --no-index --no-deps /wheelhouse/linkd_plugin_runtime-*.whl

FROM ${BUILD_MODE} AS builder

FROM ${PYTHON_IMAGE}
ENV PATH="/opt/runtime/.venv/bin:$PATH" \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1
WORKDIR /opt/runtime
COPY --from=builder /opt/runtime/.venv /opt/runtime/.venv
USER 65532:65532
EXPOSE 8091
# exec 入口让主进程直接收到 SIGTERM；服务自身等待正在执行的请求退出。
ENTRYPOINT ["linkd-plugin-runtime"]
CMD ["serve", "--host", "0.0.0.0", "--token-file", "/run/secrets/runtime-token"]
