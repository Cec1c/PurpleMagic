# 兰紫 GM Console：真实接口接入

2026-10-20 追加：[查询性能与 PVF 数据模式](GM_QUERY_PERFORMANCE.md)记录活动/物品库、多角色任务、按需加载、取消、超时和模式设置。


2026-10-20 公开源码同步：新版客户端、30天 Token 记忆和配套接口已接入外部 GM 与游戏管理网关。验证范围见 [GM Console 检查记录](../gm-console/design-qa.md)。

当前源码：`gm-console/`。以用户认可的兰紫深浅毛玻璃版为唯一视觉基准，复用现有 Go 领域事务、PVF 定义与永久回执。Node.js 用于开发和构建，正式产物为静态文件。

## 入口与设计留档

- 实际 API 联调：http://127.0.0.1:8779/ 。连接本地 8780 HTTPS 管理服务及一次性 MariaDB，账号、物品和操作结果来自实际 Go API。
- 独立视觉演示：http://127.0.0.1:8778/ 。保留演示模式，业务数据与真实客户端隔离。
- [已认可设计档与论坛复用说明](design/purplemagic-orchid-20261019/README.md)：冻结 CSS 与布局规则。
- 第一版 `gm-web/` 已归档并标记 `FROZEN_DO_NOT_REFERENCE`；历史 `gm-web-next/` 不是本版视觉基准。`scripts/package_gm_web.py` 的默认输入已改为 `gm-console/dist/`。

## 已接入范围

| 页面 | 实际行为 |
| --- | --- |
| 查询、账号与角色 | 服务端搜索、游标分页、完整账号角色展开、在线快照、复选与去重。选账号覆盖其全部现有角色。在线筛选在数据库分页前应用完整运行态名单。 |
| 背包八区 | 按角色/容器分页读取；物品实例详情、账号共享归属、PVF分类、宠物实例信息；按服务端能力提供数量、强化、增幅属性、锻造和期限调整，以及批量删除。 |
| 邮件 | PVF物品检索、固定最终收件名单、一次确认；多种附件分为多封邮件，金币仅附第一封。后台队列支持暂停/续发，失败名单可重试。邮箱详情和删除复用邮箱事务。 |
| 游戏任务 | 进行中、全部、已完成、PVF任务库；选中任务完成/重置，完成操作不发任务奖励。 |
| GM成长 | 等级、职业/转职/觉醒、额外SP/TP。组合修改只确认一次，后续步骤按最新版本重新准备，各步骤各有永久回执。 |
| 资产 | 角色金币、账号点券。点券以十进制字符串/BigInt计算差额，保持64位整数精度。 |
| 福利 | 创建、启用/暂停、现有角色及可选未来角色；启用后后台自动发放；发放情况查询读取实际投递回执。 |
| 操作记录 | 邮件任务与各领域流水分页读取；回执保留失败详情与已完成步骤。 |
| 公告 | Hub独立会话/CSRF；本标签页草稿、版本校验、发布到签名公告；编辑已有公告后保存草稿再发布不会重复追加。 |
| 下载源、游戏连接 | 完整来源ID/名称/地址/公钥/证书/启用状态；游戏地址和端口通过独立版本化签名发布接口更新。其余Profile字段及EXE/PVF摘要保留。 |

角色专属操作复用目标角色编辑保护；账号共享资产继续使用账号保护。PVF按完整hash核对。UI不连接数据库。

现有本服写契约尚未涵盖的细项：宠物成长编辑、时装属性重选、徽章镶嵌/装备附魔、代币券。这些字段保留读取或禁用态，不能用其他工具的数据库结构替代。`徽章`按当前PVF的`avatar emblem`物品标签从材料容器筛选；它不等同于公会勋章。

## 操作恢复与读取范围

Token入口为独立居中表单，无账号资料卡；默认勾选「在此设备记忆30天」。`POST /api/auth/login`新增可选`remember`：启用后签发30天HttpOnly/SameSite=Strict Cookie，HTTPS保持Secure；服务端仅持久化随机会话摘要和到期时间。原始管理Token不写入localStorage或会话文件。未勾选沿用12小时会话。服务重启恢复记忆，管理Token轮换使旧记忆失效；左下角「访问Token→更换Token」撤销并清除本机记忆。

一体管理配置需指定独立私有文件`sessionStorePath`；外部GM服务也支持该字段，默认位于`queuePath + ".sessions.json"`。文件不放在Web目录或PVF索引中，由一个认证服务进程持有。本地测试状态由 `--state-dir` 指定，位于 Git 忽略目录。

动效为独立`src/motion.css`与`src/motion.ts`层：页面/内容淡入、页签选中胶囊滑动、弹层/提示出现和按钮反馈；数据刷新与输入不重复触发导航动效。系统减少动态效果时关闭动效。原四份冻结CSS保持不变。

`src/api/http.ts` 管理同源Cookie、两个CSRF域和幂等键。`console.ts` 管理分页与取消旧请求；`operations.ts` 将表单映射到领域preview/commit。确认后的请求与已完成步骤保存在sessionStorage，响应丢失使用原请求和原幂等键恢复。明确拒绝可结束后重新准备；提交成功后的刷新失败不会把业务写入当成失败。

邮件后台模式由`capabilities.backgroundMail`识别。确认后服务端持久队列继续投递；一体联调服务则连续调用已有批次接口。已发回执避免重复发放。福利投递进度以查询时的永久回执为准，刷新重新获取实际进展。

账号目录默认20个、物品每容器100条、邮箱/作业30条、领域流水每类20条，按按钮继续分页。角色切换取消旧范围请求；空闲页面不轮询玩家资产。

## Hub连接发布契约

`GET /admin/api/state` 增加`connectionRevision`。`PUT /admin/api/connection` 接受`{gameHost,bootstrapPort}`及必需的`If-Match`；允许IP或主机名和1–65535端口。只改Published.Profile的GameHost/BootstrapPort并递增签名序号。相同内容重放不递增，冲突返回409，保存失败回滚；玩家监听不开放此管理路由。

## 构建与联调

`npm run build` 默认构建真实客户端。`npm run dev` 的development模式仍为独立视觉演示；`vite --mode live --port 8779` 读取被忽略的本地代理配置。开发代理仅接受loopback上游，并校验本地CA。生产静态目录挂载`/console/`，管理API同源。

`scripts/preview_gateway_live.py` 创建一次性MariaDB、测试运行态、真实Go网关和真实Hub。它生成专用Key/Token、CA、签名私钥和测试Profile，保存在指定`--state-dir`，不读取线上部署凭据。`gm-console/tests/live-integration.mjs` 验证真实适配器的写入与读取，要求明确提供`PURPLEMAGIC_LIVE_FIXTURE`及`NODE_EXTRA_CA_CERTS`。

完整验收以`gm-console/design-qa.md`本次追加与本地回执为准。实际部署地址和环境回执由部署者单独保存。
