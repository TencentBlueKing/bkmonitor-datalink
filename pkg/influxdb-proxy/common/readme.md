### 功能
公共依赖存放位置

### 目录
config.go 定义全局配置接口，实际是覆盖viper接口
const.go 定义一些常量，主要是viper的配置路径
define.go 存放接口定义
errors.go 存放错误定义
flowid.go 每个请求生成唯一的流id，通过这里生成
hook.go 存放一些触发逻辑
metric.go 存放一些全局指标
struct.go 存放一些实际类型，通常为具体参数类型
tags.go 存放一些针对tag逻辑的方法，放在这里的原因是cluster和transport会共用

