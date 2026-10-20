## 2026-10-20 登录器增强诊断候选

新增自动进程关联、基础日志和 AT-5T 专项；一个开关按启动记录，保留最近五次，通过原生另存为导出 `.log`。便携入口为 `PurpleMagic.exe`，识别 `.client/` 与 `client/`，保持更新契约。见 [功能与接口](LAUNCHER_DIAGNOSTICS.md) 和 [验证范围](diagnostics/VALIDATION.md)；原生游戏两小时与帧时间验收仍待完成。

<!-- orchid-live-20261019 -->
## 2026-10-19 兰紫 GM Console 真实接入

新客户端 `gm-console/` 已接同源 Go v1 与 Hub，覆盖查询、八类背包、邮件、任务、成长/资产、自动福利、领域流水和公告/下载源/游戏连接。现有本服能力范围与余项见 [本轮接入与验收](GM_CONSOLE_INTEGRATION_20261019.md)。第一版 `gm-web/` 已封存，新打包入口使用 `gm-console/dist/`。新版已完成隔离数据库和浏览器接入验证；下文历史阶段描述保留作追溯。

<!-- 2026-10-18 -->
本轮候选已更新GM-01／18／19／20／21：账号与角色复选、账号全部现有角色去重、一次确认连续批次、完整确认名单、启用后后台自动福利、旧规则迁移截止时间与永久回执；角色编辑按目标角色占用，账号共享资产保留账号范围。详见[规则契约](GM_FEEDBACK_RULES.md)。下表其余待办保留，外部服务和持久邮件队列已实现，见[服务分工](GM_EXTERNAL_SERVICES.md)。

# PurpleMagic 功能台账

2026-10-17，覆盖原48项GM目标，补充12项登录器、8项网关、3项前端能力，共71项。原GM编号保持；同一项中包含多个操作时按实际范围列出，不用一个完成百分比覆盖细项。

[全局架构](ARCHITECTURE.md) · [交互图](architecture/current.html) · [基础设施F01～F15](FOUNDATIONS.md) · [实际路由目录](API_CATALOG.md) · [前端分区](FRONTEND_PLAN.md) · [JSON](feature-ledger.json) · [CSV](feature-ledger.csv)

**状态**：已接入＝当前候选有新接口/页面或组件实现；部分完成＝列出的子集已接；旧版实现＝迁移参考有代码、当前管理入口未开放；开发中＝代码存在但验收未完成；待接入/待实现/设计已定＝后续工作。源码、自动化检查、部署、实机验收分别记录。副职业已完成隔离数据库与网页验收。

**来源**：G＝原DNF90GM；A＝提供的C# GM；R＝20260928源码包的网关后台/补丁；S＝用户桌面/安卓参考图；U＝用户新增需求。来源细项见[原48项矩阵](GM_FEATURE_MATRIX.md)。该清单保存已盘点目标，未知参考功能仍可追加编号。

**接口记法**：除以`/`开头的完整路径外，GM路径前缀为`/api/v1/`；`{a}`为`{accountId}`，`{c}`为`{characterId}`。同组简写沿用行内账号/角色前缀；精确HTTP方法、参数名和源码位置以实际路由目录为准。标“旧”的路由只用于迁移核对，当前ManagementHandler不注册它们。

## GM

