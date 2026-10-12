# KAC V2 插件调试同步预览本地联调

## 范围与源码

验证时间：2026-10-12。Linkd 主仓 HEAD `c818ad72`，Kingeye 主仓 HEAD `da37f8551b`，
均包含本轮未提交的调试预览改动。采用真实本地 MySQL 8.4、Redis 7、Elasticsearch 7.17.7
和 Linkd 控制面进程；Linkd、KAC 与 Web 联调入口由 PM2 托管。

本轮未启动 Kafka、Cleaner 或 Lifecycle Worker，没有向正式消息链路发送调试输入。
本地登录会话、专用来源授权和外部许可采用联调夹具；未验证真实 SSO、IAM 或许可服务。

## 输入与成功证据

专用来源 `local-kac-debug-20261012`，租户 `system`，始终禁用，尚未发布 Linkd 来源或分配通道。
在本机现有 MySQL 上复制出专用联调库，不修改原有业务库表结构。

插件接受推送 JSON，输出包含一个 warning/triggered evaluation 的 RawEvent：
标题为“本地 CPU 告警”，content 为 `cpu_usage=95`，bk_biz_id 为字符串 `0`。
原始输入另包含 `manual_content=人工清洗后的告警内容`，用于检验原生输入重用。

实际 HTTP 调用：

| 步骤 | 实际结果 |
| --- | --- |
| KAC `event/common/{id}/` 取样 | HTTP 200，is_validated=true，原生样本和首次预览保存成功 |
| verification | HTTP 200，返回 KAC 字段及 sample_ref |
| verify_alarms | HTTP 200，同一会话重算，name/action/level/bk_biz_id 可见 |
| verify_filter_alarms | 按旧 name 字段过滤，HTTP 200，保留一行 |
| verify_extra_clean_alarms | 从首次捕获的 manual_content 赋值 content，HTTP 200，展示新内容 |
| verification/result | HTTP 200，读取持久化后的最终结果；最终来源状态为 validated |

最终 verification 索引为 `cw_kingeye-web_saas_3.0_alarm_source_verification_local-kac-debug-20261012`，
恰有两个文档：raw 与 result。`linkd_debug_payload` 的实际 mapping 为 text/index:false。
直接读取 raw 的 content 仍为 `cpu_usage=95`；result 的 content 为“人工清洗后的告警内容”，
bk_biz_id 为数值 0。原生数据未被展示结果覆盖。

专用来源的正式 KAC 告警文档数为 0，alarm_num 为 0，is_enable=false，linkd_channel={}，
linkd_source_id 仍为空。上述证据证明本次样本没有建立正式告警或接入通道。

## 失败与恢复证据

- 发送旧 sample_ref，KAC 返回 is_validated=false。
- 停止本轮 Linkd PM2 进程，KAC 重新过滤校验返回 is_validated=false。
- 再读 verification/result，旧成功结果已替换为 is_validated=false 和空 items。
- 恢复 Linkd，使用原会话再次过滤、扩展清洗成功，最终预览重新可读；无需重新采样。

## 联调发现并修正

1. AlarmSource.save() 在 validated 状态会删除 verification 索引。V2 步骤与 guide 的最终
   保存改用明确的 QuerySet.update，保留原生样本和结果；V1 保留现有行为。
2. 推送页面首步通过 event/common 完成，不会额外调用 verification 步骤接口。取样和
   结果保存均成功后记录 verification 首步通过，避免最后一步误判前置步骤缺失。
3. 插件调试日志采用 Python logger 的 %s 参数格式，避免有效请求产生日志格式错误。

35 项 KAC 相关隔离回归、3 项页面行为测试、Python Ruff 和五个受影响 Vue 文件 ESLint
均通过。这些测试与上述真实 HTTP/ES/MySQL 联调证据分别记录，不互相替代。

## 页面验证边界

Web 主站壳的初始化仍依赖外部菜单、许可和 IAM。本轮本地入口直接挂载仓库原有
PullPlugin 四步调试组件及其子组件，接口使用原 KAC API 客户端，全部业务请求指向
本地 KAC。这个入口用于验证调试组件的请求和预览，不代表完整主站登录/菜单已联调。

浏览器中实际点击第二、第三、第四步的“校验”，原组件通过原 KAC API 客户端发送请求，
均显示“校验通过”。最终表格与 JSON 显示清洗后的内容以及数值 0；刷新后恢复同一会话的预览。
联调入口按实际 Django CSRF cookie 名称配置，请求经过正常 CSRF 检查。

使用现有已安装 Web 依赖，未更改 package.json 或 lockfile；图标库为本机现有 0.4.11，
与 lockfile 的 0.4.12 存在版本差异。Webpack 开发编译存在非调试模块的既有警告，
本次不据此声明完整生产构建门禁通过。完整 Linkd make check 的 Helm 阶段仍受本机
缺少 helm CLI 阻断。本次没有部署至测试或生产环境。

设计与外部接口见[方案](../design/kac-alarm-source-debug-preview.md)和
[接口契约](../reference/contracts/kac-ingest-preview.md)。
