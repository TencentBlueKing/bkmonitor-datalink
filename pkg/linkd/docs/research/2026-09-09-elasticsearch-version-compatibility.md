# Elasticsearch 7/8/9 兼容性调研与验证

日期：2026-09-09。源码基线：`71b4c047` 加本次 Linkd 工作树改动；本次未提交改动。
范围：Linkd Repository、Control Plane 使用的存储管理/来源持久化、OneModel 查询、Console ES 连接层。
已确认支持目标：Elastic Elasticsearch **7.10.0 起的 7.10–7.17、8.x、9.x**。
使用契约与部署要求统一维护在[兼容指南](../guides/elasticsearch-compatibility.md)。

## 事故证据

目标环境返回版本 7.10.1；别名 `linkd-events` 关联三个时间桶。
相同时间范围普通排序查询命中 1329 条，三个分片无失败；PIT 查询加入 `_shard_doc` 后，
有数据的分片返回 `No mapping found for [_shard_doc] in order to sort on`，另外两片被跳过，
HTTP 200 响应中 `_shards.failed=1`、`hits.hits=[]`。Console 原实现没有检查部分失败状态。
统计没有该排序，因此出现列表空而统计有数的现象。

原 `tests/e2e/allinone/all_in_one_test.go` 明确断言 ES 7.17.7；本次查询现存
`linkd-ghcr-es` 测试容器根 API，也确认是 7.17.7。Console 原 ES 单元测试 mock fetch，
浏览器测试 mock API，不是 ES 7.10 的实际协议测试。

## 与项目相关的版本变化

| 版本节点 | 变化与证据 | 对 Linkd/Console 的处理 |
|---|---|---|
| 7.0–7.7 | 不在本次支持范围；不能用 7.17 测试代表整个 7.x | 不为早期版本新增分页降级 |
| 7.8 | [Composable index templates 引入](https://www.elastic.co/search-labs/blog/index-composable-templates) | Linkd 使用 `_index_template`，最低 7.10 已覆盖 |
| 7.9 | [Resolve index API](https://www.elastic.co/guide/en/elasticsearch/reference/7.9/indices-resolve-index-api.html) 已提供索引、别名、data stream 解析 | Console 使用 `_resolve/index` |
| 7.10 | [PIT reader 引入](https://www.elastic.co/blog/whats-new-elasticsearch-7-10-0-searchable-snapshots-store-more-for-less/) | 作为支持下限，保持快照分页 |
| 7.10 | [FieldSortBuilder 源码](https://github.com/elastic/elasticsearch/blob/v7.10.1/server/src/main/java/org/elasticsearch/search/sort/FieldSortBuilder.java) 不接受 `format` | 移除 Go 查询中的 `sort.format`，保留纳秒精度 |
| 7.12 | [Elastic 工程师确认 `_shard_doc` 的引入版本](https://discuss.elastic.co/t/elastic-no-mapping-found-for-shard-doc-in-order-to-sort-on/268188/2) | 改为实体身份与 `_index` 的显式全序；Go 去掉新 ES 隐式追加的排序尾项 |
| 7.17 | 原实际测试基线；可作为 8.x 升级路径中的旧主版本末期节点 | 保留真实回归样本，不将其等同于 7.10 |
| 8.x | [8.0 迁移说明](https://www.elastic.co/guide/en/elasticsearch/reference/8.19/migrating-8.0.html)：移除 mapping types，默认安全行为变化 | 原生 typeless API；按部署配置认证和 CA |
| 9.x | [REST 兼容模式仅跨一个 major](https://www.elastic.co/docs/reference/elasticsearch/rest-apis/compatibility)；9 不提供通用 7 API 兼容模式 | 不使用固定 `compatible-with=7`，验证原生标准 REST 请求 |
| 9.1+ | [Bulk 协调拆分到 write_coordination](https://www.elastic.co/docs/reference/elasticsearch/configuration-reference/thread-pool-settings) | 当前 Console write 指标保留单线程池含义，不宣称包含 bulk 协调 |

本表只列与当前实现相关的变更，不是所有 minor 的完整发行说明。
升级既有集群的索引兼容和滚动升级步骤遵循 Elastic 官方升级路径，不由应用查询兼容代替。

## 实现审查

Go Repository 使用 typeless `_doc`、`_create`、`_update`、bulk/msearch、seq_no/primary_term CAS、
`_index_template`、aliases、PIT 和普通 search；未引入绑定 major 的 Elasticsearch SDK。
EventSource 存储使用普通 mapping、`_doc`、`refresh=wait_for`、CAS、keyword 排序；
OneModel 使用 term 查询，均不依赖 mapping types。
Console 拓扑和节点统计使用标准 REST，并对可选统计保留不可用状态。

本次改动统一使用各版本已有的查询语法，没有按错误文本重试或对未知版本盲目降级。
新旧游标排序协议不同，直接拒绝旧游标；未新增持久化 schema、双写或数据迁移。
完整性检查仅保留超时和失败分片数量，不将原始业务查询或敏感 ES 错误内容透出。

## 真实版本矩阵

| 版本 | Go Repository / EventSource / OneModel | Console 三实体查询与分页 |
|---|---|---|
| 7.10.0 | 通过 | 通过 |
| 7.10.1 | 通过 | 通过 |
| 7.17.7 | 通过 | 通过 |
| 8.0.0 | 通过 | 通过 |
| 8.19.0 | 通过 | 通过 |
| 9.0.0 | 通过 | 通过 |

矩阵执行完整 Go 包测试（含显式启用的真实 ES 集成测试），Console 每版本验证一个
包含三实体、多索引、多分片、详情/统计、空结果和逐页纳秒排序的真实测试。
ES 8.0.0 官方 arm64 镜像的旧 JDK 在 Apple Container cgroup v2 下启动出现 NPE；
仅在该测试容器设置 `_JAVA_OPTIONS=-XX:-UseContainerSupport` 后启动，未修改应用运行配置。

环境：Apple Container，官方 Elastic Linux arm64 镜像，独立测试索引；主机到容器通过本地
HTTP→container exec curl 转发器连接。测试不连接用户 Kubernetes，未变更其现有部署或数据。
矩阵入口：[scripts/test-elasticsearch-compatibility.sh](../../scripts/test-elasticsearch-compatibility.sh)。

限制：不保证已穷举范围内每个 minor/patch；8/9 安全默认配置、私有 CA、权限角色、
混合版本滚动升级和所有未来 9.x 小版本仍需部署侧验收。协议契约通过不等于生产全链路 E2E。
