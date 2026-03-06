# X-Claw Refactor Plan

## 背景与判断

这份计划基于对仓库主干代码的集中阅读而来，重点覆盖了以下运行时核心路径：

- `cmd/x-claw/main.go`
- `cmd/x-claw/internal/gateway/helpers.go`
- `pkg/agent/loop.go`
- `pkg/agent/context.go`
- `pkg/agent/instance.go`
- `pkg/tools/subagent.go`
- `pkg/tools/toolloop.go`
- `pkg/session/manager.go`
- `pkg/httpapi/console.go`
- `pkg/config/config.go`
- `pkg/providers/factory.go`
- `pkg/providers/fallback.go`
- `internal/core/provider/provider.go`
- `internal/core/ports/session_store.go`
- `internal/archcheck/archcheck_test.go`

结论很明确：当前代码库的主要问题不是“功能太多”，而是“少数编排层文件承担了过多职责”。

因此，本计划遵循奥卡姆剃刀原则，但这里的“精简”不是删功能，也不是做大规模重写，而是：

- 去掉不必要的耦合
- 收拢重复规则和重复装配逻辑
- 把超大对象拆成边界清晰的小组件
- 让未来改动尽量局部化、可测试、可回退

换句话说，本计划追求的是：

- 更少的跨层依赖
- 更少的隐式约定
- 更少的“改一处牵全身”
- 在功能不变前提下，逐步减少代码与认知复杂度

## 总目标

- 保持 CLI、Gateway、Tool、Session、Config 等外部行为稳定。
- 降低 `agent`、`tools`、`config`、`httpapi`、`channels` 之间的耦合。
- 消除少数“上帝对象”和“巨石文件”。
- 让系统从“少数编排中心承载全部复杂度”，演化为“明确边界 + 明确阶段 + 明确职责”的结构。
- 顺着现有 `internal/core` 迁移方向持续收口，而不是另起一套新架构。

## 当前主要复杂度热点

### 1. `agent` 编排层过于集中

当前 `pkg/agent` 本质上是整套运行时的编排核心，负责：

- 入站消息路由
- agent 选择
- session 历史加载与写回
- prompt 构建
- LLM 调用
- tool 调用循环
- handoff / subagent
- memory 注入与压缩
- trace / audit / token 统计
- plan mode 与长任务控制

问题不在于这些能力不需要，而在于它们现在主要聚在少数文件中：

- `pkg/agent/loop.go`
- `pkg/agent/context.go`
- `pkg/agent/instance.go`
- `pkg/agent/llm_iteration.go`
- `pkg/agent/run_pipeline_impl.go`

这会导致：

- 改一个点，容易波及不相干行为
- 单元测试只能围大块逻辑做验证，难以锁定局部语义
- 对新贡献者不友好，理解成本很高

### 2. `ContextBuilder` 明显是上帝对象

`pkg/agent/context.go` 当前同时承担：

- 系统提示词构建
- workspace / bootstrap 信息装载
- skill 信息装载
- history pruning
- provider 兼容性 history sanitization
- memory 检索与拼装
- prompt cache 相关逻辑

这意味着“上下文”这个概念内部其实混合了至少 4 类职责：

- 静态提示词
- 动态历史裁剪
- provider 兼容修正
- 记忆/检索拼接

这些职责适合组合，不适合继续塞在一个大对象里。

### 3. session / transcript 写入路径分散

当前会话相关写入并非总是通过统一语义入口完成。

用户消息、assistant tool-call turn、tool result、summary、active agent 切换等分别在不同路径落盘，核心实现分布在：

- `pkg/agent/run_pipeline_impl.go`
- `pkg/agent/llm_iteration.go`
- `pkg/session/manager.go`
- `pkg/session/tree.go`

这会带来两个风险：

- transcript 一致性依赖隐式约定
- 重构时很容易破坏 provider/tool transcript 兼容性

### 4. tool / subagent 编排是第二复杂度中心

`pkg/tools` 中并不只是“工具实现集合”，还承载大量编排逻辑，尤其是：

- `pkg/tools/toolcall_executor.go`
- `pkg/tools/toolloop.go`
- `pkg/tools/subagent.go`

其中同时叠加了：

- policy
- confirm
- trace
- hooks
- parallelism
- cancellation
- result contract
- orchestration limits

这块不适合贸然重写，只适合在保护测试下逐步拆分职责。

### 5. config 系统过大且知识分散

`pkg/config/config.go` 与 `pkg/config/defaults.go`、`pkg/config/migration.go` 一起承担了：

- schema
- JSON shape
- load/save
- defaults
- validation
- migration
- path / model / provider 解析

