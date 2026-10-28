<!-- orchid-live-20261019 -->
## 2026-10-19 兰紫 GM Console 真实接入

新客户端 `gm-console/` 已接同源 Go v1 与 Hub，覆盖查询、八类背包、邮件、任务、成长/资产、自动福利、领域流水和公告/下载源/游戏连接。现有本服能力范围与余项见 [本轮接入与验收](GM_CONSOLE_INTEGRATION_20261019.md)。第一版 `gm-web/` 已封存，新打包入口使用 `gm-console/dist/`。新版已完成隔离数据库和浏览器接入验证；下文历史阶段描述保留作追溯。

# 基础调用设施

本表是[功能台账](FEATURE_LEDGER.md)中F01～F15的定义。新增功能先选择相应设施，再补领域规则、接口和页面；客户端不获得通用SQL执行能力。

<a id="f01"></a>
## F01 · 管理身份与请求边界

当前：私有监听、登录Key摘要或配置密码、Cookie会话、同源限制、CSRF与JSON请求校验。`ManagementHandler`只挂载v1及鉴权/状态端点，继承旧写接口不直接开放。Hub有自己的管理会话，GM与Hub的统一身份尚未完成。

调用：`POST /api/auth/login` → Cookie → `GET /api/v1/capabilities`获取能力与CSRF → 管理API；退出`POST /api/auth/logout`。相关源码：[managed.go](../gateway/internal/services/gmtool/managed.go)、[api_auth.go](../gateway/internal/services/gmtool/api_auth.go)、[api_v1_operations.go](../gateway/internal/services/gmtool/api_v1_operations.go)。多管理员、角色权限和设备凭据属于后续工作。

<a id="f02"></a>
## F02 · 版本化查询与页面请求

当前：`/api/v1/`返回`data/page/observedAt/requestId/error`；有界游标分页，浏览器取消旧请求、请求超时、主动刷新。`gm-console/src/api/console.ts`保存当前目标及读取范围，不以固定轮询更新所有玩家资料；切换角色取消旧范围请求。

源码：[api_v1.go](../gateway/internal/services/gmtool/api_v1.go)、[api_v1_directory.go](../gateway/internal/services/gmtool/api_v1_directory.go)、[console.ts](../gm-console/src/api/console.ts)。统一查询缓存与客户端离线目录仍待接入。

<a id="f03"></a>
## F03 · MariaDB投影与连接池

当前：目录、容器、任务、邮箱等按需读取专用投影；筛选与分页下推SQL。相同DSN复用连接池，每池最大8连接、2空闲，生命周期5分钟。账号/角色分片由现有`DatabasePlan`映射；注册库和存档库可能是不同池。

源码：[management/module.go](../gateway/internal/management/module.go)、[repository接口](../gateway/internal/modules/dnf/repository/)、[MySQL实现](../gateway/internal/modules/dnf/repository/mysql/)。这些是权威存档读路径，不是已实现的玩家快照缓存。

<a id="f04"></a>
## F04 · 领域动作与事务

当前：点券、金币、容量、金库、物品、任务、SP/TP、等级、职业分别有`adminops`动作，调用仓储Unit of Work，定点读写与行锁保护相关资产和记录。邮件复用邮件/附件事务，福利复用对应持久规则。

源码：[adminops](../gateway/internal/modules/dnf/adminops/)、[repository](../gateway/internal/modules/dnf/repository/)、[testbenefits](../gateway/internal/modules/dnf/testbenefits/)。副职业已完成隔离数据库与网页验收。持久战斗属性/负重、涉及物品的任务联合事务等需要补专门契约，不能用直接覆盖数据库代替。

<a id="f05"></a>
## F05 · 预览、幂等与永久回执

当前：大部分资产/成长操作按`*-operations/preview` → `*-operations` → GET历史；签名预览绑定目标、前后状态与必要PVF版本。请求使用`Idempotency-Key`；回执和业务写同事务。点券走`currency-adjustments`自身契约，邮件/福利走固定作业，不强行套成同一种路径。

源码：[api_v1_inventory_operations.go](../gateway/internal/services/gmtool/api_v1_inventory_operations.go)、[api_v1_currency.go](../gateway/internal/services/gmtool/api_v1_currency.go)、[api_v1_operations.go](../gateway/internal/services/gmtool/api_v1_operations.go)。响应丢失时以原请求读回，保留前后值、凭据标识、原因与请求编号。跨功能审计见F12。

<a id="f06"></a>
## F06 · PVF摘要、磁盘索引与小型定义缓存

当前：磁盘目录按文件定位；物品摘要、详情、任务、职业索引，以及成长/点数/金库小表按完整PVF摘要和解析版本使用。完整摘要列表保存在外部 GM；游戏侧外部模式只缓存512条摘要，并按需获取任务／职业定义。副职业小索引及类型／等级／经验操作已验证。

源码：[DiskArchive](../gateway/internal/platform/pvf/disk.go)、[items_index.go](../gateway/internal/services/gmtool/items_index.go)、[物品详情索引](../gateway/internal/services/gmtool/item_details_index.go)、[任务索引](../gateway/internal/services/gmtool/quest_index.go)、[职业索引](../gateway/internal/services/gmtool/profession_index.go)、[GMIndex](../gateway/cmd/gmindex/main.go)。GMIndex当前生成职业/副职业索引，其余离线索引仍有旧GM工具入口。索引热切换、统一构建作业与客户端分片分发待做。

<a id="f07"></a>
## F07 · 邮件、福利与持久批次

