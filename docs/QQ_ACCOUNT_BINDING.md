# QQ 账号绑定与密码管理

QQ 绑定是可选功能。仓库示例和登录器构建默认关闭，单机、局域网和公网服都可独立选择是否启用。1.0.2 增加自愿绑定：玩家可以主动绑定，未绑定也能登录游戏。

## 玩家使用

1. 打开登录器，点击 **绑定 QQ**，填写游戏账号和密码，点击 **获取绑定码**。
2. 点击 **复制绑定指令**。在管理员配置的群内艾特机器人，或私聊机器人发送 `/绑定 123456`（替换为获取到的六位数字码）。
3. 机器人回复绑定成功后，返回登录器开始游戏。私聊时，机器人会查询该 QQ 是否属于至少一个允许群。

一个 QQ 只能绑定一个账号，一个账号只能绑定一个 QQ。绑定码为六位随机数字（保留开头的 0），有效期十分钟；重新获取使旧码失效，成功绑定后立即销毁。机器人指令和回复均不包含游戏账号。登录器中的账号密码仅用于验证归属。

登录器的 **修改密码** 需要填写原密码、新密码和确认密码。修改成功后使用新密码登录，原登录令牌和未完成的绑定码失效。登录、获取绑定码和改密共用账号验证次数：连续失败三次冷却一分钟，成功验证清零。冷却由服务端保存，重启登录器或服务不会清除；冷却中的请求不延长截止时间。

## 管理与默认配置

GM → **反作弊 → 账号与 QQ 号** 可逐条添加或移除允许群，点击右侧 **保存配置** 生效，并按完整账号、账号 ID 或 QQ 号查询绑定关系。

| 绑定方式 | `enabled` | `allowBinding` | 玩家行为 |
| --- | --- | --- | --- |
| 关闭绑定（默认） | false | false | 可直接登录，暂停新绑定 |
| 自愿绑定 | false | true | 可绑定，也可不绑定直接登录 |
| 必须绑定 | true | true | 绑定后才能登录 |

切换方式保留已有绑定关系。历史 `enabled=true` 的配置仍表示必须绑定。

登录器构建输入 [profile.example.json](../launcher/profile.example.json) 包含：

```json
{"EnableQQBinding": false, "RequireQQBinding": false}
```

`EnableQQBinding` 是编译输入开关：设为 `true` 后，登录器顶部与“开始游戏”“账号注册”“修改密码”并列显示“绑定 QQ”。默认构建隐藏该入口且不提供获取绑定码操作；服务器签名配置不能开启编译时关闭的功能。

`RequireQQBinding` 是登录策略的构建默认值。1.0.1 起，实际策略以验签后的服务器 profile 为准。自愿绑定使用 `EnableQQBinding=true`、`RequireQQBinding=false`，并在 GM 选择 **自愿绑定**。必须绑定时，GM 选择 **必须绑定**，Hub 发布 `RequireQQBinding=true` 的签名配置。恢复自愿或关闭时，将 GM 和签名 profile 的强制要求同时关闭。

其他服主从仓库构建时，保留示例中的两个 `false` 即可使用普通账号登录和修改密码，无需部署 QQ 机器人。群号、机器人凭据与正式 profile 使用各自部署的私有配置。

## QQ Agent 安装