这意味着新增一个配置项，常常要同时理解多个层面。

### 6. Gateway / HTTP / Console 边界仍偏厚

当前网关启动与热重载装配主要在：

- `cmd/x-claw/internal/gateway/helpers.go`

HTTP 只读 console 则集中在：

- `pkg/httpapi/console.go`

这两块的问题类似：

- 启动与运行时治理混在一起
- transport、query、local I/O、path safety、streaming 混在一起
- 易于生长为新的“大文件中心” 

### 7. channels 仍然存在较重装配与重复样板

`pkg/channels/manager.go` 负责：

- 初始化 channel
- 注入媒体、占位符、owner 等能力
- 启动与停止
- HTTP handler 绑定
- 发送前 placeholder / typing / reaction 处理

这说明 channel 层仍有继续收敛为更清晰 adapter/manager 边界的空间。

## 已存在的正确信号

尽管复杂度不低，但仓库已经有很好的重构基础：

- `internal/core/provider/provider.go` 已经抽出了核心 `LLMProvider` port
- `internal/core/ports/session_store.go` 已经抽出了核心 `SessionStore` port
- `pkg/session/store.go`、`pkg/routing/*.go` 等地方已经开始使用 facade / alias 过渡
- `internal/archcheck/archcheck_test.go` 已经在做边界约束保护

这说明正确方向不是“推倒重来”，而是：

- 继续沿着 `internal/core` 下沉边界
- 逐步瘦身 `pkg/*` 编排层
- 让 `pkg/*` 更多变成 facade / adapter / composition 层

## 重构原则

### 1. 功能不变优先

任何重构都必须以“行为稳定”为第一优先级，尤其包括：

- CLI 命令行为
- HTTP 路由与鉴权行为
- session JSONL 结构
- tool call transcript 语义
- gateway reload 语义
- config 兼容性

### 2. 先收口边界，再迁移实现

不要一开始就大范围移动代码。

更稳妥的路径是：

- 先提炼清晰接口
- 再把实现从巨石文件中搬出去
- 最后删除兼容层与重复代码

### 3. 先处理编排层，再处理叶子模块

先动：

- `agent`
- `context`
- `session transcript`
- `tools orchestration`
- `gateway/httpapi`

后动：

- provider 细节
- channel 适配细节
- 末端工具实现细节

### 4. 先减耦合，再减代码量

很多时候，第一步重构不会立刻明显减少代码行数。

这没有问题。真正重要的是：

- 去除重复装配
- 去掉隐式约定
- 让每层只管自己的职责

只有边界清晰后，后续删除冗余代码才是安全的。

### 5. 不做无收益抽象

不为了“看起来更工程化”而新增大量接口和目录。

本计划拒绝：

- 没有痛点支撑的抽象层
- 纯粹为了文件行数好看而拆文件
- 没有迁移路线的 API 大改
- 框架式重写

## 长时程重构路线图

## Phase 0：建立护栏

### 目标

在开始大规模重构前，先把行为边界冻结住。

### 核心工作

- 补齐主干行为测试，优先覆盖高风险路径：
  - `pkg/agent/context.go`
  - `pkg/tools/toolcall_executor.go`
  - `pkg/session/manager.go`
  - `pkg/httpapi/notify.go`
  - `cmd/x-claw/internal/gateway/helpers.go`
- 扩展架构约束测试，继续保护：
  - `internal/core` 不能反向依赖应用层
  - `pkg/agent` 不直接吞入过多 infra 依赖
- 把“功能不变”定义成可执行的回归护栏，而不是口头要求

### 产出

- 一组高价值回归测试
- 更严格的架构守卫
- 后续重构可依赖的安全网

### 验收标准

- 不新增产品行为
- 测试明确描述已有行为而非“理想行为”
- 能覆盖 tool transcript、session replay、gateway reload 等关键风险点

## Phase 1：拆薄 `agent` 主执行链

### 目标

让 `AgentLoop` 从“巨石执行器”变成“总协调器”。

### 核心工作

把当前运行主链拆成显式阶段：

- route resolution
- run preparation
- llm iteration
- tool batch application
- run finalization

保留 `AgentLoop` 对外 facade，不改变对外调用方式。

### 重点文件

- `pkg/agent/loop.go`
- `pkg/agent/llm_iteration.go`
- `pkg/agent/run_pipeline_impl.go`

### 目标状态

- `loop.go` 主要负责 orchestration facade
- 迭代状态留在专用 runner 中
- 各阶段之间通过小而清晰的数据结构交互

### 验收标准

- 行为不变
- 主链阅读路径更短
- 单次运行的生命周期可以按阶段单独定位问题

## Phase 2：分解 `ContextBuilder`

