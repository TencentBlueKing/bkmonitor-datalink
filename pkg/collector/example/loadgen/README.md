# loadgen 使用手册

`loadgen` 是 bk-collector 自适应限流的压测客户端。

单二进制零依赖，向 OTLP HTTP `/v1/traces` 串行跑 warmup → burst → bigpayload 三个阶段，逐阶段打印各状态码计数与成功请求 p99。

## 0x01 编译

源码仓库内交叉编译并打包：

```shell
cd pkg/collector
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/loadgen-linux-amd64 ./example/loadgen
tar -C dist -czf dist/loadgen-linux-amd64.tar.gz loadgen-linux-amd64
```

Linux arm64 把 `GOARCH=amd64` 换成 `GOARCH=arm64`，输出文件名同步替换。

Go 工具链 ≥ `1.23.0`，与 `pkg/collector/go.mod` 声明一致。

本机临时验证：

```shell
go build -o loadgen ./example/loadgen && ./loadgen -h
```

## 0x02 命令行参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-url` | `http://127.0.0.1:4318/v1/traces` | 目标 OTLP HTTP traces 地址。 |
| `-token` | 空 | `X-BK-TOKEN` 请求头，空值不带 token。 |
| `-c` | `50` | `burst` / `bigpayload` 并发数，`warmup` 取 `max(1, c/5)`。 |
| `-d` | `30s` | 每个阶段持续时间，总运行约 `3 × d`。 |
| `-warmup-spans` | `32` | `warmup` 阶段单请求 span 数。 |
| `-burst-spans` | `128` | `burst` 阶段单请求 span 数。 |
| `-bigpayload-spans` | `512` | `bigpayload` 阶段单请求 span 数，抬高 collector 单批处理工作量时主要调这一项。 |

## 0x03 三阶段负载

| 阶段 | 并发 | 单请求 span 数 | 单 span 填充串 | 实测请求体 | 观察重点 |
| --- | --- | --- | --- | --- | --- |
| `warmup` | `max(1, c/5)` | `32` | `128 B` | ≈ `13 KB` | 低负载基线，验证链路通顺。 |
| `burst` | `c` | `128` | `1024 B` | ≈ `160 KB` | CPU 接近上限，看是否开始返 `429`。 |
| `bigpayload` | `c` | `512` | `4096 B` | ≈ `2.2 MB` | 单请求总成本放大，看大包下的限流效果。 |

## 0x04 2c2g 加压建议

`-c` 是并发数不是 QPS（实际 QPS ≈ 并发数 / 平均请求耗时）。对 `2c2g` collector 逐级加压：

| 目标 | 参数 | 预期 |
| --- | --- | --- |
| 冒烟 | `-c 20 -d 30s` | 全 `200`，`other` / `503` 接近 `0`。 |
| 基线 | `-c 50 -d 60s` | 记录正常负载下的 `success_p99` 与 collector CPU。 |
| 观察限流 | `-c 100 -d 60s` | CPU 接近 `2` 核，开限流后开始出 `429`。 |
| 压到过载 | `-c 200 -d 120s` | `burst` 或 `bigpayload` 把 collector 推到危险区。 |
| 强压兜底 | `-c 400 -d 180s` | 隔离环境专用，关限流可能触发 OOM、重启或大量超时。 |

完整命令示例：

```shell
./loadgen-linux-amd64 -url "http://<collector_ip>:4318/v1/traces" -token "$TOKEN" -c 200 -d 120s
```

把 `loadgen` 跑在 collector 之外的机器或容器，避免抢同一份 CPU。

`-c` 加到一定档位后 collector CPU 仍未到 limit，多半是单请求处理慢、客户端在请求响应上同步阻塞。

打破方法：加大 `-bigpayload-spans`（`1024` / `2048`）让单批工作量更密集，或多机并行起多个 `loadgen`。

对照验证流程：先关限流跑出高 CPU 与高延迟，再开限流确认 `429` 出现且 collector 不崩。

## 0x05 输出与状态码

```text
loadgen start: 2026-06-19T11:00:00+08:00
phase=warmup concurrency=20 spans=32 duration=60s start=2026-06-19T11:00:00+08:00 end=2026-06-19T11:01:00+08:00
  200=4567 429=0 503=0 other=0 success_p99=5.2ms
phase=burst concurrency=100 spans=128 duration=60s start=2026-06-19T11:01:00+08:00 end=2026-06-19T11:02:00+08:00
  200=12345 429=678 503=0 other=0 success_p99=12.3ms
phase=bigpayload concurrency=100 spans=512 duration=60s start=2026-06-19T11:02:00+08:00 end=2026-06-19T11:03:00+08:00
  200=8901 429=2340 503=0 other=12 success_p99=45.6ms
loadgen end: 2026-06-19T11:03:00+08:00 elapsed=3m0s
```

时间戳：整段 `start` / `end` 圈出指标查询窗口，每阶段的 `start` / `end` 可直接拷给 Grafana 或 PromQL `@` 修饰符。

状态码计数与延迟：

| 字段 | 含义 |
| --- | --- |
| `200` | collector 成功接收。 |
| `429` | 自适应限流拒绝，预期会出现。 |
| `503` | 旁路异常，需要查 collector 日志。 |
| `other` | 其他状态码与网络错误（按 `0` 计入），阶段尾部被自身 ctx 截断的 in-flight 请求不计入。 |
| `success_p99` | 成功请求的 p99 延迟，只统计 `2xx`。 |

开限流后的健康标准：`burst` / `bigpayload` 出现 `429`，collector 不 OOM 也不重启。

`503` 与 `other` 持续上升时优先查目标地址、token、collector 日志、容器资源限制。
