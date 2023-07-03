### 功能
路由模块，http模块向其发起请求，由该模块处理路由及写入工作

### 目录
influxql 文件夹，存放对传入sql的解析逻辑
anaylize.go 存放对写入数据的解析逻辑
const.go 存放一些常量
define.go 存放一些定义
error.go 存放错误信息
execution.go 针对不同类型请求的处理逻辑
influxdata.go tag解析逻辑
manager.go 管理全局路由
metric.go 管理route相关指标
route.go 为其他模块提供路由接口
struct.go 传参定义
utils.go 工具方法