| 编号 / 功能 | 状态、当前页面 | 当前实现范围 / 剩余 | 基础调用设施与接口 |
| --- | --- | --- | --- |
| **GM-01** 账号/角色检索、归属、基础资料 | 已接入；账号/角色列表与详情 | 分页、检索、归属与基础资料。<br>后续：前端重组为独立玩家详情页 | [F01](FOUNDATIONS.md#f01) / [F02](FOUNDATIONS.md#f02) / [F03](FOUNDATIONS.md#f03)<br>`GET accounts; accounts/{a}; accounts/{a}/characters; characters; characters/{c}` |
| **GM-02** 账号货币、金币与共享晶块 | 部分完成；点券/金币/金库/共享资源 | 点券增减、角色与金库金币、12槽晶块/灵魂及各自流水。<br>后续：统一资产入口；其他货币按权威钱包逐项核对 | [F03](FOUNDATIONS.md#f03) / [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F09](FOUNDATIONS.md#f09)<br>`accounts/{a}/currency; currency-adjustments; characters/{c}/gold-operations; accounts/{a}/vault-operations; inventory-operations` |
| **GM-03** 冒险团等级、成长胶囊 | 旧版实现；待迁入账号详情 | 旧版冒险团等级与胶囊操作。<br>后续：迁入v1、预览/回执及新页面 | [F03](FOUNDATIONS.md#f03) / [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05)<br>`旧 /api/accounts/{id}/honor-level; growth-capsule` |
| **GM-04** 密码重置、登录限制与绑定管理 | 待接入；账号安全页待建 | 参考目标已收录。<br>后续：密码、绑定与登录限制接游戏准入 | [F01](FOUNDATIONS.md#f01) / [F03](FOUNDATIONS.md#f03) / [F09](FOUNDATIONS.md#f09) / [F12](FOUNDATIONS.md#f12)<br>`无当前v1管理路由` |
| **GM-05** 角色战斗属性、SP/TP | 部分完成；技能点页签 | 额外SP/TP、归零/重算与流水。<br>后续：持续战斗属性需游戏侧覆写契约 | [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F06](FOUNDATIONS.md#f06) / [F09](FOUNDATIONS.md#f09)<br>`characters/{c}/skill-points; skill-point-operations` |
| **GM-06** 等级、转职/觉醒、副职业 | 部分完成；等级/职业/副职业页签 | 等级、转职与觉醒及四种副职业已通过隔离数据库与网页验证。<br>后续：游戏适配与网关联合部署、原生重登验收 | [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F06](FOUNDATIONS.md#f06) / [F09](FOUNDATIONS.md#f09)<br>`characters/{c}/level; level-operations; profession; profession-operations; expert-job; expert-job-operations` |
| **GM-07** 负重、金币携带上限、背包/仓库容量、装备槽与副本权限 | 部分完成；容量页签 | 主背包、装扮栏、个人仓库档位扩容。<br>后续：负重/携金阶段/装备槽/副本权限迁入；持久覆写 | [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F06](FOUNDATIONS.md#f06) / [F09](FOUNDATIONS.md#f09)<br>`characters/{c}/containers; container-operations` |
| **GM-08** 契约查询与发放 | 旧版实现；契约页面待迁 | 旧版契约查询与发放。<br>后续：v1及期限/归属/回执页面 | [F03](FOUNDATIONS.md#f03) / [F04](FOUNDATIONS.md#f04) / [F06](FOUNDATIONS.md#f06) / [F09](FOUNDATIONS.md#f09)<br>`旧 /api/characters/{id}/premium` |
| **GM-09** 改名、克隆、删除角色 | 旧版实现；角色管理页面待迁 | 旧版改名、克隆、删除。<br>后续：新接口、关联数据计划与事务验收 | [F03](FOUNDATIONS.md#f03) / [F04](FOUNDATIONS.md#f04) / [F09](FOUNDATIONS.md#f09)<br>`旧 /api/characters/{id}/rename; clone; delete` |
| **GM-10** 玩家背包、穿戴及分类浏览 | 已接入；角色资源/容器列表 | 背包、穿戴及分类分页。<br>后续：按完整玩家工作区重排布局 | [F02](FOUNDATIONS.md#f02) / [F03](FOUNDATIONS.md#f03)<br>`GET characters/{c}/inventory` |
| **GM-11** 玩家持有物品的实例详情 | 部分完成；实例详情面板 | 槽位/实例值/PVF定义及已孵化宠物详情。<br>后续：附魔、徽章等完整实例字段继续适配 | [F03](FOUNDATIONS.md#f03) / [F06](FOUNDATIONS.md#f06)<br>`GET characters/{c}/inventory/{container}/{slot}` |
| **GM-12** 装备/装扮配置 | 部分完成；物品配置/批量预览 | 品级、强化/增幅、锻造、期限、堆叠及宠物装备期限。<br>后续：装扮属性、附魔、徽章、宠物成长等细项 | [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F06](FOUNDATIONS.md#f06) / [F09](FOUNDATIONS.md#f09)<br>`inventory/{container}/{slot}/options; inventory-operations` |
| **GM-13** 单格删除、选中批量删除、分类清理 | 部分完成；物品删除与本页选择 | 单格/选中批次、穿戴、宠物关联删除及回执。<br>后续：跨页分类清理、宠物蛋关联处理 | [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F09](FOUNDATIONS.md#f09)<br>`characters/{c}/inventory-operations` |
| **GM-14** 个人仓库、账号金库与共享资源 | 部分完成；容器/金库/共享资源 | 个人仓库、账号金库、共享资源读取编辑与容量/金币。<br>后续：统一账号视图；完整物品实例细项 | [F03](FOUNDATIONS.md#f03) / [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F06](FOUNDATIONS.md#f06) / [F09](FOUNDATIONS.md#f09)<br>`characters/{c}/inventory; accounts/{a}/vault; vault-operations; inventory-operations` |
| **GM-15** 物品目录、完整说明、套装与条件字段 | 已接入；物品目录与详情 | 物品定义、说明、套装/条件字段及磁盘索引。<br>后续：客户端分片缓存与内存继续压缩 | [F06](FOUNDATIONS.md#f06)<br>`GET items; items/{itemId}` |
| **GM-16** 发放参数、装备属性与期限选项 | 部分完成；邮件/福利附件参数 | 品级/强化/增幅/锻造/期限；福利跨PVF核对。<br>后续：装扮属性选项与直接背包发放 | [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F06](FOUNDATIONS.md#f06) / [F07](FOUNDATIONS.md#f07)<br>`items/{itemId}/grant-options; mail/preview; benefits` |
| **GM-17** 角色邮箱、附件、单封删除/清理 | 部分完成；邮箱列表/详情/清理 | 筛选、正文附件、选中删除和跨页固定名单清理。<br>后续：附件完整实例字段 | [F03](FOUNDATIONS.md#f03) / [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F07](FOUNDATIONS.md#f07)<br>`characters/{c}/mailbox; mailbox/{mailId}; mailbox-operations; mailbox-cleanups` |
| **GM-18** 单角色邮件预览与发放 | 已接入；发放页 | 单角色预览、邮件投递及永久回执。<br>后续：新版页面重组 | [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F07](FOUNDATIONS.md#f07)<br>`POST mail/preview; tasks/{id}/run` |
| **GM-19** 批量邮件、选定/在线角色发放 | 部分完成；名单/在线发放 | 指定最多100人、在线最多5000人，固定快照/分批恢复。<br>后续：游戏online_snapshot适配部署联调 | [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F07](FOUNDATIONS.md#f07) / [F09](FOUNDATIONS.md#f09)<br>`POST mail/preview; mail/online-preview; tasks/{id}/run` |
| **GM-20** 注册/新手福利与历史补发 | 已接入；福利规则/记录 | 金币/指定道具按账号或角色一次、点券按账号一次直接到账、全部角色预览、连续分批与暂停续发、自动扫描、参数附件、跨PVF保留去重。<br>后续：统一规则编辑体验；初始资产政策仍归游戏创建链路 | [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F06](FOUNDATIONS.md#f06) / [F07](FOUNDATIONS.md#f07)<br>`benefits; benefits/{id}/state; benefits/{id}/preview; benefits/{id}/pvf-preview; pvf-apply` |
| **GM-21** 运营作业、进度和失败重试 | 部分完成；运营作业页 | 外部持久邮件队列、进度、暂停、重试及重启恢复；邮箱清理另有持久进度。<br>后续：通用调度租约、扫描/索引/快照作业 | [F05](FOUNDATIONS.md#f05) / [F07](FOUNDATIONS.md#f07)<br>`GET tasks; tasks/{id}; tasks/{id}/recipients; POST tasks/{id}/run; 外部 schedule/pause` |
| **GM-22** 进行中、已完成游戏任务 | 已接入；角色游戏任务 | 已接/完成/全部筛选、进度及读取时刻。<br>后续：新版任务工作区 | [F02](FOUNDATIONS.md#f02) / [F03](FOUNDATIONS.md#f03) / [F06](FOUNDATIONS.md#f06)<br>`GET characters/{c}/quests` |
| **GM-23** 任务库、可接条件、前置关系、说明与奖励 | 部分完成；任务库与详情 | 名称/ID查询、条件/前置/说明/奖励。<br>后续：角色可接资格和等级/地区筛选 | [F06](FOUNDATIONS.md#f06)<br>`GET quests; quests/{questId}` |
| **GM-24** 接取、放弃、标记可交 | 部分完成；游戏任务操作 | 接取、放弃、标记可交及回执。<br>后续：涉及物品的联合事务；编辑保护联合部署 | [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F06](FOUNDATIONS.md#f06) / [F09](FOUNDATIONS.md#f09)<br>`POST characters/{c}/quest-operations/preview; quest-operations` |
| **GM-25** 标记完成、恢复未完成、重置任务链 | 已接入；任务完成/恢复/重置 | 无奖励完成、普通任务恢复、关联家族重置。<br>后续：编辑保护联合部署；职业变更走专用流程 | [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F06](FOUNDATIONS.md#f06) / [F09](FOUNDATIONS.md#f09)<br>`POST characters/{c}/quest-operations/preview; quest-operations` |
| **GM-26** 批量处理当前已接任务 | 部分完成；游戏任务本页选择 | 选中最多200项，前后值及整批原子提交。<br>后续：跨页全部已接任务 | [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F06](FOUNDATIONS.md#f06)<br>`characters/{c}/quest-operations` |
| **GM-27** 按等级/分类处理主线、支线、成就、称号、每日与职业任务 | 待实现；任务库分类批处理 | 主线/支线/成就/称号/每日/职业目标已收录。<br>后续：分类资格、奖励语义及批次事务 | [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F06](FOUNDATIONS.md#f06) / [F07](FOUNDATIONS.md#f07)<br>`无当前分类批次路由` |
| **GM-28** 游戏活动查询与开关 | 旧版实现；游戏活动页待迁 | 旧版活动读取/保存。<br>后续：v1、实际生效状态和新页面 | [F04](FOUNDATIONS.md#f04) / [F09](FOUNDATIONS.md#f09)<br>`旧 /api/activities；游戏运行适配` |
| **GM-29** 账号封禁、IP 封禁、期限、原因及解除 | 待实现；治理·封禁/IP | 账号/IP、期限、原因、解除目标已收录。<br>后续：权威准入执行点、审计及页面 | [F03](FOUNDATIONS.md#f03) / [F04](FOUNDATIONS.md#f04) / [F09](FOUNDATIONS.md#f09) / [F12](FOUNDATIONS.md#f12)<br>`无当前v1封禁路由` |
| **GM-30** 在线角色、踢下线、登录记录 | 部分完成；在线名单用于邮件 | 完整在线快照、采集时间与代次。<br>后续：独立在线页、踢下线、登录历史 | [F03](FOUNDATIONS.md#f03) / [F09](FOUNDATIONS.md#f09)<br>`POST mail/online-preview → online_snapshot` |
| **GM-31** 通关记录、排行、间隔、耗时分布、单局详情 | 待实现；通关记录与统计 | 耗时、排行、间隔、单局记录目标已收录。<br>后续：权威事件采集、幂等入库、聚合与页面 | [F03](FOUNDATIONS.md#f03) / [F07](FOUNDATIONS.md#f07) / [F13](FOUNDATIONS.md#f13)<br>`无当前通关查询路由` |
| **GM-32** 收益交叉核查、未结算进入、疑似异常与人工复核 | 待实现；异常调查 | 收益核查、未结算进入、规则与人工复核目标。<br>后续：事件来源、缺口、规则版本、人工结论 | [F07](FOUNDATIONS.md#f07) / [F12](FOUNDATIONS.md#f12) / [F13](FOUNDATIONS.md#f13)<br>`无当前异常查询路由` |
| **GM-33** 背包异常检查、无效引用与数据修复 | 待实现；资产扫描与修复计划 | 无效引用与背包异常目标已收录。<br>后续：有界扫描、修复预览/事务/回执 | [F03](FOUNDATIONS.md#f03) / [F04](FOUNDATIONS.md#f04) / [F07](FOUNDATIONS.md#f07) / [F12](FOUNDATIONS.md#f12)<br>`无当前扫描路由` |
| **GM-34** 外部只读玩家背包/穿戴/通关快照 | 设计已定；公开范围与快照管理 | GM选名单或全量含未来角色；默认每天一次。<br>后续：低优先级生成、原子发布、公开只读接口 | [F06](FOUNDATIONS.md#f06) / [F07](FOUNDATIONS.md#f07) / [F13](FOUNDATIONS.md#f13) / [F14](FOUNDATIONS.md#f14)<br>`PUBLIC_PLAYER_SNAPSHOTS.md；无当前公开路由` |
| **GM-35** 登录器公告编辑、排序与发布 | 部分完成；Hub公告；console入口待统一 | Hub编辑/排序/修订校验/签名公告已有。<br>后续：gm-web仅在/gm/模式启用；/console/会话与页面接入 | [F01](FOUNDATIONS.md#f01) / [F08](FOUNDATIONS.md#f08)<br>`GET /admin/api/state; PUT /admin/api/news` |
| **GM-36** 游戏广播、弹窗、事件播报 | 待实现；广播/弹窗/事件播报 | 接收对象与事件目标已收录。<br>后续：适配游戏通知、权限与结果 | [F09](FOUNDATIONS.md#f09) / [F12](FOUNDATIONS.md#f12)<br>`无当前v1广播路由` |
| **GM-37** 登录器更新、Linux 网关与外部更新源 | 部分完成；更新Hub独立管理页 | 登录器、源配置、更新及单网关代码已整合。<br>后续：统一管理入口；已发行登录器源码对齐 | [F08](FOUNDATIONS.md#f08) / [F10](FOUNDATIONS.md#f10)<br>`/admin/api/source; check; publish; /launcher/v2/bootstrap.json` |
| **GM-38** 服务状态、日志、维护、PVF/索引版本管理 | 部分完成；能力信息/离线索引工具 | 索引代次/能力信息与本地检查脚本。<br>后续：状态、日志、维护、构建及切换管理页 | [F02](FOUNDATIONS.md#f02) / [F06](FOUNDATIONS.md#f06) / [F08](FOUNDATIONS.md#f08) / [F15](FOUNDATIONS.md#f15)<br>`GET capabilities; /api/status；GMIndex CLI` |
| **GM-39** 账号导出/恢复、服务数据备份 | 旧版实现；账号导出/恢复待迁 | 旧版导出恢复；服务备份归部署工具。<br>后续：v1预览与版本核对、恢复验收 | [F03](FOUNDATIONS.md#f03) / [F04](FOUNDATIONS.md#f04) / [F12](FOUNDATIONS.md#f12) / [F15](FOUNDATIONS.md#f15)<br>`旧 /api/accounts/{id}/backup; /api/accounts/restore` |
| **GM-40** 操作审计与查询 | 部分完成；各功能历史与回执 | 资产/成长/任务/邮箱独立历史，邮件福利持久回执。<br>后续：跨功能审计、管理员身份和统一筛选 | [F05](FOUNDATIONS.md#f05) / [F12](FOUNDATIONS.md#f12)<br>`*-operations; currency-adjustments; tasks/{id}/recipients` |
| **GM-41** 决斗胜点、等级与段位 | 待接入；决斗成长 | 胜点、等级、段位目标已收录。<br>后续：PVF选项、事务和页面 | [F03](FOUNDATIONS.md#f03) / [F04](FOUNDATIONS.md#f04) / [F06](FOUNDATIONS.md#f06)<br>`无当前v1决斗管理路由` |
| **GM-42** 副本进入次数与额度 | 待接入；副本额度 | 基础/额外/已用、账号/角色/日期目标。<br>后续：与游戏进入校验一致的额度管理 | [F03](FOUNDATIONS.md#f03) / [F04](FOUNDATIONS.md#f04) / [F09](FOUNDATIONS.md#f09)<br>`无当前v1额度管理路由` |
| **GM-43** 异常统计白名单 | 待实现；异常白名单 | 加入/移除/原因/操作者及原始记录目标。<br>后续：规则生效和复查过滤 | [F12](FOUNDATIONS.md#f12) / [F13](FOUNDATIONS.md#f13)<br>`无当前v1白名单路由` |
| **GM-44** IP 注册额度 | 待接入；IP注册额度 | 设置和恢复默认目标。<br>后续：接实际注册准入与读回 | [F04](FOUNDATIONS.md#f04) / [F09](FOUNDATIONS.md#f09) / [F12](FOUNDATIONS.md#f12)<br>`无当前v1注册额度路由` |
| **GM-45** 释放卡住的角色会话 | 待接入；释放卡住会话 | 卡角色会话释放目标。<br>后续：游戏权威执行与返回结果 | [F09](FOUNDATIONS.md#f09) / [F12](FOUNDATIONS.md#f12)<br>`无当前v1释放会话路由` |
| **GM-46** 账号同步与批量管理 | 待接入；批量账号管理 | 同步/创建/删除/批量重置密码目标。<br>后续：关联数据预览、任务、事务及权限 | [F03](FOUNDATIONS.md#f03) / [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05) / [F07](FOUNDATIONS.md#f07) / [F09](FOUNDATIONS.md#f09)<br>`无当前v1批量账号路由` |
| **GM-47** 客户端文件完整性与允许版本 | 部分完成；登录器文件检查/Hub发布 | 签名更新与文件摘要校验已有。<br>后续：允许版本管理与新版统一页面 | [F08](FOUNDATIONS.md#f08) / [F10](FOUNDATIONS.md#f10)<br>`Host check/update；签名bootstrap与内容清单` |
| **GM-48** 网关端口与连接策略 | 待接入；系统连接与维护 | 端口/来源/维护目标与工具已有。<br>后续：统一编辑、发布、生效读回与回退 | [F08](FOUNDATIONS.md#f08) / [F09](FOUNDATIONS.md#f09) / [F15](FOUNDATIONS.md#f15)<br>`现有网关配置/部署脚本；无统一v1路由` |

## 登录器

| 编号 / 功能 | 状态、当前页面 | 当前实现范围 / 剩余 | 基础调用设施与接口 |
| --- | --- | --- | --- |
| **L-01** 启动初始化与偏好 | 已接入；Flutter首页/设置 | 目录、偏好与引擎状态。<br>后续：与新项目品牌/发行基线对齐 | [F10](FOUNDATIONS.md#f10)<br>`Host initialize/preferences` |
| **L-02** 账号注册/登录 | 已接入；注册登录表单 | 调用玩家网关，凭据与游戏管理鉴权分开。<br>后续：与实际游戏实例联调 | [F08](FOUNDATIONS.md#f08) / [F10](FOUNDATIONS.md#f10)<br>`Host register/login → /api/account/register; /api/account/login` |
| **L-03** 客户端目录发现 | 已接入；客户端目录选择 | 0.2.15保留自动目录发现。<br>后续：后续更新持续保留目录发现回归 | [F10](FOUNDATIONS.md#f10)<br>`ClientDirectoryDiscovery` |
| **L-04** 健康与频道诊断 | 已接入；状态/诊断 | 状态、频道配置与检查逻辑。<br>后续：实际发布profile联调 | [F10](FOUNDATIONS.md#f10)<br>`Host health；ChannelProbe/NativeChannelHistory` |
| **L-05** 检查/自动/手动更新与暂停 | 已接入；更新进度 | 文件摘要、进度、取消与恢复。<br>后续：对齐最新发行内容源契约 | [F08](FOUNDATIONS.md#f08) / [F10](FOUNDATIONS.md#f10)<br>`Host check/autoUpdate/update/pause` |
| **L-06** 签名与兼容性校验 | 已接入；启动/更新流程 | 签名、EXE/PVF兼容与完整性检查。<br>后续：对齐当前批准profile和实际玩家资源 | [F08](FOUNDATIONS.md#f08) / [F10](FOUNDATIONS.md#f10)<br>`SignedUpdate/NativeClientGate/NativeGuildCompatibility` |
| **L-07** 游戏启动与进程管理 | 已接入；启动游戏 | 原生客户端启动与进程生命周期。<br>后续：真实客户端验收 | [F10](FOUNDATIONS.md#f10)<br>`LauncherEngine/BundleProcesses` |
| **L-08** 配置重置 | 已接入；设置页 | 恢复游戏配置入口。<br>后续：统一新版设置布局 | [F10](FOUNDATIONS.md#f10)<br>`Host resetGameConfiguration` |
| **L-09** 公告显示 | 已接入；公告列表/正文 | 读取已发布公告。<br>后续：GM独立console编辑入口待统一 | [F08](FOUNDATIONS.md#f08) / [F10](FOUNDATIONS.md#f10)<br>`Hub签名bootstrap.news` |
| **L-10** 登录器自更新/便携安装 | 已接入；自更新与安装包 | 文件事务、回退和打包逻辑。<br>后续：最新发行基线与资源输入对齐 | [F08](FOUNDATIONS.md#f08) / [F10](FOUNDATIONS.md#f10)<br>`Host updateLauncher；BundleInstaller/FileTransaction/Portable` |
| **L-11** 品牌/视觉/主题 | 部分完成；Flutter界面 | 当前登录器界面已有。<br>后续：PurpleMagic命名/资产与产品界面重构 | [F10](FOUNDATIONS.md#f10) / [F11](FOUNDATIONS.md#f11)<br>`Flutter main.dart/launcher_controller.dart` |
| **L-12** 其他系统客户端 | 待规划；当前为Windows | 需求边界保留。<br>后续：如需扩展，复用API并单独适配平台 | [F10](FOUNDATIONS.md#f10)<br>`无当前Android/Linux桌面发行` |

## 网关

| 编号 / 功能 | 状态、当前页面 | 当前实现范围 / 剩余 | 基础调用设施与接口 |
| --- | --- | --- | --- |
| **GW-01** 玩家白名单转发 | 已接入；无独立操作页 | 剥离伪造来源/凭据头，注入可信来源。<br>后续：新页面配置入口 | [F08](FOUNDATIONS.md#f08)<br>`POST /api/account/login/register；GET /api/status` |
| **GW-02** 私有监听与GM挂载 | 已接入；/console/ | 同进程分离玩家/管理监听。<br>后续：跨域客户端另定鉴权方案 | [F01](FOUNDATIONS.md#f01) / [F02](FOUNDATIONS.md#f02)<br>`ManagementHandler + privateHandler` |
| **GW-03** 源配置与检查 | 已接入；/admin/ | 固定源配置、状态、修订与检查。<br>后续：统一到新GM系统页 | [F08](FOUNDATIONS.md#f08)<br>`PUT /admin/api/source；POST /admin/api/check` |
| **GW-04** 签名发布与资源服务 | 已接入；/admin/ | 签名发布、缓存与包服务。<br>后续：对齐当前线上源/profile，不从旧候选覆盖 | [F08](FOUNDATIONS.md#f08)<br>`POST /admin/api/publish；/launcher/v2/*` |
| **GW-05** 安装包上传 | 已接入；Hub管理页 | 包输入、校验与缓存。<br>后续：新统一管理页面 | [F08](FOUNDATIONS.md#f08)<br>`POST /admin/api/bundle/{version}` |
| **GW-06** 公告发布 | 已接入；Hub公告页 | If-Match修订、CSRF、签名发布。<br>后续：独立console接入 | [F08](FOUNDATIONS.md#f08)<br>`GET /admin/api/state；PUT /admin/api/news` |
| **GW-07** 管理schema初始化 | 已接入；安装CLI | 显式初始化、启动检查已有schema。<br>后续：版本迁移/回退流程继续规范 | [F03](FOUNDATIONS.md#f03) / [F04](FOUNDATIONS.md#f04) / [F05](FOUNDATIONS.md#f05)<br>`--init-management-schema` |
| **GW-08** 单进程运行与构建 | 已接入；配置/脚本 | Windows/Linux产物，无游戏服务源码依赖。<br>后续：完整线上安装和容量验收 | [F08](FOUNDATIONS.md#f08) / [F15](FOUNDATIONS.md#f15)<br>`cmd/playergateway；build_gateway.py` |

## 前端

| 编号 / 功能 | 状态、当前页面 | 当前实现范围 / 剩余 | 基础调用设施与接口 |
| --- | --- | --- | --- |
| **UI-01** 页面布局与主题编辑 | 已接入；GM导航/主题面板 | 主题调色、偏好保存及响应式。<br>后续：按完整功能重新组织页面 | [F11](FOUNDATIONS.md#f11)<br>`gm-web/shell.js；vendor/theme-studio.js` |
| **UI-02** 独立静态GM分发 | 已接入；/console/ | 无需Node后台，私有网关同源提供。<br>后续：发行版本协商和部署管理 | [F01](FOUNDATIONS.md#f01) / [F11](FOUNDATIONS.md#f11)<br>`gm-web/index.html；package_gm_web.py` |
| **UI-03** PWA/Android | 待实现；可选后续客户端 | Web已适配移动布局。<br>后续：安装/离线能力、设备凭据和原生适配 | [F01](FOUNDATIONS.md#f01) / [F02](FOUNDATIONS.md#f02) / [F11](FOUNDATIONS.md#f11)<br>`复用v1；当前无原生端或完整PWA` |

## 下一轮实现顺序

1. 继续统一玩家详情的资产、背包/穿戴、游戏任务、成长页面。
2. 继续装扮/附魔/宠物成长、任务物品联合事务与旧版高频管理功能；每项绑定领域事务与回执。
3. 部署已验证的游戏编辑保护；继续准入/封禁、在线会话及统一审计，完成公告/Hub会话统一。
4. 建通关事件与统计、异常复核、每日公开快照，再扩展PWA/Android。
5. 用批准PVF和已发行登录器确切源码做整套联调与上线验收。

账号编辑保护已完成候选及隔离验证，联合部署与原生客户端验收待执行。
