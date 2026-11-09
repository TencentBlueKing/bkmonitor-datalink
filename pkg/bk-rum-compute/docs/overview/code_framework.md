# 代码结构

包根为 `com.tencent.bk.bkmonitor.rum`。

| 类或包 | 职责 |
| --- | --- |
| `RumPrecomputeJob` | 读取 JSON 和命令行参数，配置 Flink 环境，提交及停止作业 |
| `RumJobConfig` | 解析并校验运行参数 |
| `RumJobTopology` | 装配 Source、Session/View 分支及 Sink |
| `serde` | Kafka 消息与 `RumEvent` 的转换 |
| `process` | keyed state、窗口定时器、去重和侧输出 |
| `aggregation` | 事件分类及 Session/View 字段累计 |
| `model` | 输入事件、聚合状态和输出对象 |
| `precalculate` | 索引命名、分表选择及路由缓存 |
| `sink` | 输出字段映射与 Elasticsearch 请求构造 |
| `utils` | 身份编码、字段取值、时间转换等工具 |

源码位于 [src/main/java](../../src/main/java/com/tencent/bk/bkmonitor/rum)，测试位于 [src/test/java](../../src/test/java/com/tencent/bk/bkmonitor/rum)。

新增聚合字段时，同步检查状态模型、聚合器、输出映射、Elasticsearch 模板和字段文档。修改状态字段、key、算子 UID 或状态描述符时，需要验证 Checkpoint/Savepoint 兼容性。
