# Java 代码风格

## 格式与注释

- 使用 `.editorconfig` 和 `.gitattributes`；Java 使用 4 空格缩进、CRLF 行尾，行宽 120。
- 包名使用 `com.tencent.bk.bkmonitor.rum.*`，按现有包职责组织代码。
- 不引入编译器警告；无法消除时使用具体的 `@SuppressWarnings` 并注释原因。
- 注释解释原因，JavaDoc 描述契约和意图，不重复类名或方法名。
- 用提前返回减少嵌套，不顺带修改无关代码和格式。

## 对象与接口

- 字段尽量 `final`，对象在构造完成后即可使用，不增加 `init()` / `setup()`。
- Map 键必须不可变；只在相等关系有明确含义时实现 `equals` / `hashCode`。
- 默认非空，可空值标注 `javax.annotation.Nullable`；公开 API 的可空返回值优先使用 `Optional`。
- `Optional` 只用于返回值，不作为参数或字段。
- 重复字面量提取为常量，重复业务逻辑集中实现。
- 依赖通过构造函数传入，不在构造函数里创建。
- 可序列化类声明 `serialVersionUID`；不直接使用 Java 序列化。

## 日志与类型

- 日志使用占位符，如 `LOG.info("key={}", key)`，禁止字符串拼接。
- 日志中需要额外构造的调试内容放在 `LOG.isDebugEnabled()` 判断内。
- Preconditions 使用格式参数，避免在参数中拼接字符串。
- 不使用 raw types；无法消除的 unchecked 警告需说明原因。
- 避免反射；跨模块加载、Flink `TypeExtractor` 或 JDK 特性检测需说明用途。
- 不为单个方法引入新依赖。复制许可允许的代码时保留归属和许可说明。

## 集合与数据处理

- 优先使用 `ArrayList` / `ArrayDeque`；合并对同一 Map 的重复查询。
- 需要初始化集合容量时说明依据。
- 优先非捕获 Lambda，能用方法引用时使用方法引用。
- Stream 只用于方法内的协调逻辑，不用于逐条记录的数据处理。
- `process`、`serde`、`sink`、`aggregation` 中按需要复用对象、使用基本类型及数组。
- 对可合并的分配、查找或调用使用 buffer / bundle / batch，避免每条记录重复构造。
- 避免自行创建线程或使用 `CompletableFuture`，优先已有执行抽象。

## 测试

- 使用 JUnit 5、AssertJ 和 Arrange-Act-Assert。
- 优先可复用的测试实现，避免 Mockito；不使用反射、PowerMock 或 Whitebox 测试私有逻辑。
- JUnit 方法不设置本地 timeout，由 CI 控制全局超时。
- 修改状态字段、key、算子 UID 或状态描述符时，验证恢复兼容性。