### 目标

把 `pkg/agent/context.go` 从全能上下文对象，拆成可组合组件。

### 建议拆分方向

- `SystemPromptBuilder`
- `HistoryPruner`
- `ProviderHistorySanitizer`
- `MemoryAssembler`

### 优先顺序

先抽离 `ProviderHistorySanitizer`，因为：

- 逻辑相对独立
- 风险高但边界清晰
- 已有相关测试可直接保护

再逐步拆历史裁剪与记忆拼装。

### 重点文件

- `pkg/agent/context.go`
- `pkg/agent/context_test.go`
- `pkg/agent/compaction.go`

### 验收标准

- prompt 构建顺序不变
- history sanitization 行为不变
- memory 注入行为不变
- cache 失效逻辑不变

## Phase 3：统一 transcript / session 写入语义

### 目标

让所有会话写入都收敛到统一语义入口。

### 核心工作

引入统一的会话记录/追加器，例如：

- `ConversationAppender`
- 或 `SessionRecorder`

将以下写入统一收口：

- user message
- assistant tool-call turn
- tool output
- summary
- active agent switch
- history truncate / history set

### 重点文件

- `pkg/session/manager.go`
- `pkg/session/tree.go`
- `pkg/session/events.go`
- `pkg/agent/llm_iteration.go`
- `pkg/agent/run_pipeline_impl.go`

### 价值

- 减少“谁负责写历史”的隐式约定
- 降低 transcript 破坏风险
- 让 session tree/replay 更容易验证

### 验收标准

- session JSONL 结构稳定
- replay / switch leaf 行为稳定
- tool call history 不丢失、不串位

## Phase 4：收敛 agent 实例装配

### 目标

把 `NewAgentInstance` 从“大构造器”变成“配置归一化 + 依赖装配”两层。

### 核心工作

拆分为：

- 纯配置归一化层：解析默认值、fallback、workspace、memory vector、context pruning 等
- 纯依赖装配层：基于已归一化配置创建 registry、context builder、memory store、subagent manager

### 重点文件

- `pkg/agent/instance.go`

### 价值

- 降低初始化复杂度
- 降低修改配置时对构造器主流程的影响
- 让实例装配可以被更小粒度测试

### 验收标准

- 实例初始化行为完全一致
- 配置默认值与回退行为完全一致
- 初始化相关测试更易读、更聚焦

## Phase 5：声明式工具注册

### 目标

把共享工具注册从命令式堆叠，变成更清晰的声明式安装过程。

### 核心工作

- 为工具注册建立 installer 列表或安装表
- 收口：
  - 配置开关判断
  - secret 解析
  - 默认参数填充
  - registry 注册
- 限制未来继续在一个长函数里追加工具注册逻辑

### 重点文件

- `pkg/agent/tool_registration.go`
- `pkg/tools/registry.go`

### 价值

- 新增工具成本更低
- secret 解析与工具构造逻辑不再散落
- 注册过程更容易测试

### 验收标准

- 工具集合不变
- tool schema / availability 不变
- 配置开关语义不变

## Phase 6：拆解工具执行与子代理编排

### 目标

在不改变工具协议的前提下，给 `pkg/tools` 降低编排复杂度。

### 核心工作

对 `pkg/tools/subagent.go` 与工具执行器做职责拆分：

- task model
- scheduler
- runner
- event publisher
- result contract
- limits / policy assembly

保留现有对外 tool 接口，不改调用者语义。

### 重点文件

- `pkg/tools/subagent.go`
- `pkg/tools/toolcall_executor.go`
- `pkg/tools/toolloop.go`

### 风险控制

这是高风险区域，必须在 `Phase 0` 护栏充足后再动。

### 验收标准

- policy / confirm / trace / hooks / parallelism 行为不变
- cancellation 与 async callback 行为不变
- subagent result payload 结构不变

## Phase 7：配置系统瘦身

### 目标

让配置系统从“大文件 + 多重职责”变成一组稳定子模块。

### 核心工作

拆分职责为：

- schema
- defaults
- load/save
- validation
- migration
- resolver / derived values

### 重点文件

- `pkg/config/config.go`
- `pkg/config/defaults.go`
- `pkg/config/migration.go`

### 关键约束

对外必须保持：

- `LoadConfig`
- `SaveConfig`
- JSON 兼容契约
- 已有 migration 语义

### 验收标准

- 配置文件兼容性不破坏
- 默认值来源更集中
- 新增配置项时 blast radius 更小

## Phase 8：provider 装配简化

### 目标

降低 provider 选择与 fallback 编排的条件树复杂度。

### 核心工作

