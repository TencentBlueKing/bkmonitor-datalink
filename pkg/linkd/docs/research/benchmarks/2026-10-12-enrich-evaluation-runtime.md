# Enrich evaluation 工作对象改造验证

日期：2026-10-12。范围：本地实现和纯 CPU 微基准；未部署到 bkee5。

本次改造让每个等级持有一个工作对象，复用原始事实与当前 Go 查询视图。操作直接应用已求值补丁，保留局部撤销、原始字段保护和逐等级隔离。Lifecycle 保存丰富结果后继续复用冻结对象，恢复仅解释已存储补丁；持久化检查点和 CAS 保留。

## 基准方法

- Apple M4 Pro，darwin/arm64，Go 1.26.7。
- 用例见 [evaluation_bench_test.go](../../../internal/enrich/evaluation_bench_test.go)：40 KiB 来源 JSON、1 个 evaluation、1/8 个简单内容补丁步骤。
- 修改实现前后均运行三次，表中为算术平均。基线 Enrich 实现与 `e1320473` 中的实现相同；基准文件在修改实现前加入并执行。
- 不连接 ES、Redis、Kafka、MySQL 或其他数据源。结果不能直接推算真实 Lifecycle 吞吐。

```bash
go test ./internal/enrich -run '^$' -bench '^BenchmarkEvaluationChain$' -benchmem -count=3
```

| 步骤数 | 耗时 ms/op：改造前 → 后 | B/op：改造前 → 后 | allocs/op：改造前 → 后 |
|---|---|---|---|
| 1 | 0.533 → 0.350 | 551,886 → 276,348 | 837 → 532 |
| 8 | 4.162 → 0.420 | 3,603,801 → 369,159 | 6,219 → 2,132 |

8 步骤用例约快 9.9 倍，累计分配字节减少约 89.8%，分配对象数减少约 65.7%。保留输出协议的规范化与提交边界校验，步骤之间不再重建完整 Event。

## 验证范围

- `make check` 通过：格式、全量普通测试、go vet、竞态、golangci-lint、Console、Helm 和发布脚本门禁。
- 新增回归覆盖：单工作对象和多等级共享原始事实、原子失败撤销、新建父节点撤销、panic/error/invalid/cancel 隔离、冻结补丁的值所有权、字段/预算限制、大数和 JSON 编码计量一致性、跨租户及 payload 变更缓存失效。
- 现有自定义规则、KAC 样例、策略、生命周期、存储和 all-in-one 测试通过。
- 存储使用的 JSON 字段与补丁顺序保留；新增工作对象和缓存引用不进入 JSON。

## 后续运行验证

需要发布后用同一输入速率、并发和资源限制比较 Enrich/ProcessEvent 耗时、CPU、分配速率、GC、积压与下游延迟。此报告不把本地微基准表述成环境上的处理速度。
