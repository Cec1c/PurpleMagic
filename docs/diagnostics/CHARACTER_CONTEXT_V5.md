# 角色与时间上下文（外层 5 / 记录 4）

这份日志可直接回答：哪个角色、哪次连接、哪个副本／房间、故障发生于何时，以及服务器最后完成了哪个结算阶段。游戏进程 `exit_code=0` 与业务断线分别记录。

## 玩家操作

设置中同意并开启增强诊断日志，再登录游戏。导出仍是一份 `.log`，最近五次、每份 10 MiB、不自动上传。新增角色名和角色编号属于私有支持信息，分享给负责排查的人员；公开问题只使用脱敏摘要。采集同意升为 3，已有反作弊同意保持。

服务端身份关联在登录认证时建立；在游戏已经运行后才开启日志，或重开登录器恢复进程时，基础与异常采集继续工作，角色关联标为 `launch_binding_unavailable`，下次登录建立新关联。凭据不保存到配置、日志或导出文件。

## 字段与解释

`log.summary.context` 和身份、事件、异常记录的 `context` 包含同一结构：

| 字段 | 含义 |
| --- | --- |
| `correlation_id` | 本次启动的随机诊断编号，与进程日志 `session_id` 关联；双开分别生成 |
| `server_instance` / `connection_generation` | 服务进程实例及本次登录内的连接代次；旧实例和旧代次不能覆盖当前身份 |
| `source` | `authenticated_game_session`，由服务端认证会话提供 |
| `observed_at` / `received_at` | 服务端观察时间与登录器收到响应的 UTC 时间 |
| `status` / `reason` | `current`、`waiting`、`last_known`、`unavailable` 及原因 |
| `fields` | 逐字段的 `value`、`status`、`reason`；继承上述来源、观察时间和连接代次 |
| `clock` | 时间源、估算偏差与误差范围（毫秒）、本机 UTC 偏移和校准状态 |

白名单字段为：`character_id`、`character_name`、`character_generation`、`connection_id`、`channel_id`、`party_id`、`party_generation`、`dungeon_id`、`map_id`、`room`、`instance_id`、`settlement_phase`、`boss_dead`、`business_state`、`reason_code`。数值身份使用十进制字符串；不经过浮点数。无角色时身份字段为 `null/not_selected`，未在场景中的字段为 `null/not_in_scene`。当前队伍模型没有独立队伍代次，`party_generation` 明确标 `not_exposed`。

`instance_id` 在组队时使用权威副本实例编号，单人使用服务端连接与副本创建时间组合；与 `server_instance` 一起关联。`boss_dead` 来自服务端已接受的终怪判定，不代表客户端每个死亡画面。结算阶段来自服务端单调状态机，不从按键、退出码或日志文案推断。

服务器在会话事件处理后读取已有状态，不查玩家数据库、不读客户端内存、不增加游戏报文。状态变化进入有界事件队列；连接、角色、场景、结算变化均形成记录。断线保留最后身份和结算状态并标 `last_known`。无法区分主动关闭与网络中断时使用 `connection_closed_unknown`；登录替换使用 `login_replaced`，不编造故障根因。

客户端约每两秒读取一次；请求超时后降低频率。超过十秒未获得新观察或读回失败时保留 `last_known`。异常记录保存当时已经收到的上下文；它有自己的 `observed_at`，不能把有界轮询当成与异常同步发生的服务端快照。进程结束后短暂保留采集以接收最终连接事件；网络完全中断时最后已知值和缺失原因仍可导出。

每个事件保留 UTC。分析器的 `support_summary` 同时输出北京时间（`+08:00`）、角色与角色编号、连接、副本、结算阶段、上下文状态和校时信息。时钟偏差由带认证的请求收发时间估算，误差为网络往返时间扣除服务端处理时间后的半宽；本机时钟跳变时标 `local_clock_changed`，不输出伪精确偏差。

## 最小服务端契约

1. 登录请求可选 `diagnosticCorrelationId`（32 位十六进制）。关闭日志时不发送。旧服务端可忽略该字段，登录器显示 `server_not_provided`，登录不受影响。
2. 成功登录的可选 `diagnostics`：`version=1`、`correlation_id`、`read_token`、`expires_utc`。只读凭据与本次游戏登录令牌在服务端内存中绑定；不输出游戏令牌。普通与保护发行包使用相同协议。
3. `POST /api/diagnostics/context`，JSON 为 `read_token`、`correlation_id`、`after`（已接收的事件序号）。网关仅新增这一条 POST 白名单；管理接口继续隔离。禁止在 URL、Cookie 或日志中携带凭据。
4. 响应：`version`、`correlation_id`、`server_received_utc`、`server_sent_utc`、`last_sequence`、`events_lost`、`snapshot`、`events`。每个事件为 `sequence`、`kind`、`snapshot`。快照的 `fields` 同上述白名单；其 `observed_at/source/status` 为共同字段来源。
5. 同一凭据最短一秒读取一次，返回最多 64 个事件；队列截断由 `events_lost` 指明。最多 256 个订阅，24 小时到期，十分钟无读取则失效，过期项在签发时清理。凭据与关联编号不匹配返回 404；无效字段不进入导出。

服务端配套位于游戏服务的账号 API 和会话事件所有者中；PurpleMagic 网关仅转发允许的请求。客户端、网关和游戏服务必须累计部署后才具有完整角色采集能力。源码实现、发布和玩家真实采集分别记录。

## 验证

合成验证覆盖：A → 选角 → B → 重连 → 断线；旧连接／旧角色代次拒绝；双开关联隔离；无角色与接口不可用；队列有界和丢事件标记；时钟跳变；退出码 0 与业务断线并存；Go HTTP 实际响应经 C# 源生成序列化读取；新日志经分析器和严格 schema 验证；旧 2/3/4 格式兼容。

新格式见 [envelope-v5.schema.json](envelope-v5.schema.json) 和 [record-v4.schema.json](record-v4.schema.json)。旧日志封存，格式不混写。异常原生证据契约继续使用 [NATIVE_EVIDENCE_V4.md](NATIVE_EVIDENCE_V4.md)。
