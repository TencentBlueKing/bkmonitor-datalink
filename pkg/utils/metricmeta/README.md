# 指标元信息

`metricmeta` 使用标准库提供协议、待发合并、双 Bloom 去重、防抖和限额；发送目标由使用方注入。

```go
config := metricmeta.DefaultConfig()
exporter, err := metricmeta.NewExporter(config, exportJSON)
if err != nil {
    return err
}
if err := exporter.Start(); err != nil {
    return err
}
key := metricmeta.NewKey(dataID, metricmeta.Scope{Type: metricmeta.ScopeAPM, Name: scope}, metric)
exporter.Store().Observe(key, value)
return exporter.Stop()
```

- `Observe` 合并同一主键的完整最新值，拒绝时返回 `false`，原因从 `Stats().Dropped` 获取。
- `ExportFunc` 接收 JSON 数组，返回 `nil` 确认整批，返回错误保留待发项，必须响应 `context` 取消。
- `Start` 幂等，`Stop` 拒收新值并用独立期限发送最后一轮，未启动也可以关闭，关闭后不可重启。

## 去重与防抖

默认每 `1 h` 轮换一个 Bloom，查询当前与上一小时的过滤器，只向当前过滤器登记成功版本。版本保留 `1 h～2 h`，重复观察不会延长有效期，过期后通过继续观察重发；总目标误判率为 `10⁻⁶`。

同键连续变更只保留一个候选，默认等待稳定 `1 min`，相同值不会重置计时，连续变化最多等待 `10 min`。历史值回退可能命中过往版本，等待轮转或调用 `Clean()` 后重报。

每个 Bloom 的成功版本达到容量后，发送暂停至小时轮换；已接收的积压保留，新的未命中版本被拒绝，容量满不触发清空。`Clean()` 保留待发项和防抖状态，使在途旧快照不能重新填充 Bloom。

## 限额与配置

使用 `DefaultConfig()` 后覆盖字段；配置在构造时校验。collector 的完整配置片段见 [metric_metadata.yml](../../collector/example/metric_metadata.yml)。

| 限制 | 默认值 | 行为 |
| --- | --- | --- |
| 待发主键数／字符串总字节 | `100,000`／`4,096 B` | 新主键或整条元信息超限时拒绝 |
| 每批条目数／JSON 数组字节 | `1,000`／`256 KiB` | 同时满足，单条编码超限时丢弃并计数 |
| 发送间隔／整轮期限 | `100 ms`／`5 s` | 失败或超时回填，保留并发更新的新值 |
| 全局更新数／DataID 更新数 | 每 `1 min` `100,000`／`10,000` | 固定窗口熔断，到期自动恢复 |
| DataID 黑名单／独立限额 | 空 | 独立限额 `0` 禁用该 DataID 的元信息 |

配额统计候选新增与变更，包含被防抖合并的版本；相同待发值和已发送版本不重复收费。黑名单、拒绝和熔断仅影响元信息，采集器继续处理数值。

`Stats()` 返回独立快照；collector 将缓存、丢弃原因、重试和整轮耗时暴露为 `bk_collector_metric_metadata_*` 指标，这些统计指标的标签不包含 DataID 或指标名。

collector 默认把发送批次发布为 `bk_collector_metric_metadata_info=1`，标签包含元信息字段，`present` 区分未知字段与空字符串。同一主键替换旧版本，最多保留 `max_pending_entries` 条，默认 `info_retention=2m` 后清理；保留时间应覆盖至少两次采集间隔。确认表示进入本机采集窗口，远端入库需要通过监控链路验证。指定 `endpoint` 时改为 HTTP 导出。

## 验证

```bash
go test -race ./metricmeta
go vet ./metricmeta
go test ./metricmeta -run '^$' -bench . -benchmem
```

模块语言版本为 Go `1.21`。collector 本地联调需在仓库根目录创建包含 `./pkg/utils`、`./pkg/collector` 的 `go.work`；发布时先发布 utils，再更新 collector 的 utils 依赖。
