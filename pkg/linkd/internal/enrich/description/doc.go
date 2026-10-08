// Package description 将已验证的检测事实渲染为 bk-monitor Event.description。
// 本包只处理不可变输入，不读取策略、历史点或来源 content，不执行外部 I/O。
// 模板基线为 bk-monitor 51c834dc6；未实现的算法明确返回错误。
package description
