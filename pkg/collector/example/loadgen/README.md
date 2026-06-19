# loadgen 使用手册

`loadgen` 是 bk-collector 自适应限流验收用的轻量压测工具。它只依赖 Go 标准库，向 OTLP HTTP `/v1/traces` 发送 JSON trace，并按 `warmup`、`burst`、`bigpayload` 三个阶段串行施压。

## 打包二进制

建议使用 Go `1.23.x` 打包，`pkg/collector/go.mod` 当前声明为 `go 1.23.0`。

在源码仓库内执行：

```shell
cd pkg/collector
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/loadgen-linux-amd64 ./example/loadgen
tar -C dist -czf dist/loadgen-linux-amd64.tar.gz loadgen-linux-amd64
```

如果目标环境是 Linux arm64，将 `GOARCH=amd64` 改为 `GOARCH=arm64`，并同步调整输出文件名。

在目标环境解包：

```shell
tar -xzf loadgen-linux-amd64.tar.gz
chmod +x loadgen-linux-amd64
```

本机临时验证可直接编译：

```shell
cd pkg/collector
go build -o loadgen ./example/loadgen
./loadgen -h
```

## 参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-url` | `http://127.0.0.1:4318/v1/traces` | 目标 OTLP HTTP traces 地址。 |
| `-token` | 空 | 写入 `X-BK-TOKEN` 请求头；为空时不带 token。 |
| `-c` | `50` | `burst` 和 `bigpayload` 阶段并发数；`warmup` 阶段使用 `max(1, c/5)`。 |
| `-d` | `30s` | 每个阶段持续时间；总运行时间约为 `3 * d`。 |

三个阶段的负载含义：

| 阶段 | 负载 | 用途 |
| --- | --- | --- |
| `warmup` | 低并发、小包 | 建立基线，确认低负载不误丢。 |
| `burst` | 高并发、小包 | 观察 CPU 压力下是否开始返回 `429`。 |
| `bigpayload` | 高并发、大包 | 放大单请求成本，观察高成本请求下的限流效果。 |

## 常用命令

默认压测本机 collector：

```shell
./loadgen-linux-amd64 -token "$TOKEN"
```

指定目标地址：

```shell
./loadgen-linux-amd64 -url "http://<collector_ip>:4318/v1/traces" -token "$TOKEN"
```

低强度冒烟：

```shell
./loadgen-linux-amd64 -url "http://<collector_ip>:4318/v1/traces" -token "$TOKEN" -c 10 -d 10s
```

高强度压测：

```shell
./loadgen-linux-amd64 -url "http://<collector_ip>:4318/v1/traces" -token "$TOKEN" -c 100 -d 60s
```

不带 token 的本地联调：

```shell
./loadgen-linux-amd64 -url "http://127.0.0.1:4318/v1/traces" -c 20 -d 15s
```

## 2c2g 加压建议

`-c` 是并发数，不是 QPS。实际 QPS 约等于 `并发数 / 平均请求耗时`，会随 collector 响应耗时、网络延迟和限流状态变化。

如果目标 collector 资源限制约为 `2c2g`，建议按下表逐级加压：

| 目标 | 参数 | 预期现象 |
| --- | --- | --- |
| 冒烟 | `-c 20 -d 30s` | 请求能正常打通，`other` 和 `503` 接近 `0`。 |
| 基线 | `-c 50 -d 60s` | 观察正常负载下的 `200`、`success_p99` 和 collector CPU。 |
| 观察限流 | `-c 100 -d 60s` | CPU 接近 `2` 核上限时，开启限流后应开始出现 `429`。 |
| 压到明显过载 | `-c 200 -d 120s` | `burst` 或 `bigpayload` 阶段应能把 `2c2g` collector 推到危险区。 |
| 强压兜底 | `-c 400 -d 180s` | 仅在隔离环境使用；未开启限流或限流配置过松时，可能触发 OOM、重启或大量超时。 |

压垮 `2c2g` 的优先命令：

```shell
./loadgen-linux-amd64 -url "http://<collector_ip>:4318/v1/traces" -token "$TOKEN" -c 200 -d 120s
```

如果 collector CPU 没有接近 `2` 核，或 `bigpayload` 阶段仍没有明显延迟上升，再提高到：

```shell
./loadgen-linux-amd64 -url "http://<collector_ip>:4318/v1/traces" -token "$TOKEN" -c 400 -d 180s
```

建议把 `loadgen` 跑在 collector 之外的机器或容器里，避免压测客户端和 collector 抢同一份 CPU。对照验证时，先关闭限流确认可以打到高 CPU、高延迟或重启风险，再开启限流确认出现 `429` 且 collector 不崩溃。

## 结果解读

整次压测的起止时间会打到首尾，便于回查指标时圈定时间窗：

```text
loadgen start: 2026-06-19T11:00:00+08:00
phase=warmup concurrency=20 duration=60s start=2026-06-19T11:00:00+08:00 end=2026-06-19T11:01:00+08:00
  200=4567 429=0 503=0 other=0 success_p99=5.2ms
phase=burst concurrency=100 duration=60s start=2026-06-19T11:01:00+08:00 end=2026-06-19T11:02:00+08:00
  200=12345 429=678 503=0 other=0 success_p99=12.3ms
phase=bigpayload concurrency=100 duration=60s start=2026-06-19T11:02:00+08:00 end=2026-06-19T11:03:00+08:00
  200=8901 429=2340 503=0 other=12 success_p99=45.6ms
loadgen end: 2026-06-19T11:03:00+08:00 elapsed=3m0s
```

时间戳含义：

- `loadgen start` / `loadgen end`：整次压测的起止与累计耗时。
- 每个 `phase=` 行的 `start` / `end`：该阶段实际开始与结束时间，可直接拷给 Grafana 或 PromQL `@` 修饰符。

每行计数含义：

- `200`：collector 成功接收。
- `429`：被自适应限流拒绝，压测时出现该值通常符合预期。
- `503`：旁路异常状态，需要结合 collector 日志确认。
- `other`：其他状态码或网络错误，网络错误按状态码 `0` 计入。
- `success_p99`：成功请求的 p99 延迟，只统计 `2xx` 响应。

开启限流后的基本预期：`burst` 或 `bigpayload` 阶段可以出现 `429`，collector 不应 OOM 或重启；如果 `503`、`other` 持续增加，优先检查目标地址、token、collector 日志和容器资源限制。
