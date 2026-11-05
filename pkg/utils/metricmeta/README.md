# 指标元信息

`metricmeta` 使用标准库提供协议、待发合并、双 Bloom 去重和限额；发送目标由使用方注入。

```go
config := metricmeta.DefaultConfig()
config.Exporter = metricmeta.ExporterConfig{
    Interval:  time.Minute,
    Timeout:   5 * time.Second,
    BatchSize: 1_000,
    QueueSize: 100_000,
}
exporter, err := metricmeta.NewBatchExporter(config, exportEntries)
if err != nil {
    return err
}
if err := exporter.Start(); err != nil {
    return err
}
key := metricmeta.NewKey(dataID, metricmeta.Scope{Type: metricmeta.ScopeAPM, Name: scope}, metric)
key.BizID = bizID // 鉴权后的来源业务，保留负数空间 ID。
exporter.Store().Observe(key, value)
return exporter.Stop()
```

- `Observe` 合并同一主键的完整最新值，拒绝时返回 `false`，原因从 `Stats().Dropped` 获取。
- `NewBatchExporter` 的回调同步接收只读的 `[]metricmeta.Entry`，返回 `nil` 确认整批，返回错误则保留未确认项。回调须响应 `context` 取消，跨调用保留条目时先复制切片。
- 使用 JSON 协议时调用 `NewExporter(config, exportJSON)`，回调接收同一批条目的 JSON 数组。
- `Start` 幂等，`Stop` 拒收新值并用独立期限发送最后一轮，未启动也可以关闭，关闭后不可重启。

## 去重与待发合并

默认每 `1 h` 轮换一个 Bloom，查询当前与上一小时的过滤器，只向当前过滤器登记成功版本。版本保留 `1 h～2 h`，重复观察不会延长有效期，过期后通过继续观察重发；在每代写入设计量时，总目标误判率为 `10⁻⁶`。

待发缓存按主键保留最新值。默认每分钟刷新一次，每轮对每个主键最多发送一个版本。历史值回退可能命中过往版本，等待轮转或调用 `Clean()` 后重报。

包内按每代 100,000 个不同版本分配位图。超过设计量仍继续发送和登记，小时轮转会逐代清除旧位图。连续两小时各写入 200,000 个版本时，双代合计理论误判率约为 0.49%。`Clean()` 保留待发项，使在途旧快照不能重新填充 Bloom。

## 限额与配置

`Config.Dedup` 管理去重时间，`Config.Limits` 管理每分钟的更新次数，`Config.Exporter` 管理发送周期、整轮超时、每批条数和待发容量。collector 在平台配置 `exporter.metric_metadata.exporter` 下读取四个发送参数，省略的字段采用 SDK 默认值。见 [collector 配置示例](../../collector/example/metric_metadata.yml)。

| 限制 | 默认值 | 行为 |
| --- | --- | --- |
| 待发指标数 | `100,000` | 缓存已满时拒绝新增指标 |
| 每批元信息条数 | `1,000` | 达到上限时分批发送 |
| 每轮刷新期限 | `5 s` | 满批立即发送，尾批在本轮结束时发送，未确认项可重试 |
| 全局更新数／DataID 更新数 | 每 `1 min` `100,000`／`10,000` | 固定窗口熔断，到期自动恢复 |
| 来源 DataID 覆盖额度 | 空 | 独立限额 `0` 禁用该 DataID 的元信息 |

配额统计候选新增与变更，包含被待发缓存合并的版本；相同待发值和已发送版本不重复收费。黑名单、拒绝和熔断仅影响元信息，采集器继续处理数值。

`Stats()` 返回独立快照；collector 将缓存、丢弃原因、重试和整轮耗时暴露为 `bk_collector_metric_metadata_*` 指标，这些统计指标的标签不包含 DataID 或指标名。

collector 将元信息作为 `bk_collector_metric_metadata_info=1` 发送到配置的目标指标 DataID。维度 `dataid` 与 `bk_biz_id` 标识来源，`present` 区分未知字段与空字符串。发送成功表示本地客户端接纳，远端存储需另行核对。`/metrics` 仅提供运行统计。

平台配置用 `dataid` 指定元信息上报目标。`rate_limit.instance_per_minute` 限制每个 collector 实例，`rate_limit.dataid_per_minute` 设置每个来源 DataID 的默认额度，单位均为每分钟更新数，多副本不共享额度。

联调时，平台启用元信息上报并指定 collector 自监控 DataID `1100014`。同一目标下重载发送参数或默认额度时保留待发值、已发送版本和当前窗口计数。切换目标后，collector 会向新目标重新上报后续观察到的版本。

## 验证

```bash
go test -race ./metricmeta
go vet ./metricmeta
go test ./metricmeta -run '^$' -bench . -benchmem
```

模块语言版本为 Go `1.21`。collector 本地联调需在仓库根目录创建包含 `./pkg/utils`、`./pkg/collector` 的 `go.work`；发布时先发布 utils，再更新 collector 的 utils 依赖。
