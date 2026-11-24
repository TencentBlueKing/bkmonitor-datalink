# 独立执行器镜像

镜像仅安装 `linkd-plugin-runtime` 及锁定的 Python 依赖，包含所有内置插件使用的 requests、pymssql、pyvmomi，不包含 Django、KAC、数据库或 Kafka 服务。Go 采集器仍是唯一事件发布方。

## 构建

在 Linkd 项目根目录执行：

```bash
docker build -f python/Dockerfile -t linkd-plugin-runtime:local python
```

构建使用 `uv.lock` 和 `--frozen --all-extras`，不会重新解析依赖版本。默认基础镜像为 `python:3.11-slim-bookworm`，可以通过 `--build-arg PYTHON_IMAGE=...` 使用部署环境批准的同系列镜像。构建环境需要访问镜像仓库和 PyPI。镜像以 UID/GID 65532 运行；挂载的脚本和凭据文件必须允许该 UID 读取。

## 构建环境不能出网时

宿主能访问包仓库、构建容器不能出网时，可以准备目标平台的离线 wheelhouse。使用空目录，不混用不同运行时版本或架构。以下示例目标为 Linux ARM64 / Python 3.11：

```bash
mkdir -p python/wheelhouse
uv export --project python --frozen --all-extras --no-dev --no-emit-project \
  --format requirements-txt --output-file python/wheelhouse/requirements.txt
uv build python --wheel --out-dir python/wheelhouse
python3 -m pip download --platform manylinux_2_28_aarch64 \
  --python-version 3.11 --implementation cp --abi cp311 --only-binary=:all: \
  --dest python/wheelhouse -r python/wheelhouse/requirements.txt
docker build --platform linux/arm64 --build-arg BUILD_MODE=offline \
  -f python/Dockerfile -t linkd-plugin-runtime:local python
```

离线模式在构建容器内只安装 wheelhouse 中的锁定依赖及运行时 wheel；最终镜像与在线模式使用同一虚拟环境路径、用户、入口和默认命令。wheelhouse 不提交 Git，也不复制到最终镜像。构建仍需准备基础 Python 镜像。AMD64 对应 manylinux_2_28_x86_64 和 linux/amd64，需要单独准备并验证。

## 启动与并发

将内部认证 token 保存在宿主的 `runtime-token` 文件中，再启动：

```bash
docker run --rm --name linkd-plugin-runtime \
  --cpus 2 --memory 1g \
  -p 127.0.0.1:8091:8091 \
  --mount type=bind,src="$PWD/runtime-token",dst=/run/secrets/runtime-token,readonly \
  linkd-plugin-runtime:local \
  serve --host 0.0.0.0 --port 8091 \
  --token-file /run/secrets/runtime-token \
  --processes 2 --threads 4 --execution-timeout 30 --read-timeout 10
```

这里的资源和并发仅是启动示例，不是容量承诺。2 个常驻 Python 子进程、每进程 4 个线程，最多同时处理 8 个请求。满载立即拒绝，不增加业务排队。I/O 插件可增加线程；CPU 密集插件优先增加进程，并根据实际 CPU、内存和上游接口限额调整。单个线程执行超时会终止所属进程，同进程其他调用也会失败。

Linkd 的 `collector.endpoint` 指向 `/execute`，`collector.token` 与挂载 token 内容相同。容器间访问使用内部服务地址，不使用宿主环回地址。多副本置于内部服务负载均衡后即可扩容 Python 计算；拉取任务所有权仍由 Go Collector 调度管理，不能在每个 Python 副本启动定时任务。

需要恢复查询时添加 `--linkd-url` 和 `--linkd-token-file`，分别配置 Linkd 查询地址和已签发的 Internal-Token JWT 文件（不是签名密钥或 Worker Token）。需要自定义插件时只读挂载插件目录，并传入 `--plugin-manifest /plugins/manifest.json`；清单中相对路径以清单所在目录为基准。不要把凭据放进镜像或命令参数。

服务直接接收 SIGTERM 并停止接入，等待当前请求退出后回收子进程。容器停止宽限期应大于读请求超时和执行超时之和，并为清理预留时间；例如上述配置使用 `docker stop --time 45 linkd-plugin-runtime`。

## 镜像内调试

同一镜像可以运行开发示例，调试只计算，不发布事件：

```bash
docker run --rm \
  --mount type=bind,src="$PWD/python/examples",dst=/examples,readonly \
  linkd-plugin-runtime:local debug \
  --request /examples/push_request.json \
  --plugin-file /examples/push_plugin.py
```

完整插件契约见 [插件开发指南](../docs/guides/python-plugin-development.md)。部署前还需要验证目标架构的驱动包、厂商网络连通性和容器资源预算；本镜像不提供不可信代码沙箱。

## Kubernetes 部署

Linkd 2.x 分支不内置 Helm Chart。环境侧需独立配置 Python Deployment/Service，沿用上文镜像、启动参数、凭据文件和并发预算；Collector 使用内部 Service 的 `/execute` 地址。默认不启动新角色，部署配置见 [Kubernetes 部署指南](../docs/guides/helm.md)。实际 Kubernetes 启动仍待验收。

## 安装产物回归

构建完成后，仅挂载测试与示例，验证镜像里实际安装的 wheel，不用源码目录覆盖安装包：

```bash
docker run --rm --read-only --tmpfs /tmp \
  --mount type=bind,src="$PWD/python/tests",dst=/suite/tests,readonly \
  --mount type=bind,src="$PWD/python/examples",dst=/suite/examples,readonly \
  --env PYTHONPATH=/opt/runtime/.venv/lib/python3.11/site-packages \
  --entrypoint /opt/runtime/.venv/bin/python \
  linkd-plugin-runtime:local \
  -W error::ResourceWarning -m unittest discover -s /suite/tests
```

测试中的纯标准库调试子进程使用 `-S`，因此显式指向镜像的安装目录；这里没有挂载 linkd_runtime 源码。该命令只启动临时测试容器，不启用业务来源。

## 当前验证范围

2026-11-14：从当前源码重新构建 wheel，并通过同一 Dockerfile 的离线模式构建 Linux ARM64 最终镜像。镜像内 UID/GID 65532、只读根文件系统和独立 /tmp 下的全部 108 项测试通过，测试仅挂载 tests/examples，执行实际安装的运行时。覆盖同步 HTTP、常驻子进程、多线程、超时、调试、相对导入附件、指标宿主、TCOP 跨宿主时区指纹、vCenter TLS/超时及 debug 预算/进程回收回归；厂商连接使用替身。

同一镜像已单独验证 requests、pymssql、pyVmomi 导入，确认 Django 不存在，并成功执行安装后的 debug 命令。此前还验证过 2 进程×2 线程服务的四路同步 HTTP 调用，以及 SIGTERM 后前台容器退出码 0。临时测试容器已清理。

2026-11-15：修复旧蓝鲸监控插件的 UTC 查询窗口后，再次构建 wheel 和 Linux ARM64 镜像。沿用上述非 root、只读根文件系统和仅挂载 tests/examples 的方式，全部 109 项测试通过；新增覆盖等价时区偏移输入、历史激活时间及 12 小时窗口裁剪。宿主 Python 3.11 与 3.14 同样通过 109 项测试。厂商接口使用替身，未部署或启用业务来源。

在线构建曾在安装 uv 时因容器出网 `Network is unreachable` 中止；本次使用已准备的离线依赖完成构建，不宣称在线构建网络已恢复。AMD64、Kubernetes 实际部署及厂商连通性仍未验证。