- 把 provider selection 从大 `switch` / 大条件树收敛为更清晰的表驱动或注册式逻辑
- 把 fallback 的候选解析、错误分类、冷却策略、执行驱动继续解耦
- 保持各 provider 实现本身稳定

### 重点文件

- `pkg/providers/factory.go`
- `pkg/providers/factory_provider.go`
- `pkg/providers/fallback.go`

### 验收标准

- provider 选择结果不变
- fallback 结果与重试语义不变
- 新 provider 接入成本下降

## Phase 9：网关与 HTTP 边界重组

### 目标

让 gateway / httpapi 从“厚装配层”变成“薄入口 + 可复用服务”。

### 核心工作

拆分：

- gateway bootstrap
- lifecycle
- reload controller
- HTTP route registration
- console query service
- console file access service
- stream / tail helper

### 重点文件

- `cmd/x-claw/internal/gateway/helpers.go`
- `pkg/httpapi/console.go`
- `pkg/httpapi/auth.go`
- `pkg/httpapi/notify.go`

### 价值

- transport 与 local I/O 分离
- reload 更容易理解和验证
- console handler 复杂度下降

### 验收标准

- HTTP API 路由不变
- 鉴权语义不变
- console 只读能力不变
- gateway reload 语义不变

## Phase 10：继续推进 `internal/core` 边界迁移

### 目标

让真正稳定的领域类型与端口继续沉到 `internal/core`，减少应用层互相直连。

### 核心工作

- 将成熟的核心协议、领域对象、端口接口下沉
- 保持 `pkg/*` 兼容 facade，逐步缩薄
- 为每次迁移补充架构测试，防止边界回流

### 重点文件

- `internal/core/*`
- `pkg/session/store.go`
- `pkg/routing/*.go`
- `pkg/providers/types.go`

### 验收标准

- 没有新的 import cycle
- `internal/core` 不反向依赖应用层
- facade 只保留必要兼容职责

## Phase 11：删除兼容债务与文档收尾

### 目标

在主要重构稳定后，真正兑现“代码更少”。

### 核心工作

- 删除过渡 helper
- 删除重复默认值
- 删除重复装配逻辑
- 删除已被新边界替代的 facade / alias
- 更新开发文档、架构说明、测试入口说明

### 验收标准

- 删除的是冗余，不是行为
- 文档与代码结构一致
- 新人能沿文档快速理解主干分层

## 各阶段统一验收规则

每个阶段都必须满足：

- 外部行为不变
- 可以独立合并
- 可以独立回滚
- 主要收益是降耦合，而不是引入更多抽象层
- 至少有对应的聚焦测试可证明没有破坏关键行为

## 推荐开工顺序

建议的实际施工顺序如下：

1. `Phase 0`：先补护栏
2. `Phase 1`：拆薄 `agent` 主链
3. `Phase 2`：拆 `ContextBuilder`
4. `Phase 3`：统一 transcript / session 写入
5. `Phase 4`：收敛 agent 实例装配
6. `Phase 5`：重整工具注册
7. `Phase 6`：拆解工具执行与 subagent 编排
8. `Phase 7`：配置系统瘦身
9. `Phase 8`：provider 装配简化
10. `Phase 9`：网关与 HTTP 边界重组
11. `Phase 10`：继续推进 `internal/core` 下沉
12. `Phase 11`：删除兼容债务并完善文档

这个顺序的原因是：

- 先切主复杂度中心
- 再切高风险数据面（session / transcript）
- 再处理装配层
- 最后处理边界迁移与债务删除

这比一开始就动 provider 或 channel 更稳妥，也更符合奥卡姆剃刀原则。

## 当前不做的事

本计划明确不包含以下内容：

- 在同一批次里顺便做产品设计改动
- 为了“现代化”而引入额外框架
- 无迁移策略的 repo 级重命名
- 只为追求文件更短而做机械拆分
- 把 `ref/*` 参考仓库当作本次重构对象

## 第一批落地建议

如果按本计划正式开工，建议第一批变更只做三件事：

- 建立/补强重构护栏测试
- 拆薄 `pkg/agent` 主执行链但不改行为
- 从 `pkg/agent/context.go` 中优先抽离 `ProviderHistorySanitizer`

这样做的收益是：

- 对主复杂度中心下第一刀
- 风险可控
- 对未来所有阶段都有正向复用价值

## 本文档的定位

本文档是长期重构总图，不是一次性大改清单。

后续实施时，建议再为每个 Phase 单独建立更细的执行说明，记录：

- 目标文件
- 不可破坏的行为
- 计划新增/复用测试
- 合并顺序
- 回退策略

只有这样，整个重构才能既“宏大”，又“稳”。
