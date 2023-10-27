### 功能
http服务模块，提供对外http接口，也负责监听/触发其他模块的更新


### 目录
auth 文件夹，存放influxdb-proxy的认证实现,目前只有基础认证
config.go  存放了一些固定的装饰器序列
const.go 存放一些常量
decorator 存放一些装饰器，用于在正式处理请求前的前置操作
errors.go 存放一些错误信息
handler.go 各种请求的具体处理逻辑
hook.go 存放一些触发逻辑
http.go 存放http服务的生成/重载/关闭逻辑，也存放了其他模块的初始化和重载逻辑
metric.go 存放一些http指标
rebalance.go 额外提供给cluster/routecluster的模块，负责重新均衡tag
utils.go 存放一些工具方法
watch.go 存放所有监听逻辑，监听会触发所有模块的的更新与reload

