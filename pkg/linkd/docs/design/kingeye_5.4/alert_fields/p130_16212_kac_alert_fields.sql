-- KAC 5.4 告警字段目录。先选择目标数据库，在同一连接设置 @bk_tenant_id。
-- 示例（把 system、admin、kac 替换为目标租户、操作者与数据库名）：
--   mysql --init-command="SET @bk_tenant_id='system', @maintainer='admin'" kac < p130_16212_kac_alert_fields.sql
-- @maintainer 可省略，按 MaintainerInfo 的缺省值写空串。租户必须由调用方给出。
-- 适用于新建数据库，或已具备本文件完整表结构的新租户。重复登记报唯一键冲突。使用遇错即停的客户端执行整份文件。
-- 三张正式表的 DDL 先于数据事务；该文件不升级旧表，也不回填已登记租户。
SET NAMES utf8mb4;

CREATE TABLE IF NOT EXISTS alert_field (
    id BIGINT NOT NULL AUTO_INCREMENT,
    bk_tenant_id VARCHAR(128) NOT NULL,
    field VARCHAR(2048) NOT NULL,
    field_digest BINARY(32) NOT NULL,
    name VARCHAR(64) NOT NULL,
    category VARCHAR(16) NOT NULL,
    source_type VARCHAR(32) NOT NULL,
    value_type VARCHAR(16) NOT NULL,
    created_by VARCHAR(32) NULL DEFAULT '',
    updated_by VARCHAR(32) NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_af_tenant_digest (bk_tenant_id, field_digest),
    UNIQUE KEY uq_af_tenant_id (bk_tenant_id, id),
    KEY ix_af_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS kac_alert_field (
    id INT NOT NULL AUTO_INCREMENT,
    bk_tenant_id VARCHAR(128) NOT NULL,
    `key` VARCHAR(64) NOT NULL,
    key_display VARCHAR(64) NOT NULL,
    alias VARCHAR(64) NOT NULL DEFAULT '',
    alias_zh_hans VARCHAR(64) NULL DEFAULT '',
    alias_zh_hant VARCHAR(64) NULL DEFAULT '',
    alias_en VARCHAR(64) NULL DEFAULT '',
    alert_field_id BIGINT NULL DEFAULT NULL,
    category VARCHAR(16) NOT NULL,
    display_category VARCHAR(16) NOT NULL,
    source_type VARCHAR(32) NOT NULL,
    source_info JSON NOT NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'text',
    is_display TINYINT(1) NOT NULL DEFAULT 1,
    `order` INT NOT NULL DEFAULT 0,
    option_order INT NOT NULL DEFAULT 0,
    created_by VARCHAR(32) NULL DEFAULT '',
    updated_by VARCHAR(32) NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_kaf_tenant_id (bk_tenant_id, id),
    UNIQUE KEY uq_kaf_tenant_key (bk_tenant_id, `key`),
    UNIQUE KEY uq_kaf_tenant_standard (bk_tenant_id, alert_field_id),
    KEY ix_kaf_display (bk_tenant_id, display_category, `order`, id),
    KEY ix_kaf_option (bk_tenant_id, option_order, id),
    KEY ix_kaf_created_at (created_at),
    CONSTRAINT fk_kaf_tenant_standard FOREIGN KEY (bk_tenant_id, alert_field_id)
        REFERENCES alert_field (bk_tenant_id, id) ON DELETE RESTRICT ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS kac_alert_field_mapping (
    id BIGINT NOT NULL AUTO_INCREMENT,
    bk_tenant_id VARCHAR(128) NOT NULL,
    kac_alert_field_id INT NOT NULL,
    stage VARCHAR(32) NOT NULL,
    `usage` VARCHAR(32) NOT NULL,
    direction VARCHAR(8) NOT NULL,
    method VARCHAR(16) NOT NULL,
    path VARCHAR(2048) NULL DEFAULT NULL,
    created_by VARCHAR(32) NULL DEFAULT '',
    updated_by VARCHAR(32) NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_kafm_usage (bk_tenant_id, kac_alert_field_id, stage, `usage`),
    KEY ix_kafm_stage (bk_tenant_id, stage, kac_alert_field_id),
    KEY ix_kafm_created_at (created_at),
    CONSTRAINT fk_kafm_tenant_field FOREIGN KEY (bk_tenant_id, kac_alert_field_id)
        REFERENCES kac_alert_field (bk_tenant_id, id) ON DELETE RESTRICT ON UPDATE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 临时表只承载本次初始化的常量，在会话结束时自动释放。
CREATE TEMPORARY TABLE seed_standard_field (
    path VARCHAR(2048) NOT NULL,
    name VARCHAR(64) NOT NULL,
    category VARCHAR(16) NOT NULL,
    value_type VARCHAR(16) NOT NULL
);

CREATE TEMPORARY TABLE seed_kac_field (
    `key` VARCHAR(64) NOT NULL PRIMARY KEY,
    key_display VARCHAR(64) NOT NULL,
    category VARCHAR(16) NOT NULL,
    type VARCHAR(32) NOT NULL,
    is_display TINYINT(1) NOT NULL,
    `order` INT NOT NULL,
    option_order INT NOT NULL,
    alias_en VARCHAR(64) NOT NULL,
    alias_zh_hant VARCHAR(64) NOT NULL,
    standard_path VARCHAR(2048) NULL
);

-- 路径使用 Linkd Target.String() 的规范形式，摘要按其 UTF-8 字节计算。
INSERT INTO seed_standard_field (path, name, category, value_type) VALUES
    ('$["fingerprint"]', '事件指纹', 'alert', 'text'),
    ('$["title"]', '事件标题', 'alert', 'text'),
    ('$["content"]', '事件正文', 'alert', 'text'),
    ('$["occurred_at"]', '事件发生时间', 'alert', 'date'),
    ('$["evaluations"]', '各等级的判定', 'alert', 'list'),
    ('$["values"]', '事件观测值', 'alert', 'object'),
    ('$["labels"]["strategy_id"]', '监控项ID', 'alert', 'number'),
    ('$["dimensions"]', '事件维度', 'subject', 'object'),
    ('$["dimensions"]["bk_biz_id"]', 'CMDB业务ID', 'subject', 'number'),
    ('$["dimensions"]["bk_target_ip"]', '目标IP', 'subject', 'text'),
    ('$["subject_system"]', '对象系统', 'subject', 'text'),
    ('$["subject_type"]', '对象类型', 'subject', 'text'),
    ('$["subject_id"]', '对象身份', 'subject', 'text'),
    ('$["subject_name"]', '对象名称', 'subject', 'text'),
    ('$["bk_tenant_id"]', '租户ID', 'extra', 'text'),
    ('$["event_source_id"]', '事件来源ID', 'extra', 'text'),
    ('$["event_source_version"]', '接入来源版本', 'extra', 'number'),
    ('$["event_id"]', '事件ID', 'extra', 'text'),
    ('$["related_alert_ids"]', '关联告警ID列表', 'extra', 'list'),
    ('$["produced_at"]', '来源产生时间', 'extra', 'date'),
    ('$["received_at"]', '接收时间', 'extra', 'date'),
    ('$["create_at"]', '事件创建时间', 'extra', 'date'),
    ('$["source_event_id"]', '来源事件ID', 'extra', 'text'),
    ('$["source_alert_id"]', '来源告警ID', 'extra', 'text'),
    ('$["source_raw_data"]', '来源原始消息', 'extra', 'object'),
    ('$["labels"]', '来源标签', 'extra', 'object'),
    ('$["extra_data"]', '来源扩展数据', 'extra', 'object'),
    ('$["merge_origin"]', '合并来源信息', 'extra', 'object'),
    ('$["enrich_status"]', '丰富状态', 'extra', 'text'),
    ('$["enrich"]', '各等级的丰富结果', 'extra', 'object'),
    ('$["enriched_at"]', '丰富结果提交时间', 'extra', 'date'),
    ('$["enrich_config_digest"]', '丰富配置摘要', 'extra', 'text'),
    ('$["enrich_source_version"]', '丰富使用的来源版本', 'extra', 'number');

-- 标准目录同时登记已确认的标量丰富输出，类型供目录展示；按协议取值时仍保留原始类型或转换。
-- cloud_plat_name、app_service_alias 为 5.4 待补的 Processor 输出，路径登记不表示已有数据。
INSERT INTO seed_standard_field (path, name, category, value_type) VALUES
    ('$["labels"]["strategy_name"]', '监控策略名称', 'alert', 'text'),
    ('$["labels"]["bk_biz_id"]', 'CMDB业务ID', 'subject', 'number'),
    ('$["labels"]["bk_biz_name"]', 'CMDB业务名称', 'subject', 'text'),
    ('$["labels"]["bk_cloud_id"]', 'CMDB云区域ID', 'subject', 'number'),
    ('$["labels"]["bk_obj_id"]', 'CMDB模型ID', 'subject', 'text'),
    ('$["labels"]["bk_inst_id"]', 'CMDB模型实例ID', 'subject', 'text'),
    ('$["labels"]["model_inst_id"]', '对象模型实例ID', 'subject', 'text'),
    ('$["labels"]["model_id"]', '对象模型ID', 'subject', 'text'),
    ('$["labels"]["model_name"]', '对象模型名称', 'subject', 'text'),
    ('$["labels"]["bk_cloud_name"]', 'CMDB云区域名称', 'subject', 'text'),
    ('$["labels"]["log_query_string"]', '检索语句', 'subject', 'text'),
    ('$["labels"]["log_theme_id"]', '日志主题id', 'subject', 'number'),
    ('$["labels"]["log_theme_name"]', '日志主题名称', 'subject', 'text'),
    ('$["labels"]["log_relate_info"]', '日志关联信息', 'subject', 'text'),
    ('$["labels"]["cloud_plat_id"]', '云平台id', 'subject', 'text'),
    ('$["labels"]["cloud_plat_name"]', '云平台名称', 'subject', 'text'),
    ('$["labels"]["apm_app_name"]', 'apm应用名称', 'subject', 'text'),
    ('$["labels"]["apm_app_id"]', 'apm应用ID', 'subject', 'number'),
    ('$["labels"]["apm_app_alias"]', 'apm应用别名', 'subject', 'text'),
    ('$["labels"]["apm_service_name"]', 'apm服务名称', 'subject', 'text'),
    ('$["labels"]["app_service_alias"]', 'apm服务别名', 'subject', 'text'),
    ('$["labels"]["cluster_name"]', '集群名称', 'subject', 'text'),
    ('$["labels"]["namespace"]', '命名空间', 'subject', 'text'),
    ('$["labels"]["workload_name"]', '工作负载名称', 'subject', 'text'),
    ('$["labels"]["source_id"]', '告警源ID', 'extra', 'text'),
    ('$["labels"]["source_name"]', '告警源名称', 'extra', 'text'),
    ('$["labels"]["data_source"]', '数据来源', 'extra', 'text');

-- log_theme_id、apm_app_id 以固定投影的 number 登记；内置 Processor 对非数值来源 ID 仍可返回字符串。
-- 60 个 KAC key 中有 34 个标准字段引用。其余逐项注明保留 NULL 的原因。
INSERT INTO seed_kac_field
    (`key`, key_display, category, type, is_display, `order`, option_order, alias_en, alias_zh_hant, standard_path)
VALUES
    ('alarm_id', '告警ID', 'alert', 'text', 1, 1, 1, 'Alert ID', '', NULL),  -- Alert 身份 alert_id
    ('item_id', '监控项ID', 'alert', 'text', 1, 65, 65, '', '', '$["labels"]["strategy_id"]'),
    ('item', '监控项', 'alert', 'text', 1, 4, 4, 'Item', '', NULL),  -- 共享策略丰富的 $.labels.strategy_name，见用途记录
    ('name', '告警名称', 'alert', 'text', 1, 5, 5, 'Name', '', '$["title"]'),
    ('fingerprint', '告警指纹', 'alert', 'text', 1, 63, 63, 'Alert Fingerprint', '告警指紋', '$["fingerprint"]'),
    ('alarm_time', '告警时间', 'alert', 'time', 1, 7, 7, 'Occur Time', '', NULL),  -- Event 发生时间 / Alert 开始时间
    ('latest_alarm_time', '最近告警时间', 'alert', 'time', 1, 66, 66, '', '', '$["occurred_at"]'),
    ('content', '告警内容', 'alert', 'text', 1, 8, 8, 'Content', '', '$["content"]'),
    ('level', '告警等级', 'alert', 'text', 1, 10, 10, 'Severity', '', NULL),  -- 按 evaluation 或 Alert 等级转换
    ('metric_id', '指标ID', 'alert', 'text', 1, 67, 67, '', '', NULL),  -- 取事件对应的策略版本。单指标为指标全名，多指标为用指标全名展开的计算表达式字符串。
    ('strategy_id', '监控策略ID', 'alert', 'text', 0, 29, 29, 'Strategy ID', '', NULL),  -- 显式 extra_data.strategy_id 或丰富所得 monitor_template_id
    ('strategy_name', '监控策略名称', 'alert', 'text', 1, 30, 30, 'Strategy Name', '', '$["labels"]["strategy_name"]'),
    ('processing_status', '处理状态', 'alert', 'text', 1, 68, 68, '', '', NULL),  -- 按 Event ID 读 EventProcessing.state
    ('status', '告警状态', 'alert', 'text', 1, 38, 38, 'Status', '', NULL),  -- 生命周期与 KAC 处置状态
    ('source_alarm_status', '源告警状态', 'alert', 'text', 1, 42, 42, 'Source Alert Status', '源告警狀態', NULL),  -- 按 Alert 状态和结束类型转换
    ('tag_info', '告警标签', 'alert', 'text', 1, 9999, 9999, 'Tags', '', NULL),  -- 合并 Alert.policy_tags 与 KAC 标签
    ('object', '告警对象', 'subject', 'text', 1, 11, 11, 'Object', '', NULL),  -- extra_data.object、subject_name、subject_id 按优先级读取
    ('bk_biz_id', 'CMDB业务ID', 'subject', 'text', 0, 12, 12, 'BK Biz ID', '', '$["labels"]["bk_biz_id"]'),
    ('bk_biz_name', 'CMDB业务名称', 'subject', 'text', 1, 13, 13, 'BK Biz Name', '', '$["labels"]["bk_biz_name"]'),
    ('bk_set_id', 'CMDB集群ID', 'subject', 'text', 0, 14, 14, 'BK Set ID', '', NULL),  -- 拓扑可返回标量或列表，按有效 labels.bk_set_id / extra_data.bk_set_id 读取
    ('bk_set_name', 'CMDB集群名称', 'subject', 'text', 1, 15, 15, 'BK Set Name', '', NULL),  -- 拓扑可返回标量或列表，按有效 labels.bk_set_name / extra_data.bk_set_name 读取
    ('bk_module_id', 'CMDB模块ID', 'subject', 'text', 0, 16, 16, 'BK Module ID', '', NULL),  -- 拓扑可返回标量或列表，按有效 labels.bk_module_id / extra_data.bk_module_id 读取
    ('bk_module_name', 'CMDB模块名称', 'subject', 'text', 1, 17, 17, 'BK Module Name', '', NULL),  -- 拓扑可返回标量或列表，按有效 labels.bk_module_name / extra_data.bk_module_name 读取
    ('bk_cloud_id', 'CMDB云区域ID', 'subject', 'text', 0, 18, 18, 'BK Cloud ID', '', '$["labels"]["bk_cloud_id"]'),
    ('bk_obj_id', 'CMDB模型ID', 'subject', 'text', 1, 19, 19, 'BK Object ID', '', '$["labels"]["bk_obj_id"]'),
    ('bk_inst_id', 'CMDB模型实例ID', 'subject', 'text', 1, 20, 20, 'BK Instance ID', '', '$["labels"]["bk_inst_id"]'),
    ('dimension_info', '维度信息', 'subject', 'text', 1, 23, 23, 'Dimension', '', NULL),  -- 显式 extra_data.dimension_info 或展示丰富的 dimension_text
    ('dimension', '维度', 'subject', 'text', 0, 69, 69, '', '', '$["dimensions"]'),
    ('model_inst_id', '对象模型实例ID', 'subject', 'text', 1, 24, 24, 'Model Instance ID', '', '$["labels"]["model_inst_id"]'),
    ('model_id', '对象模型ID', 'subject', 'text', 0, 25, 25, 'Model ID', '', '$["labels"]["model_id"]'),
    ('model_name', '对象模型名称', 'subject', 'text', 1, 26, 26, 'Model Name', '', '$["labels"]["model_name"]'),
    ('bk_cloud_name', 'CMDB云区域名称', 'subject', 'text', 1, 27, 27, 'BK Cloud Name', '', '$["labels"]["bk_cloud_name"]'),
    ('bk_target_ip', '目标IP', 'subject', 'text', 1, 70, 70, '', '', '$["dimensions"]["bk_target_ip"]'),
    ('log_query_string', '检索语句', 'subject', 'text', 1, 71, 71, '', '', '$["labels"]["log_query_string"]'),
    ('log_theme_id', '日志主题id', 'subject', 'text', 1, 43, 43, '日志主题id', '', '$["labels"]["log_theme_id"]'),
    ('log_theme_name', '日志主题名称', 'subject', 'text', 1, 44, 44, '日志主题名称', '', '$["labels"]["log_theme_name"]'),
    ('log_relate_info', '日志关联信息', 'subject', 'text', 1, 45, 45, '日志关联信息', '', '$["labels"]["log_relate_info"]'),
    ('cloud_plat_id', '云平台id', 'subject', 'text', 1, 46, 46, '云平台id', '', '$["labels"]["cloud_plat_id"]'),
    ('cloud_plat_name', '云平台名称', 'subject', 'text', 1, 72, 72, '', '', '$["labels"]["cloud_plat_name"]'),
    ('apm_app_name', 'apm应用名称', 'subject', 'text', 1, 47, 47, 'apm应用名称', '', '$["labels"]["apm_app_name"]'),
    ('apm_app_id', 'apm应用ID', 'subject', 'text', 1, 48, 48, 'apm应用ID', '', '$["labels"]["apm_app_id"]'),
    ('apm_app_alias', 'apm应用别名', 'subject', 'text', 1, 49, 49, 'apm应用别名', '', '$["labels"]["apm_app_alias"]'),
    ('apm_service_name', 'apm服务名称', 'subject', 'text', 1, 53, 53, 'apm服务名称', '', '$["labels"]["apm_service_name"]'),
    ('app_service_alias', 'apm服务别名', 'subject', 'text', 1, 73, 73, '', '', '$["labels"]["app_service_alias"]'),
    ('cluster_name', '集群名称', 'subject', 'text', 1, 54, 54, '集群名称', '', '$["labels"]["cluster_name"]'),
    ('namespace', '命名空间', 'subject', 'text', 1, 55, 55, '命名空间', '', '$["labels"]["namespace"]'),
    ('workload_name', '工作负载名称', 'subject', 'text', 1, 61, 61, '工作负载名称', '', '$["labels"]["workload_name"]'),
    ('notify_status', '通知状态', 'extra', 'text', 0, 0, 0, 'Notify Status', '', NULL),  -- KAC 通知流程维护
    ('source_id', '告警源ID', 'extra', 'text', 0, 2, 2, 'Alert Source ID', '', '$["labels"]["source_id"]'),
    ('source_name', '告警源名称', 'extra', 'text', 1, 3, 3, 'Alert Source Name', '', '$["labels"]["source_name"]'),
    ('action', '告警动作', 'extra', 'text', 0, 9, 9, 'Action', '', NULL),  -- Event 判定或 Alert 状态转换
    ('meta_info', '告警元信息', 'extra', 'text', 1, 22, 22, 'Meta Info', '', NULL),  -- 显式 extra_data.meta_info、来源丰富或 source_event_id
    ('close_reason', '告警关闭原因', 'extra', 'text', 1, 31, 31, 'Close Reason', '', NULL),  -- 生命周期 Alert.end_reason
    ('close_time', '告警关闭时间', 'extra', 'time', 1, 32, 32, 'Close Time', '', NULL),  -- 生命周期 Alert.end_at
    ('storage_time', '告警入库时间', 'extra', 'time', 1, 33, 33, 'Storage Time', '', NULL),  -- 兼容文档首次入库时间
    ('alarm_details_link', '告警详情链接', 'extra', 'link', 1, 34, 34, 'Details Link', '', NULL),  -- KAC 根据告警 ID 生成链接
    ('associate_count', '告警计数', 'extra', 'number', 1, 35, 35, 'Count', '', NULL),  -- 按合并成员关系计算
    ('conductor', '当前处理人', 'extra', 'user', 1, 36, 36, 'Owner', '', NULL),  -- KAC 处置流程维护
    ('duration', '告警持续时间', 'extra', 'number', 1, 37, 37, 'Duration', '', NULL),  -- KAC 按告警时间与关闭或当前时间计算
    ('data_source', '数据来源', 'extra', 'text', 1, 41, 41, '数据来源', '', '$["labels"]["data_source"]');

-- 分阶段记录依据本目录开发设计及 Linkd 435f6a3a、Kingeye a031ad42cb。
-- stage / usage 均为 5.4 发布设计，现有 Go 函数提供行为依据，未接入按表分派。
-- “5.4 已定”表示已确认的取值；“建议”标识尚未约定的具体用法；“待定”只留注释。
-- method=builtin 时 path 为 NULL，Linkd 按 stage、usage 和 KAC 字段 key 选择内置处理逻辑。
CREATE TEMPORARY TABLE seed_kac_mapping (
    field_key VARCHAR(64) NOT NULL,
    stage VARCHAR(32) NOT NULL,
    `usage` VARCHAR(32) NOT NULL,
    direction VARCHAR(8) NOT NULL,
    method VARCHAR(16) NOT NULL,
    path VARCHAR(2048),
    PRIMARY KEY (field_key, stage, `usage`)
);

-- RawEvent 路径来自 Kingeye alarm_collect/linkd/source_rules.py 的 standard_field。
-- condition 指过滤字段，assignment 指来源规则的目标；保留调用方已有的值转换及 Cleaner 路径语法。
-- 对来源显式提供的扩展只登记其来源值；进入 Event 后的丰富输出另有记录。
-- item_id、dimension、bk_target_ip 的取值依照 5.4；建议发布时将这三个 KAC key 编译到下列标准输入路径。
INSERT INTO seed_kac_mapping (field_key, stage, `usage`, direction, method, path) VALUES
    ('name', 'source_clean', 'condition', 'read', 'path', '$.title'),
    ('name', 'source_clean', 'assignment', 'write', 'path', '$.title'),
    ('alarm_time', 'source_clean', 'condition', 'read', 'path', '$.occurred_at'),
    ('alarm_time', 'source_clean', 'assignment', 'write', 'path', '$.occurred_at'),
    ('level', 'source_clean', 'condition', 'read', 'path', '$.evaluations[0].severity'),
    ('level', 'source_clean', 'assignment', 'write', 'path', '$.evaluations[0].severity'),
    ('action', 'source_clean', 'condition', 'read', 'path', '$.evaluations[0].action'),  -- 沿用来源规则的动作值翻译；下标仅适用于该接入规则。
    ('action', 'source_clean', 'assignment', 'write', 'path', '$.evaluations[0].action'),  -- 沿用来源规则的动作值翻译；下标仅适用于该接入规则。
    ('object', 'source_clean', 'condition', 'read', 'path', '$.subject.name'),
    ('object', 'source_clean', 'assignment', 'write', 'path', '$.subject.name'),
    ('content', 'source_clean', 'condition', 'read', 'path', '$.content'),
    ('content', 'source_clean', 'assignment', 'write', 'path', '$.content'),
    ('fingerprint', 'source_clean', 'condition', 'read', 'path', '$.fingerprint'),
    ('fingerprint', 'source_clean', 'assignment', 'write', 'path', '$.fingerprint'),
    ('item_id', 'source_clean', 'condition', 'read', 'path', '$.labels.strategy_id'),  -- 5.4 已定。本次来源事件的监控项 ID。
    ('item_id', 'source_clean', 'assignment', 'write', 'path', '$.labels.strategy_id'),  -- 5.4 已定。本次来源事件的监控项 ID。
    ('dimension', 'source_clean', 'condition', 'read', 'path', '$.dimensions'),  -- 5.4 已定。保留维度对象。
    ('dimension', 'source_clean', 'assignment', 'write', 'path', '$.dimensions'),  -- 5.4 已定。保留维度对象。
    ('bk_target_ip', 'source_clean', 'condition', 'read', 'path', '$.dimensions.bk_target_ip'),  -- 5.4 已定。使用插件清洗得到的来源维度。
    ('bk_target_ip', 'source_clean', 'assignment', 'write', 'path', '$.dimensions.bk_target_ip'),  -- 5.4 已定。使用插件清洗得到的来源维度。
    ('strategy_id', 'source_clean', 'condition', 'read', 'path', '$.extra_data.strategy_id'),
    ('strategy_id', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.strategy_id'),
    ('meta_info', 'source_clean', 'condition', 'read', 'path', '$.extra_data.meta_info'),
    ('meta_info', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.meta_info'),
    ('dimension_info', 'source_clean', 'condition', 'read', 'path', '$.extra_data.dimension_info'),
    ('dimension_info', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.dimension_info'),
    ('bk_biz_id', 'source_clean', 'condition', 'read', 'path', '$.extra_data.bk_biz_id'),
    ('bk_biz_id', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.bk_biz_id'),
    ('bk_biz_name', 'source_clean', 'condition', 'read', 'path', '$.extra_data.bk_biz_name'),
    ('bk_biz_name', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.bk_biz_name'),
    ('bk_set_id', 'source_clean', 'condition', 'read', 'path', '$.extra_data.bk_set_id'),
    ('bk_set_id', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.bk_set_id'),
    ('bk_set_name', 'source_clean', 'condition', 'read', 'path', '$.extra_data.bk_set_name'),
    ('bk_set_name', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.bk_set_name'),
    ('bk_module_id', 'source_clean', 'condition', 'read', 'path', '$.extra_data.bk_module_id'),
    ('bk_module_id', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.bk_module_id'),
    ('bk_module_name', 'source_clean', 'condition', 'read', 'path', '$.extra_data.bk_module_name'),
    ('bk_module_name', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.bk_module_name'),
    ('bk_cloud_id', 'source_clean', 'condition', 'read', 'path', '$.extra_data.bk_cloud_id'),
    ('bk_cloud_id', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.bk_cloud_id'),
    ('bk_obj_id', 'source_clean', 'condition', 'read', 'path', '$.extra_data.bk_obj_id'),
    ('bk_obj_id', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.bk_obj_id'),
    ('bk_inst_id', 'source_clean', 'condition', 'read', 'path', '$.extra_data.bk_inst_id'),
    ('bk_inst_id', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.bk_inst_id'),
    ('model_inst_id', 'source_clean', 'condition', 'read', 'path', '$.extra_data.model_inst_id'),
    ('model_inst_id', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.model_inst_id'),
    ('model_id', 'source_clean', 'condition', 'read', 'path', '$.extra_data.model_id'),
    ('model_id', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.model_id'),
    ('model_name', 'source_clean', 'condition', 'read', 'path', '$.extra_data.model_name'),
    ('model_name', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.model_name'),
    ('bk_cloud_name', 'source_clean', 'condition', 'read', 'path', '$.extra_data.bk_cloud_name'),
    ('bk_cloud_name', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.bk_cloud_name'),
    ('log_query_string', 'source_clean', 'condition', 'read', 'path', '$.extra_data.log_query_string'),
    ('log_query_string', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.log_query_string'),
    ('log_theme_id', 'source_clean', 'condition', 'read', 'path', '$.extra_data.log_theme_id'),
    ('log_theme_id', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.log_theme_id'),
    ('log_theme_name', 'source_clean', 'condition', 'read', 'path', '$.extra_data.log_theme_name'),
    ('log_theme_name', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.log_theme_name'),
    ('log_relate_info', 'source_clean', 'condition', 'read', 'path', '$.extra_data.log_relate_info'),
    ('log_relate_info', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.log_relate_info'),
    ('apm_app_name', 'source_clean', 'condition', 'read', 'path', '$.extra_data.apm_app_name'),
    ('apm_app_name', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.apm_app_name'),
    ('apm_app_id', 'source_clean', 'condition', 'read', 'path', '$.extra_data.apm_app_id'),
    ('apm_app_id', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.apm_app_id'),
    ('apm_app_alias', 'source_clean', 'condition', 'read', 'path', '$.extra_data.apm_app_alias'),
    ('apm_app_alias', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.apm_app_alias'),
    ('apm_service_name', 'source_clean', 'condition', 'read', 'path', '$.extra_data.apm_service_name'),
    ('apm_service_name', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.apm_service_name'),
    ('cluster_name', 'source_clean', 'condition', 'read', 'path', '$.extra_data.cluster_name'),
    ('cluster_name', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.cluster_name'),
    ('namespace', 'source_clean', 'condition', 'read', 'path', '$.extra_data.namespace'),
    ('namespace', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.namespace'),
    ('workload_name', 'source_clean', 'condition', 'read', 'path', '$.extra_data.workload_name'),
    ('workload_name', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.workload_name'),
    ('source_id', 'source_clean', 'condition', 'read', 'path', '$.extra_data.source_id'),
    ('source_id', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.source_id'),
    ('source_name', 'source_clean', 'condition', 'read', 'path', '$.extra_data.source_name'),
    ('source_name', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.source_name'),
    ('data_source', 'source_clean', 'condition', 'read', 'path', '$.extra_data.data_source'),
    ('data_source', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.data_source'),
    ('strategy_name', 'source_clean', 'condition', 'read', 'path', '$.extra_data.strategy_name'),
    ('strategy_name', 'source_clean', 'assignment', 'write', 'path', '$.extra_data.strategy_name');

-- 告警源规则的 condition / assignment 分别对应 Kingeye kac/linkd/enrich.py 的 source_value / field_path。
-- 内置读取按当前 KAC key 求值。标识字段先 labels 后 extra_data；
-- 其他来源条件先 extra_data 后 labels，object 还可取 subject_name。仅实际配置的规则才执行赋值。
-- builtin_output 记录内置 Processor 的写入位置，避免和同一 key 的告警源 assignment 冲突。
-- builtin_output 描述 internal/enrich/kingeye/projection.go 的写值位置；新增输出见各行标注。
-- Processor 按来源配置调度一次。item 和 strategy_name 可引用同一次 strategy 丰富输出。
-- level、action、alarm_time 的告警源自定义丰富规则沿用 source_value / field_path 的同名扩展读写；
-- 策略使用的等级、动作和时间仍由 Event / Alert 提供。
INSERT INTO seed_kac_mapping (field_key, stage, `usage`, direction, method, path) VALUES
    ('name', 'enrich', 'builtin_output', 'write', 'path', '$.title'),
    ('content', 'enrich', 'builtin_output', 'write', 'path', '$.content'),
    ('object', 'enrich', 'builtin_output', 'write', 'path', '$.subject_name'),
    ('item', 'enrich', 'builtin_output', 'write', 'path', '$.labels.strategy_name'),  -- 5.4 已定。与 strategy_name 引用同一策略丰富结果。
    ('strategy_id', 'enrich', 'builtin_output', 'write', 'path', '$.labels.monitor_template_id'),  -- 策略输出 monitor_template_id。
    ('dimension_info', 'enrich', 'builtin_output', 'write', 'path', '$.labels.dimension_text'),
    ('strategy_name', 'enrich', 'builtin_output', 'write', 'path', '$.labels.strategy_name'),
    ('bk_biz_id', 'enrich', 'builtin_output', 'write', 'path', '$.labels.bk_biz_id'),
    ('bk_biz_name', 'enrich', 'builtin_output', 'write', 'path', '$.labels.bk_biz_name'),
    ('bk_cloud_id', 'enrich', 'builtin_output', 'write', 'path', '$.labels.bk_cloud_id'),
    ('bk_obj_id', 'enrich', 'builtin_output', 'write', 'path', '$.labels.bk_obj_id'),
    ('bk_inst_id', 'enrich', 'builtin_output', 'write', 'path', '$.labels.bk_inst_id'),
    ('model_inst_id', 'enrich', 'builtin_output', 'write', 'path', '$.labels.model_inst_id'),
    ('model_id', 'enrich', 'builtin_output', 'write', 'path', '$.labels.model_id'),
    ('model_name', 'enrich', 'builtin_output', 'write', 'path', '$.labels.model_name'),
    ('bk_cloud_name', 'enrich', 'builtin_output', 'write', 'path', '$.labels.bk_cloud_name'),
    ('log_query_string', 'enrich', 'builtin_output', 'write', 'path', '$.labels.log_query_string'),
    ('log_theme_id', 'enrich', 'builtin_output', 'write', 'path', '$.labels.log_theme_id'),
    ('log_theme_name', 'enrich', 'builtin_output', 'write', 'path', '$.labels.log_theme_name'),
    ('log_relate_info', 'enrich', 'builtin_output', 'write', 'path', '$.labels.log_relate_info'),
    ('cloud_plat_id', 'enrich', 'builtin_output', 'write', 'path', '$.labels.cloud_plat_id'),  -- cloud 已写 ResourceValues.CloudPlatformID。
    ('cloud_plat_name', 'enrich', 'builtin_output', 'write', 'path', '$.labels.cloud_plat_name'),  -- 5.4 已定、待实现。cloud 新增独立输出 CloudResource.CloudName。
    ('apm_app_name', 'enrich', 'builtin_output', 'write', 'path', '$.labels.apm_app_name'),
    ('apm_app_id', 'enrich', 'builtin_output', 'write', 'path', '$.labels.apm_app_id'),
    ('apm_app_alias', 'enrich', 'builtin_output', 'write', 'path', '$.labels.apm_app_alias'),
    ('apm_service_name', 'enrich', 'builtin_output', 'write', 'path', '$.labels.apm_service_name'),
    ('app_service_alias', 'enrich', 'builtin_output', 'write', 'path', '$.labels.app_service_alias'),  -- 5.4 已定、待实现。apm 仅给服务实例和接口写所属服务实际别名。
    ('cluster_name', 'enrich', 'builtin_output', 'write', 'path', '$.labels.cluster_name'),
    ('namespace', 'enrich', 'builtin_output', 'write', 'path', '$.labels.namespace'),
    ('workload_name', 'enrich', 'builtin_output', 'write', 'path', '$.labels.workload_name'),
    ('source_id', 'enrich', 'builtin_output', 'write', 'path', '$.labels.source_id'),
    ('source_name', 'enrich', 'builtin_output', 'write', 'path', '$.labels.source_name'),
    ('data_source', 'enrich', 'builtin_output', 'write', 'path', '$.labels.data_source'),
    ('bk_set_id', 'enrich', 'builtin_output', 'write', 'builtin', NULL),  -- ProjectAssignments 按结果形态写 labels（标量）或 extra_data（列表）。
    ('bk_set_name', 'enrich', 'builtin_output', 'write', 'builtin', NULL),  -- ProjectAssignments 按结果形态写 labels（标量）或 extra_data（列表）。
    ('bk_module_id', 'enrich', 'builtin_output', 'write', 'builtin', NULL),  -- ProjectAssignments 按结果形态写 labels（标量）或 extra_data（列表）。
    ('bk_module_name', 'enrich', 'builtin_output', 'write', 'builtin', NULL),  -- ProjectAssignments 按结果形态写 labels（标量）或 extra_data（列表）。
    ('meta_info', 'enrich', 'builtin_output', 'write', 'path', '$.labels.meta_info'),
    ('name', 'enrich', 'condition', 'read', 'path', '$.title'),
    ('name', 'enrich', 'assignment', 'write', 'path', '$.title'),
    ('content', 'enrich', 'condition', 'read', 'path', '$.content'),
    ('content', 'enrich', 'assignment', 'write', 'path', '$.content'),
    ('fingerprint', 'enrich', 'condition', 'read', 'path', '$.fingerprint'),
    ('object', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('object', 'enrich', 'assignment', 'write', 'path', '$.extra_data.object'),
    ('source_alarm_status', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('source_id', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('strategy_id', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('strategy_id', 'enrich', 'assignment', 'write', 'path', '$.extra_data.strategy_id'),
    ('meta_info', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('meta_info', 'enrich', 'assignment', 'write', 'path', '$.extra_data.meta_info'),
    ('dimension_info', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('dimension_info', 'enrich', 'assignment', 'write', 'path', '$.extra_data.dimension_info'),
    ('bk_biz_id', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('bk_biz_id', 'enrich', 'assignment', 'write', 'path', '$.extra_data.bk_biz_id'),
    ('bk_biz_name', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('bk_biz_name', 'enrich', 'assignment', 'write', 'path', '$.extra_data.bk_biz_name'),
    ('bk_set_id', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('bk_set_id', 'enrich', 'assignment', 'write', 'path', '$.extra_data.bk_set_id'),
    ('bk_set_name', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('bk_set_name', 'enrich', 'assignment', 'write', 'path', '$.extra_data.bk_set_name'),
    ('bk_module_id', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('bk_module_id', 'enrich', 'assignment', 'write', 'path', '$.extra_data.bk_module_id'),
    ('bk_module_name', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('bk_module_name', 'enrich', 'assignment', 'write', 'path', '$.extra_data.bk_module_name'),
    ('bk_cloud_id', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('bk_cloud_id', 'enrich', 'assignment', 'write', 'path', '$.extra_data.bk_cloud_id'),
    ('bk_obj_id', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('bk_obj_id', 'enrich', 'assignment', 'write', 'path', '$.labels.bk_obj_id'),
    ('bk_inst_id', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('bk_inst_id', 'enrich', 'assignment', 'write', 'path', '$.labels.bk_inst_id'),
    ('model_inst_id', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('model_inst_id', 'enrich', 'assignment', 'write', 'path', '$.labels.model_inst_id'),
    ('model_id', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('model_id', 'enrich', 'assignment', 'write', 'path', '$.labels.model_id'),
    ('model_name', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('model_name', 'enrich', 'assignment', 'write', 'path', '$.labels.model_name'),
    ('bk_cloud_name', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('bk_cloud_name', 'enrich', 'assignment', 'write', 'path', '$.extra_data.bk_cloud_name'),
    ('log_query_string', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('log_query_string', 'enrich', 'assignment', 'write', 'path', '$.extra_data.log_query_string'),
    ('log_theme_id', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('log_theme_id', 'enrich', 'assignment', 'write', 'path', '$.extra_data.log_theme_id'),
    ('log_theme_name', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('log_theme_name', 'enrich', 'assignment', 'write', 'path', '$.extra_data.log_theme_name'),
    ('log_relate_info', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('log_relate_info', 'enrich', 'assignment', 'write', 'path', '$.extra_data.log_relate_info'),
    ('apm_app_name', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('apm_app_name', 'enrich', 'assignment', 'write', 'path', '$.extra_data.apm_app_name'),
    ('apm_app_id', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('apm_app_id', 'enrich', 'assignment', 'write', 'path', '$.extra_data.apm_app_id'),
    ('apm_app_alias', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('apm_app_alias', 'enrich', 'assignment', 'write', 'path', '$.extra_data.apm_app_alias'),
    ('apm_service_name', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('apm_service_name', 'enrich', 'assignment', 'write', 'path', '$.extra_data.apm_service_name'),
    ('cluster_name', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('cluster_name', 'enrich', 'assignment', 'write', 'path', '$.extra_data.cluster_name'),
    ('namespace', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('namespace', 'enrich', 'assignment', 'write', 'path', '$.extra_data.namespace'),
    ('workload_name', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('workload_name', 'enrich', 'assignment', 'write', 'path', '$.extra_data.workload_name'),
    ('source_name', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('source_name', 'enrich', 'assignment', 'write', 'path', '$.extra_data.source_name'),
    ('data_source', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('data_source', 'enrich', 'assignment', 'write', 'path', '$.extra_data.data_source'),
    ('strategy_name', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('strategy_name', 'enrich', 'assignment', 'write', 'path', '$.extra_data.strategy_name'),
    ('item', 'enrich', 'condition', 'read', 'path', '$.labels.strategy_name'),  -- 5.4 已定。取策略丰富名。
    ('bk_target_ip', 'enrich', 'condition', 'read', 'path', '$.dimensions.bk_target_ip'),  -- 5.4 按插件清洗结果读取。
    ('item_id', 'enrich', 'condition', 'read', 'builtin', NULL),  -- 建议取原始 Event.labels.strategy_id，避免前序丰富覆盖原值。
    ('dimension', 'enrich', 'condition', 'read', 'path', '$.dimensions'),  -- 5.4 已定。以原始维度对象作为来源。
    ('level', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('level', 'enrich', 'assignment', 'write', 'path', '$.extra_data.level'),
    ('action', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('action', 'enrich', 'assignment', 'write', 'path', '$.extra_data.action'),
    ('alarm_time', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('alarm_time', 'enrich', 'assignment', 'write', 'path', '$.extra_data.alarm_time'),
    ('cloud_plat_id', 'enrich', 'condition', 'read', 'builtin', NULL),
    ('cloud_plat_id', 'enrich', 'assignment', 'write', 'path', '$.extra_data.cloud_plat_id'),
    ('cloud_plat_name', 'enrich', 'condition', 'read', 'builtin', NULL),  -- 5.4 待补上游云平台名称输出。
    ('app_service_alias', 'enrich', 'condition', 'read', 'builtin', NULL);  -- 5.4 待补上游别名输出。

-- policy_event / policy_alert 对应 internal/policy/view.go 的 EventView / AlertView。
-- 内置条件按策略阶段和 KAC 字段 key 求值。现行 KACFields 的同名读取通常先 labels 后 extra_data；
-- object、meta_info、strategy_id、dimension_info、bk_biz_id 另沿用 Field 的专门取值顺序。
-- 对非 KACFields 的既有同名目录键，沿用 Kingeye kac/linkd/configuration.py 的 extra_data 后 labels。
-- 本节 condition 供条件及使用相同读取器的聚合、模板字段引用。
INSERT INTO seed_kac_mapping (field_key, stage, `usage`, direction, method, path) VALUES
    ('item_id', 'policy_event', 'condition', 'read', 'builtin', NULL),  -- 建议在此策略阶段读取本次原始 Event的原始 labels.strategy_id，避免读到丰富改写值。
    ('metric_id', 'policy_event', 'condition', 'read', 'builtin', NULL),  -- 建议按本次原始 Event 对应的策略版本取值。单指标取全名，多指标将表达式变量替换为各自的指标全名。
    ('name', 'policy_event', 'condition', 'read', 'path', '$.title'),
    ('content', 'policy_event', 'condition', 'read', 'path', '$.content'),
    ('object', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('item', 'policy_event', 'condition', 'read', 'path', '$.labels.strategy_name'),  -- 5.4 已定。用策略丰富名替换旧 item 优先顺序。
    ('bk_set_name', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('bk_module_name', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('bk_cloud_name', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('meta_info', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('strategy_id', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('dimension_info', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('model_id', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('model_inst_id', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('model_name', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('source_id', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('source_name', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('level', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('action', 'policy_event', 'condition', 'read', 'builtin', NULL),  -- 当前 evaluation 动作转 KAC 动作。
    ('bk_biz_name', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('bk_set_id', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('bk_module_id', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('bk_obj_id', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('bk_inst_id', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('source_alarm_status', 'policy_event', 'condition', 'read', 'builtin', NULL),  -- 策略仅读来源同名值。投递才按 Alert 状态和结束类型换算。
    ('fingerprint', 'policy_event', 'condition', 'read', 'path', '$.fingerprint'),
    ('bk_biz_id', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('bk_cloud_id', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('duration', 'policy_event', 'condition', 'read', 'builtin', NULL),  -- 现有策略仅读来源提供的同名值。详情由 KAC 计算持续时间。
    ('alarm_time', 'policy_event', 'condition', 'read', 'builtin', NULL),  -- Event.OccurredAt，格式化上海时间。
    ('strategy_name', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('log_query_string', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('log_theme_id', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('log_theme_name', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('log_relate_info', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('apm_app_name', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('apm_app_id', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('apm_app_alias', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('apm_service_name', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('cluster_name', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('namespace', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('workload_name', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('data_source', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('cloud_plat_id', 'policy_event', 'condition', 'read', 'builtin', NULL),
    ('cloud_plat_name', 'policy_event', 'condition', 'read', 'path', '$.labels.cloud_plat_name'),  -- 建议按新增 cloud 丰富键提供策略条件。
    ('app_service_alias', 'policy_event', 'condition', 'read', 'path', '$.labels.app_service_alias'),  -- 建议按新增 apm 别名提供策略条件。
    ('bk_target_ip', 'policy_event', 'condition', 'read', 'path', '$.dimensions.bk_target_ip');  -- 建议使用该视图的来源维度。

-- 已有 Alert 的策略条件以 opening Event 的冻结丰富结果和 Alert 当前事实求值。
INSERT INTO seed_kac_mapping (field_key, stage, `usage`, direction, method, path) VALUES
    ('item_id', 'policy_alert', 'condition', 'read', 'builtin', NULL),  -- 建议在此策略阶段读取该 Alert 的首次触发 Event的原始 labels.strategy_id，避免读到丰富改写值。
    ('metric_id', 'policy_alert', 'condition', 'read', 'builtin', NULL),  -- 建议按该 Alert 的首次触发 Event 对应的策略版本取值。单指标取全名，多指标将表达式变量替换为各自的指标全名。
    ('name', 'policy_alert', 'condition', 'read', 'path', '$.title'),
    ('content', 'policy_alert', 'condition', 'read', 'path', '$.content'),
    ('object', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('item', 'policy_alert', 'condition', 'read', 'path', '$.labels.strategy_name'),  -- 5.4 已定。用策略丰富名替换旧 item 优先顺序。
    ('bk_set_name', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('bk_module_name', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('bk_cloud_name', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('meta_info', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('strategy_id', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('dimension_info', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('model_id', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('model_inst_id', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('model_name', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('source_id', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('source_name', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('level', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('action', 'policy_alert', 'condition', 'read', 'builtin', NULL),  -- 按当前 Alert 状态转 KAC 动作。
    ('bk_biz_name', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('bk_set_id', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('bk_module_id', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('bk_obj_id', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('bk_inst_id', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('source_alarm_status', 'policy_alert', 'condition', 'read', 'builtin', NULL),  -- 策略仅读来源同名值。投递才按 Alert 状态和结束类型换算。
    ('fingerprint', 'policy_alert', 'condition', 'read', 'path', '$.fingerprint'),
    ('bk_biz_id', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('bk_cloud_id', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('duration', 'policy_alert', 'condition', 'read', 'builtin', NULL),  -- 现有策略仅读来源提供的同名值。详情由 KAC 计算持续时间。
    ('alarm_time', 'policy_alert', 'condition', 'read', 'builtin', NULL),  -- Alert.BeginAt，格式化上海时间。
    ('strategy_name', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('log_query_string', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('log_theme_id', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('log_theme_name', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('log_relate_info', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('apm_app_name', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('apm_app_id', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('apm_app_alias', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('apm_service_name', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('cluster_name', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('namespace', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('workload_name', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('data_source', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('cloud_plat_id', 'policy_alert', 'condition', 'read', 'builtin', NULL),
    ('cloud_plat_name', 'policy_alert', 'condition', 'read', 'path', '$.labels.cloud_plat_name'),  -- 建议按新增 cloud 丰富键提供策略条件。
    ('app_service_alias', 'policy_alert', 'condition', 'read', 'path', '$.labels.app_service_alias'),  -- 建议按新增 apm 别名提供策略条件。
    ('bk_target_ip', 'policy_alert', 'condition', 'read', 'path', '$.dimensions.bk_target_ip');  -- 建议使用该视图的来源维度。

-- 原始 Event 详情。关联键为 Event ID，原始动作、等级与策略匹配的 KAC 枚举分别处理。
INSERT INTO seed_kac_mapping (field_key, stage, `usage`, direction, method, path) VALUES
    ('name', 'event_detail', 'detail', 'read', 'path', '$.title'),  -- 按原始 Event 读取。
    ('content', 'event_detail', 'detail', 'read', 'path', '$.content'),  -- 按原始 Event 读取。
    ('latest_alarm_time', 'event_detail', 'detail', 'read', 'path', '$.occurred_at'),  -- 5.4 已定。原始行取本次发生时间，按页面格式展示。
    ('processing_status', 'event_detail', 'detail', 'read', 'builtin', NULL),  -- 5.4 已定。复用同一 stored Event 的 Processing.State。
    ('object', 'event_detail', 'detail', 'read', 'builtin', NULL),  -- 建议取原始 Event.subject_name，再取 subject_id。
    ('level', 'event_detail', 'detail', 'read', 'builtin', NULL),  -- 5.4 详情展示来源等级。多判定时建议按相应 evaluation 展示。
    ('action', 'event_detail', 'detail', 'read', 'builtin', NULL),  -- 5.4 详情保留来源动作。多判定时建议与 level 取同一 evaluation。
    ('source_name', 'event_detail', 'detail', 'read', 'builtin', NULL),  -- 建议用本次 Event 绑定的来源版本解析展示名称。
    ('dimension', 'event_detail', 'detail', 'read', 'path', '$.dimensions'),  -- 建议在详情按原始对象返回维度。
    ('item_id', 'event_detail', 'detail', 'read', 'path', '$.labels.strategy_id'),  -- 建议在详情复用已确认的原始监控项 ID。
    ('bk_target_ip', 'event_detail', 'detail', 'read', 'path', '$.dimensions.bk_target_ip');  -- 建议在详情直接读来源维度。

-- KAC 兼容文档输出。internal/lifecycle/kachook/message.go、compatibility.go 与 internal/kaccompat/writer.go
-- 分别负责有效值解码、兼容字段转换和新建/局部更新。普通丰富字段复用 decodeEffectiveEnrich
-- 的 labels 优先、extra_data 次之与字段类型转换；拓扑字段保留列表，namespace 保留小写协议名。
-- 映射不改变新建/更新的字段所有权，5.4 指定随事件更新的字段须同步接入 writer 更新分支。
-- bk_target_ip 的固定投影仍待定；建议在来源提供时投影对应 Event.dimensions.bk_target_ip，待协议确定后登记。latest_alarm_time 和 processing_status 只用于原始事件。
-- conductor、notify_status 由 KAC 维护，writer 仅为新文档初始化。alarm_details_link 由 KAC 生成；
-- duration 在 KAC 详情计算。上述四个字段没有 Linkd 投递取值规则。
INSERT INTO seed_kac_mapping (field_key, stage, `usage`, direction, method, path) VALUES
    ('alarm_id', 'kac_projection', 'output', 'read', 'path', '$.alert_id'),  -- 兼容文档采用原生 Alert ID。
    ('item_id', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 5.4 已定、待接入。取投递对应原始 Event.labels.strategy_id。
    ('item', 'kac_projection', 'output', 'read', 'path', '$.labels.strategy_name'),  -- 5.4 已定、待调整。仅取策略丰富名。
    ('name', 'kac_projection', 'output', 'read', 'path', '$.title'),
    ('fingerprint', 'kac_projection', 'output', 'read', 'path', '$.fingerprint'),
    ('alarm_time', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 将 Alert.begin_at 格式化为上海时间。
    ('content', 'kac_projection', 'output', 'read', 'path', '$.content'),
    ('level', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 将当前 Alert.severity 按配置映射为 KAC 等级。
    ('metric_id', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 5.4 已定。单指标取指标全名，多指标按表达式替换变量。例如 A+B 输出 system.cpu_summary.usage+system.cpu_summary.idle。
    ('strategy_id', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 显式 extra_data.strategy_id 优先，否则取丰富结果 monitor_template_id 并按文本协议输出。
    ('strategy_name', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('status', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 复用 writer 的状态合并，普通 active 保留 KAC 处置。
    ('source_alarm_status', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 区分来源关闭、人工关闭与恢复。
    ('tag_info', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 保留 KAC 已有标签并合入策略标签。
    ('object', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 显式 extra_data.object（含空串）优先，否则取 subject_name，再取 subject_id。
    ('bk_biz_id', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 从有效丰富值取 ID，沿用 NormalizeIntegerFields 的整数与空值转换。
    ('bk_biz_name', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('bk_set_id', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('bk_set_name', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('bk_module_id', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('bk_module_name', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('bk_cloud_id', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 从有效丰富值取 ID，沿用 NormalizeIntegerFields 的整数与空值转换。
    ('bk_obj_id', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('bk_inst_id', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('dimension_info', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 显式 extra_data.dimension_info 优先，否则取展示丰富的 dimension_text 拼接字符串。
    ('dimension', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 5.4 已定、待接入。取本次对应原始 Event.dimensions，保留 JSON 原类型，随事件更新。
    ('model_inst_id', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 数值来源实例 ID 转字符串，保留原精度。
    ('model_id', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('model_name', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('bk_cloud_name', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('log_query_string', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('log_theme_id', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 按固定 AlarmMessage 转为 int64。
    ('log_theme_name', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('log_relate_info', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('cloud_plat_id', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 5.4 已定、待接线。将 CloudPlatformID 按固定消息的字符串协议输出。
    ('cloud_plat_name', 'kac_projection', 'output', 'read', 'path', '$.labels.cloud_plat_name'),  -- 5.4 已定、待补 cloud 输出及投递。
    ('apm_app_name', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('apm_app_id', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 按固定 AlarmMessage 转为 int64。
    ('apm_app_alias', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('apm_service_name', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('app_service_alias', 'kac_projection', 'output', 'read', 'path', '$.labels.app_service_alias'),  -- 5.4 已定、待补 apm 输出及投递。只存实际别名；所属服务无别名时页面只显示服务名称。
    ('cluster_name', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('namespace', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('workload_name', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('source_id', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 来源丰富 SourceID 为空时取 Alert.event_source_id。
    ('source_name', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 来源丰富 SourceName 为空时取 Alert.event_source_id。
    ('action', 'kac_projection', 'output', 'read', 'builtin', NULL),
    ('meta_info', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 显式 extra_data.meta_info 优先，否则取来源丰富值，再取 source_event_id。
    ('close_reason', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 按现有 writer 行为返回 EndReason（含空串）。
    ('close_time', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- EndAt 存在时格式化，否则写 null。
    ('storage_time', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 仅新建文档取写入时间。
    ('associate_count', 'kac_projection', 'output', 'read', 'builtin', NULL),  -- 按合并关系对成员 Alert ID 去重计数。
    ('data_source', 'kac_projection', 'output', 'read', 'builtin', NULL);

-- 初次登记使用同一时点与操作者。主键由目标数据库分配。
START TRANSACTION;
SET @field_seed_time = CURRENT_TIMESTAMP(6);

INSERT INTO alert_field
    (bk_tenant_id, field, field_digest, name, category, source_type, value_type,
     created_by, updated_by, created_at, updated_at)
SELECT @bk_tenant_id, path, UNHEX(SHA2(path, 256)), name, category, 'built_in', value_type,
       COALESCE(@maintainer, ''), COALESCE(@maintainer, ''), @field_seed_time, @field_seed_time
FROM seed_standard_field;

INSERT INTO kac_alert_field
    (bk_tenant_id, `key`, key_display, alias_en, alias_zh_hant, alert_field_id,
     category, display_category, source_type, source_info, type, is_display, `order`, option_order,
     created_by, updated_by, created_at, updated_at)
SELECT @bk_tenant_id, k.`key`, k.key_display, k.alias_en, k.alias_zh_hant, a.id,
       k.category, k.category, 'built_in', JSON_OBJECT(), k.type, k.is_display, k.`order`, k.option_order,
       COALESCE(@maintainer, ''), COALESCE(@maintainer, ''), @field_seed_time, @field_seed_time
FROM seed_kac_field AS k
LEFT JOIN alert_field AS a
  ON a.bk_tenant_id = @bk_tenant_id
 AND a.field_digest = UNHEX(SHA2(k.standard_path, 256));

INSERT INTO kac_alert_field_mapping
    (bk_tenant_id, kac_alert_field_id, stage, `usage`, direction, method, path,
     created_by, updated_by, created_at, updated_at)
SELECT @bk_tenant_id, k.id, m.stage, m.`usage`, m.direction, m.method, m.path,
       COALESCE(@maintainer, ''), COALESCE(@maintainer, ''), @field_seed_time, @field_seed_time
FROM seed_kac_mapping AS m
JOIN kac_alert_field AS k ON k.bk_tenant_id = @bk_tenant_id AND k.`key` = m.field_key;

COMMIT;

DROP TEMPORARY TABLE seed_kac_mapping, seed_kac_field, seed_standard_field;
