# QQ 账号绑定与密码管理

PurpleMagic 1.0.0 支持由 QQ 机器人确认玩家身份。开源配置默认关闭；单机、局域网和公网服都可独立选择是否启用。

## 玩家使用

1. 打开登录器，点击 **绑定 QQ**，填写游戏账号和密码，点击 **获取绑定码**。
2. 点击 **复制绑定指令**。在管理员配置的群内艾特机器人，或私聊机器人发送 `/绑定 账号 绑定码`。
3. 机器人回复绑定成功后，返回登录器开始游戏。私聊时，机器人会查询该 QQ 是否属于至少一个允许群。

一个 QQ 只能绑定一个账号，一个账号只能绑定一个 QQ。绑定码有效期十分钟；重新获取会替换旧码。密码不会发送给机器人。

登录器的 **修改密码** 需要填写原密码、新密码和确认密码。修改成功后使用新密码登录，原登录令牌和未完成的绑定码失效。登录、获取绑定码和改密共用账号验证次数：连续失败三次冷却十分钟，成功验证清零。冷却由服务端保存，重启登录器或服务不会清除；冷却中的请求不延长截止时间。

## 管理与默认配置

GM → **反作弊 → 账号与 QQ 号** 可保存允许群列表、打开登录绑定要求，并按完整账号、账号 ID 或 QQ 号查询绑定关系。配置启用后，游戏账号接口和游戏令牌准入都会核对绑定状态。

登录器构建输入 [profile.example.json](../launcher/profile.example.json) 包含：

```json
{"RequireQQBinding": false}
```

构建专用服登录器时设为 `true`，并在当前 Hub profile 中同步配置。登录器会显示绑定入口，要求账号服务返回已验证的绑定结果。服务端的启用开关仍在 GM；开启后旧登录器也需要绑定账号。发布带绑定功能的新登录器后，再启用本服要求。

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
5. 先部署游戏侧账号适配、PlayerGateway、GMService 和 `gm-console/dist`，再发布登录器，最后在 GM 保存允许群并启用。机器人须已加入允许群，私聊绑定才可查询群成员。

插件仅访问回环接口。`GET /api/bot/qq-binding/settings` 读取群规则，`POST /api/bot/qq-binding/complete` 消费绑定码；该凭据不能调用 GM 管理操作。玩家监听不提供这两个路径。群号、QQ 关系、凭据、数据库和正式 profile 由部署方私有保存。

## 游戏服务接入契约

PurpleMagic 网关将以下玩家请求转发到配置的游戏账号 API：

| 路径 | 请求 | 结果 |
| --- | --- | --- |
| `POST /api/account/login` | `account,password,device,requireQQBinding` | 认证成功返回 `token,account_id,boundQQ`；未绑定返回 `qq_binding_required` |
| `POST /api/account/binding-code` | `account,password,device` | 返回 `bindingCode,expiresAt,groups`，已绑定返回 `boundQQ` |
| `POST /api/account/password` | `account,password,newPassword,device` | 校验原密码后定点更新密码摘要，撤销原令牌 |

失败响应包含 `ok:false,message,code`；冷却响应为 HTTP 429，附 `retryAfter` 秒数和 `Retry-After` 头。绑定码和密码只在专用请求体中传递，不写日志。

游戏私有 `POST /api/game/admin` 增加 `qq_settings`、`qq_settings_save`、`qq_bindings`、`qq_complete` 四个动作。类型见 [qqbinding/contract.go](../gateway/internal/modules/dnf/qqbinding/contract.go)。群规则用 revision 校验并发编辑；绑定写入需在同一事务中核对规则版本、允许群、绑定码和唯一约束。数据库保存绑定码摘要及永久绑定关系，重复消息只返回同一结果。

游戏项目需实现这些动作及认证校验；只部署 PurpleMagic 静态前端不会增加游戏账号能力。配套适配在账号表旁初始化 `_auth_failures`、`_qq_settings`、`_qq_codes`、`_qq_bindings` 四张增量表，默认禁用；原账号、规则和福利回执保持原值。

## 验证

插件离线验证：`cd integrations/qq-agent && npm test`。测试通过伪造的 OneBot 事件和发送器覆盖群内艾特、私聊成员查询、重复消息和失败路径。账号侧需在一次性 MariaDB 中验证并发猜测、十分钟冷却、绑定唯一性、绑定码轮换与密码变更后令牌失效。

## 账号联合封禁

反作弊中点击封禁账号，可勾选 **最近登录 IP**、**注册 IP**、**踢出群聊并加入黑名单**。确认框列出实际 IP、绑定 QQ 和允许群，默认不勾选。服务器重新核对确认过的目标，记录变化时要求刷新确认；相同 IP 只处理一次，已有更长或永久的 IP 封禁保持。

账号、IP、QQ 黑名单和群处理任务在同一事务落库。机器人每 15 秒领取少量任务，逐群执行 `set_group_kick` 并设置 `reject_add_request=true`。机器人需要在对应群担任管理员或群主；权限不足和接口失败显示在封禁记录中，五分钟后重试，重启后继续。QQ 当前不在群内时保留黑名单，后续入群申请被拒绝；已入群事件也会触发移出。

QQ 黑名单持续生效。账号封禁到期或单独解除账号封禁不清除 IP 记录和 QQ 黑名单。

游戏账号库另增 `_qq_blacklist` 与 `_qq_actions` 表；私有动作增加 `ban_targets`、`qq_jobs`、`qq_ack`、`qq_blocked`。机器人凭据只允许查询群规则、提交绑定、领取已确认的群处理任务、写回任务结果和查询 QQ 黑名单，不能创建封禁。对应回环路径为 `/api/bot/qq-binding/{settings,complete,jobs,ack,blocked}`，玩家监听不提供这些路径。
