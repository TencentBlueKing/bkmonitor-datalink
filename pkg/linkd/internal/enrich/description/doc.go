// Package description 在创建 Alert 时读取精确版本配置并渲染冻结的检测事实。
// Resolver 经只读端口取得配置，Renderer 不执行外部 I/O 或解析来源 content。
// 模板基线为 bk-monitor 51c834dc6；无数据按 alarmd 已冻结的周期数陈述，
// 不补算旧 checkpoint 的延时。未实现的算法明确返回错误。
package description