当前：`mail/preview`或`benefits/{id}/preview`建立固定名单和作业，外部持久队列在确认后通过`tasks/{id}/run`逐批推进；关闭网页后继续，按回执恢复。在线邮件先取完整会话快照。邮箱跨页清理使用独立`mailbox-cleanups`，固定名单、进度和回执持久保存。自动福利扫描有批量及超时，账号一次/角色一次共用永久去重。

源码：[api_v1_operations.go](../gateway/internal/services/gmtool/api_v1_operations.go)、[mailbox_cleanup.go](../gateway/internal/modules/dnf/adminops/mailbox_cleanup.go)、[management/module.go](../gateway/internal/management/module.go)。邮件持久队列和福利调度见[外部服务](GM_EXTERNAL_SERVICES.md)；通用作业优先级和公开快照作业待实现。

<a id="f08"></a>
## F08 · 玩家网关、更新Hub与公告

当前：玩家白名单代理；Hub提供`bootstrap.json`、包服务和私有`/admin/api/*`，包含源配置、检查、发布、公告及包上传。公告更新带`If-Match`和CSRF，发布结果进入签名数据。具体白名单/精确方法见[API目录](API_CATALOG.md)。

源码：[playergateway/main.go](../gateway/cmd/playergateway/main.go)、[launcherhub/admin.go](../gateway/internal/services/launcherhub/admin.go)、[launcherhub/hub.go](../gateway/internal/services/launcherhub/hub.go)。候选有此能力，仍需与当前发行版本和实际更新源配置对齐。

<a id="f09"></a>
## F09 · 游戏运行态适配

当前：固定配置的`POST /api/game/admin`，版本化JSON、Bearer凭据、5秒超时、拒绝重定向。普通响应上限64KiB，在线快照512KiB。管理模块使用`character_edit_status`、`account_edit_status`、`online_snapshot`、`catalog`；继承协议还保留活动/实例编码等适配字段，不能据此宣布新v1页面已支持。

源码：[adminapi/client.go](../gateway/internal/modules/dnf/adminapi/client.go)、[协议](../gateway/internal/modules/dnf/adminapi/protocol.go)、[账号编辑检查](../gateway/internal/services/gmtool/api_v1_quest_operations.go)。账号事务与游戏登录共用数据库行锁，游戏清理完成前保持占用，见[编辑并发保护](GM_EDIT_CONCURRENCY.md)。游戏适配与网关联合部署待执行；踢人、封禁准入与事件采集继续接入。

<a id="f10"></a>
## F10 · 登录器本地引擎

当前：Flutter → Host逐行JSON RPC → C# Engine/Core。方法包括`initialize/health/check/autoUpdate/update/pause/preferences/register/login/resetGameConfiguration/updateLauncher`。文件事务、安装器、目录发现、进程管理和签名检查各有独立实现。

源码：[Host](../launcher/Host/Program.cs)、[Engine](../launcher/Engine/)、[Core](../launcher/Core/)、[Flutter控制器](../launcher/Flutter/lib/launcher_controller.dart)。当前为Windows，Host不通过本地HTTP端口提供控制API。

<a id="f11"></a>
## F11 · GM页面与主题

当前：`gm-web`独立ES模块、侧栏布局、主题编辑、响应式样式和功能组件。按私有网关同源部署，客户端不持有数据库连接。

源码：[shell.js](../gm-web/shell.js)、[live.js](../gm-web/live.js)、[第三方声明](../gm-web/THIRD_PARTY_NOTICES.md)。前端重构按[页面职责](FRONTEND_PLAN.md)推进；PWA/Android仍是后续客户端。

<a id="f12"></a>
## F12 · 审计与治理

当前：各领域的永久操作记录、邮件/福利回执已接；统一审计查询、多管理员身份/权限、封禁理由与执行回执待建设。新增治理动作复用F04/F05，运行态执行经F09。

现有证据入口：[GM_API_V1_DRAFT.md](GM_API_V1_DRAFT.md)及各`*-operations`接口；新实现以[实际路由](API_CATALOG.md)为准。

<a id="f13"></a>
## F13 · 通关采集、统计与异常规则（已接入结算采集和阈值筛选）

已实现：游戏结算事务采集并按完成键去重，按时间范围分页聚合次数、最快/平均耗时与小时峰值；反作弊模块提供阈值筛选及人工封禁入口。见[反作弊](GM_ANTI_CHEAT.md)。

计划：游戏进入/过图/通关/结算事件 → 事件标识去重 → 有界批量入库 → 按时间/角色/副本聚合 → 规则版本、证据和人工复核。客户端上报应保留来源，与游戏权威结算分开。当前无新管理路由，不由页面高频查询游戏代替采集。

<a id="f14"></a>
## F14 · 玩家公开快照（设计已定）

计划：GM公开名单，或全部角色含未来角色 → 每日低优先级生成背包/穿戴/通关投影 → 完整快照原子发布 → 外部服务只读。公开请求读取已发布缓存，权限与管理API分离。依赖F06/F07/F13。

设计：[PUBLIC_PLAYER_SNAPSHOTS.md](PUBLIC_PLAYER_SNAPSHOTS.md)。当前没有快照任务或公开读取端点。

<a id="f15"></a>
## F15 · 构建、版本、运行与备份

当前：独立依赖边界检查、双平台Go构建、Web打包、C#/Flutter测试、隔离预览脚本；schema显式初始化。配置与产物外置，游戏服务部署和玩家源发布走各自流程。

源码：[scripts](../scripts/)、[deploy](../deploy/)、[management/schema.go](../gateway/internal/management/schema.go)。管理页面的服务状态、日志、备份恢复编排和PVF切换尚需统一。历史测试结果与本次提取验证分开记录。
