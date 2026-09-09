# Elasticsearch 兼容范围

Linkd 与 Console 的目标支持范围为 **Elasticsearch 7.10–7.17、8.x、9.x**，最低版本为 **7.10.0**。
范围针对 Elastic Elasticsearch 的标准 REST API，不自动包含 OpenSearch、Serverless 或厂商修改版。
版本支持以本页的已验证版本及测试范围为证据，不表示每个 minor/patch 都已经逐一测试。

## 配置与部署

使用既有 `storage.elasticsearch` 配置即可，不需要设置版本号，也不需要修改已有索引 mapping。
Go 端使用项目内 HTTP Transport，Console 使用 Node 原生 fetch；均调用 typeless REST API，
发送 `application/json` 或 bulk/msearch 所需的 `application/x-ndjson`，没有绑定某个 major 的官方 SDK。
不发送固定的 `compatible-with=7`：ES 的兼容模式只跨一个 major，不能作为 7 到 9 的通用适配。

8.x/9.x 默认安全配置与旧开发环境不同；按集群实际情况配置 HTTPS、Basic Auth 或 API Key。
私有 CA 必须进入运行时信任链（Go 使用系统证书或 `SSL_CERT_FILE`，Node 可使用 `NODE_EXTRA_CA_CERTS`）。
本次版本矩阵使用隔离的无鉴权 HTTP 测试容器，未把该结果当作生产 TLS/权限验证。

升级此修复需要重新构建并部署 **Linkd 和 Console 两个镜像**，不需要重建 ES 索引。
旧分页游标失效，请重新执行查询或从扫描起点重试；不能沿用旧排序生成的游标。

## 查询与可靠性

- 保留 PIT 快照与 `search_after`，不改为非快照分页或无界全量查询。
- 显式排序使用业务时间、作用域内实体身份、物理索引 `_index`，不依赖 7.12 才引入的 `_shard_doc`。
  每个物理索引内实体身份唯一，索引名区分时间桶及 Active/History 过渡副本。
- 不使用 ES 7.10 不支持的 `sort.format`。Go 以 `json.RawMessage` 保留排序数值；
  Console 把超出 JS 安全整数范围的纳秒时间精确转换成 ISO 纳秒字符串，避免翻页时精度丢失。
- 对 HTTP 200 中的 `timed_out`、`_shards.failed` 显式报错。
  适用于 Repository search/msearch/PIT、EventSource 列表、OneModel 查询以及 Console 查询，
  防止查询失败被误判成无数据、对象不存在或扫描完成。
- Console 的 ES 节点 `write` 指标表示该线程池本身；ES 9.1+ 的独立 `write_coordination`
  不包含在这些指标中，不能将其解释成所有 bulk 协调队列总量。

## 验证与复现

2026-09-09 的版本矩阵与 API 差异证据见
[ES 7/8/9 兼容性调研](../research/2026-09-09-elasticsearch-version-compatibility.md)。

对独立测试集群运行：

```bash
sh scripts/test-elasticsearch-compatibility.sh \
  http://es-7-test:9200 \
  http://es-8-test:9200 \
  http://es-9-test:9200
```

脚本必须在 Linkd 仓库中执行，使用现有 Go、Node 24 和 Console node_modules。
测试创建独立前缀/部署作用域的临时索引与模板，并清理自身创建的资源；普通 `make check`
不会隐式连接本机 ES。真实版本测试覆盖：

- Repository 单条与合批契约、实时读取、幂等/CAS、事件投影、Alert 归档、多页事件扫描；
- EventSource Record/Release 初始化、创建冲突、CAS、列表与缺失对象；
- OneModel 扁平查询及租户隔离；
- Console 三种实体的跨索引/多分片 PIT 分页、1ns 时间差、详情、统计与空结果。

真实协议契约测试不等于 Kafka→Cleaner→Lifecycle 的完整部署 E2E，也不覆盖真实集群故障、
滚动升级、混合版本集群、生产网络策略或性能压测。原 all-in-one E2E 的 ES 基线仍为 7.17.7。