适配 [QQ Agent](https://github.com/Kondius/qq-agent) 0.4 的确定性插件位于 [integrations/qq-agent](../integrations/qq-agent/)。固定指令由插件处理，不经过语言模型。其能力名为 `message.account-binding`；随附安装脚本将消费入口接到 OneBot 消息接收处，绑定指令不会写入 AI 会话历史。

1. 复制 `integrations/qq-agent/plugins/purplemagic-binding/` 到 QQ Agent 的 `plugins/`。
2. 执行 `python integrations/qq-agent/install_adapter.py /path/to/qqagent`。脚本核对入口并备份原文件，重复执行不重复插入。首次安装核心适配后重启 QQ Agent；后续插件文件可按 QQ Agent 的热加载配置更新。
3. 在同机 PlayerGateway 的私有配置中设置以下字段，生成独立随机凭据：

   ```json
   {
     "qq_bot_listen": "127.0.0.1:8782",
     "qq_bot_token": "REPLACE_WITH_A_SEPARATE_RANDOM_TOKEN_OF_AT_LEAST_32_CHARACTERS"
   }
   ```

4. 在 QQ Agent 的 **插件 → PurpleMagic 账号绑定** 填写本机接口 `http://127.0.0.1:8782` 和同一凭据，启用插件。允许群在 GM 内维护，机器人每次绑定读取当前配置。
5. 先部署游戏侧账号适配、PlayerGateway、GMService 和 `gm-console/dist`，再发布开启绑定入口的登录器，最后在 GM 保存允许群并选择绑定方式。机器人须已加入允许群，私聊绑定才可查询群成员。

插件仅访问回环接口。`GET /api/bot/qq-binding/settings` 读取群规则，`POST /api/bot/qq-binding/complete` 消费绑定码；该凭据不能调用 GM 管理操作。玩家监听不提供这两个路径。群号、QQ 关系、凭据、数据库和正式 profile 由部署方私有保存。

## 游戏服务接入契约

PurpleMagic 网关将以下玩家请求转发到配置的游戏账号 API：

| 路径 | 请求 | 结果 |
| --- | --- | --- |
| `POST /api/account/login` | `account,password,device,requireQQBinding` | 认证成功返回 `token,account_id,boundQQ`；仅强制绑定时未绑定返回 `qq_binding_required` |
| `POST /api/account/binding-code` | `account,password,device` | 返回 `bindingCode,expiresAt,groups`，已绑定返回 `boundQQ` |
| `POST /api/account/password` | `account,password,newPassword,device` | 校验原密码后定点更新密码摘要，撤销原令牌 |

失败响应包含 `ok:false,message,code`；冷却响应为 HTTP 429，附 `retryAfter` 秒数和 `Retry-After` 头。绑定码和密码只在专用请求体中传递，不写日志。

游戏私有 `POST /api/game/admin` 增加 `qq_settings`、`qq_settings_save`、`qq_bindings`、`qq_complete` 四个动作。类型见 [qqbinding/contract.go](../gateway/internal/modules/dnf/qqbinding/contract.go)。群规则用 revision 校验并发编辑；绑定写入需在同一事务中核对规则版本、允许群、绑定码和唯一约束。数据库只保存绑定码摘要及永久绑定关系。六位码通过摘要定位账号，绑定写入与删除有效码在同一事务完成；已使用码不可再次绑定。回复丢失时，返回登录器重新获取即可查看已绑定状态。每个 QQ 每十分钟最多尝试六次，限制持久化且不影响登录。

游戏项目需实现这些动作及认证校验；只部署 PurpleMagic 静态前端不会增加游戏账号能力。配套适配在账号表旁初始化 `_auth_failures`、`_qq_settings`、`_qq_codes`、`_qq_bindings` 与 `_qq_code_reservations` 增量表，默认禁用；原账号、规则和福利回执保持原值。

## 六位码升级顺序

先更新游戏账号服务，再更新 PlayerGateway 和 QQ Agent 插件，最后发布登录器。新增预留摘要表防止活跃码冲突；已消费或被替换的摘要保留至原截止时间，之后可复用数字。旧的账号加32位码指令在原有效期内兼容，旧登录器收到六位码后复制的指令也可用。新登录器只复制六位码指令。升级保持原绑定关系、允许群和自愿/必须/关闭策略。

## 验证

插件离线验证：`cd integrations/qq-agent && npm test`。测试通过伪造的 OneBot 事件和发送器覆盖群内艾特、私聊成员查询、重复消息和失败路径。账号侧需在一次性 MariaDB 中验证并发猜测、一分钟冷却、绑定唯一性、绑定码轮换与密码变更后令牌失效。

## 账号联合封禁

反作弊中点击封禁账号，可勾选 **最近登录 IP**、**注册 IP**、**踢出群聊并加入黑名单**。确认框列出实际 IP、绑定 QQ 和允许群，默认不勾选。服务器重新核对确认过的目标，记录变化时要求刷新确认；相同 IP 只处理一次，已有更长或永久的 IP 封禁保持。

账号、IP、QQ 黑名单和群处理任务在同一事务落库。机器人每 15 秒领取少量任务，逐群执行 `set_group_kick` 并设置 `reject_add_request=true`。机器人需要在对应群担任管理员或群主；权限不足和接口失败显示在封禁记录中，五分钟后重试，重启后继续。QQ 当前不在群内时保留黑名单，后续入群申请被拒绝；已入群事件也会触发移出。

QQ 黑名单持续生效。账号封禁到期或单独解除账号封禁不清除 IP 记录和 QQ 黑名单。

游戏账号库另增 `_qq_blacklist` 与 `_qq_actions` 表；私有动作增加 `ban_targets`、`qq_jobs`、`qq_ack`、`qq_blocked`。机器人凭据只允许查询群规则、提交绑定、领取已确认的群处理任务、写回任务结果和查询 QQ 黑名单，不能创建封禁。对应回环路径为 `/api/bot/qq-binding/{settings,complete,jobs,ack,blocked}`，玩家监听不提供这些路径。

账号预留连接和全部连接断开后的重连宽限均为20秒；仍有活动连接时保留会话。服务升级后，已有的十分钟验证冷却按原失败时间计算一分钟上限，不从升级时重新计时。
