# 当前接口目录

由`python scripts/catalog_management_routes.py`从ManagementHandler、v1Routes、v1OperationRoutes提取。仅登记当前注册的HTTP方法和路径；字段schema、资格和部署状态另见对应功能文档。

当前私有管理注册共96条。字段与验证范围见对应功能文档。旧gmtool.go中的旧接口未纳入当前管理入口。

## 私有GM接口

| 方法 | 路径 | 处理器 | 来源 |
| --- | --- | --- | --- |
| POST | `/api/auth/login` | `s.handleLogin` | [managed.go:55](../gateway/internal/services/gmtool/managed.go#L55) |
| POST | `/api/auth/logout` | `s.handleLogout` | [managed.go:56](../gateway/internal/services/gmtool/managed.go#L56) |
| GET | `/api/status` | `func(w http.ResponseWriter, r *http.Request) {` | [managed.go:52](../gateway/internal/services/gmtool/managed.go#L52) |
| GET | `/api/v1/accounts` | `s.v1Accounts` | [api_v1.go:103](../gateway/internal/services/gmtool/api_v1.go#L103) |
| GET | `/api/v1/accounts/{accountId}` | `s.v1Account` | [api_v1.go:104](../gateway/internal/services/gmtool/api_v1.go#L104) |
| GET | `/api/v1/accounts/{accountId}/characters` | `s.v1AccountCharacters` | [api_v1.go:105](../gateway/internal/services/gmtool/api_v1.go#L105) |
| GET | `/api/v1/accounts/{accountId}/currency` | `s.v1Currency` | [api_v1.go:73](../gateway/internal/services/gmtool/api_v1.go#L73) |
| GET | `/api/v1/accounts/{accountId}/currency-adjustments` | `s.v1CurrencyLog` | [api_v1.go:98](../gateway/internal/services/gmtool/api_v1.go#L98) |
| POST | `/api/v1/accounts/{accountId}/currency-adjustments` | `s.v1AdjustCurrency` | [api_v1.go:99](../gateway/internal/services/gmtool/api_v1.go#L99) |
| GET | `/api/v1/accounts/{accountId}/inventory-operations` | `s.v1InventoryOperationLog` | [api_v1.go:63](../gateway/internal/services/gmtool/api_v1.go#L63) |
| POST | `/api/v1/accounts/{accountId}/password-reset` | `s.v1ResetPassword` | [api_v1.go:127](../gateway/internal/services/gmtool/api_v1.go#L127) |
| GET | `/api/v1/accounts/{accountId}/vault` | `s.v1Vault` | [api_v1.go:69](../gateway/internal/services/gmtool/api_v1.go#L69) |
| GET | `/api/v1/accounts/{accountId}/vault-operations` | `s.v1VaultLog` | [api_v1.go:70](../gateway/internal/services/gmtool/api_v1.go#L70) |
| POST | `/api/v1/accounts/{accountId}/vault-operations` | `s.v1VaultOperation` | [api_v1.go:72](../gateway/internal/services/gmtool/api_v1.go#L72) |
| POST | `/api/v1/accounts/{accountId}/vault-operations/preview` | `s.v1VaultOperation` | [api_v1.go:71](../gateway/internal/services/gmtool/api_v1.go#L71) |
| GET | `/api/v1/activities` | `s.v1Activities` | [api_v1.go:128](../gateway/internal/services/gmtool/api_v1.go#L128) |
| POST | `/api/v1/activities` | `s.v1SaveActivities` | [api_v1.go:129](../gateway/internal/services/gmtool/api_v1.go#L129) |
| GET | `/api/v1/anti-cheat/ban-targets/{account}` | `s.v1BanTargets` | [api_v1.go:123](../gateway/internal/services/gmtool/api_v1.go#L123) |
| POST | `/api/v1/anti-cheat/bans` | `s.v1Moderation` | [api_v1.go:126](../gateway/internal/services/gmtool/api_v1.go#L126) |
| GET | `/api/v1/anti-cheat/qq/bindings` | `s.v1QQBinding` | [api_v1.go:125](../gateway/internal/services/gmtool/api_v1.go#L125) |
| GET | `/api/v1/anti-cheat/qq/settings` | `s.v1QQBinding` | [api_v1.go:122](../gateway/internal/services/gmtool/api_v1.go#L122) |
| POST | `/api/v1/anti-cheat/qq/settings` | `s.v1QQBinding` | [api_v1.go:124](../gateway/internal/services/gmtool/api_v1.go#L124) |
| GET | `/api/v1/anti-cheat/{resource}` | `s.v1Moderation` | [api_v1.go:121](../gateway/internal/services/gmtool/api_v1.go#L121) |
| GET | `/api/v1/benefits` | `s.v1Campaigns` | [api_v1_operations.go:27](../gateway/internal/services/gmtool/api_v1_operations.go#L27) |
| POST | `/api/v1/benefits` | `s.v1CreateCampaign` | [api_v1_operations.go:34](../gateway/internal/services/gmtool/api_v1_operations.go#L34) |
| GET | `/api/v1/benefits/{id}` | `s.v1Campaign` | [api_v1_operations.go:28](../gateway/internal/services/gmtool/api_v1_operations.go#L28) |
| POST | `/api/v1/benefits/{id}/delete` | `s.v1CampaignDelete` | [api_v1_operations.go:36](../gateway/internal/services/gmtool/api_v1_operations.go#L36) |
| POST | `/api/v1/benefits/{id}/preview` | `s.v1CampaignPreview` | [api_v1_operations.go:37](../gateway/internal/services/gmtool/api_v1_operations.go#L37) |
| POST | `/api/v1/benefits/{id}/pvf-apply` | `s.v1CampaignPVFApply` | [api_v1_operations.go:25](../gateway/internal/services/gmtool/api_v1_operations.go#L25) |
| GET | `/api/v1/benefits/{id}/pvf-history` | `s.v1CampaignPVFHistory` | [api_v1_operations.go:23](../gateway/internal/services/gmtool/api_v1_operations.go#L23) |
| POST | `/api/v1/benefits/{id}/pvf-preview` | `s.v1CampaignPVFPreview` | [api_v1_operations.go:24](../gateway/internal/services/gmtool/api_v1_operations.go#L24) |
| GET | `/api/v1/benefits/{id}/recipients` | `s.v1CampaignRecipients` | [api_v1_operations.go:29](../gateway/internal/services/gmtool/api_v1_operations.go#L29) |
| POST | `/api/v1/benefits/{id}/state` | `s.v1CampaignState` | [api_v1_operations.go:35](../gateway/internal/services/gmtool/api_v1_operations.go#L35) |
| GET | `/api/v1/capabilities` | `s.v1Capabilities` | [api_v1.go:100](../gateway/internal/services/gmtool/api_v1.go#L100) |
| GET | `/api/v1/characters` | `s.v1Characters` | [api_v1.go:106](../gateway/internal/services/gmtool/api_v1.go#L106) |
| GET | `/api/v1/characters/{characterId}` | `s.v1Character` | [api_v1.go:107](../gateway/internal/services/gmtool/api_v1.go#L107) |
| GET | `/api/v1/characters/{characterId}/container-operations` | `s.v1ContainerLog` | [api_v1.go:75](../gateway/internal/services/gmtool/api_v1.go#L75) |
| POST | `/api/v1/characters/{characterId}/container-operations` | `s.v1ContainerOperation` | [api_v1.go:77](../gateway/internal/services/gmtool/api_v1.go#L77) |
| POST | `/api/v1/characters/{characterId}/container-operations/preview` | `s.v1ContainerOperation` | [api_v1.go:76](../gateway/internal/services/gmtool/api_v1.go#L76) |
| GET | `/api/v1/characters/{characterId}/containers` | `s.v1Container` | [api_v1.go:74](../gateway/internal/services/gmtool/api_v1.go#L74) |
| GET | `/api/v1/characters/{characterId}/expert-job` | `s.v1ExpertJob` | [api_v1.go:86](../gateway/internal/services/gmtool/api_v1.go#L86) |
| GET | `/api/v1/characters/{characterId}/expert-job-operations` | `s.v1ExpertJobLog` | [api_v1.go:89](../gateway/internal/services/gmtool/api_v1.go#L89) |
| POST | `/api/v1/characters/{characterId}/expert-job-operations` | `s.v1ExpertJobOperation` | [api_v1.go:88](../gateway/internal/services/gmtool/api_v1.go#L88) |
| POST | `/api/v1/characters/{characterId}/expert-job-operations/preview` | `s.v1ExpertJobOperation` | [api_v1.go:87](../gateway/internal/services/gmtool/api_v1.go#L87) |
| GET | `/api/v1/characters/{characterId}/gold` | `s.v1Gold` | [api_v1.go:78](../gateway/internal/services/gmtool/api_v1.go#L78) |
| GET | `/api/v1/characters/{characterId}/gold-operations` | `s.v1GoldLog` | [api_v1.go:79](../gateway/internal/services/gmtool/api_v1.go#L79) |
| POST | `/api/v1/characters/{characterId}/gold-operations` | `s.v1GoldOperation` | [api_v1.go:97](../gateway/internal/services/gmtool/api_v1.go#L97) |
| POST | `/api/v1/characters/{characterId}/gold-operations/preview` | `s.v1GoldOperation` | [api_v1.go:96](../gateway/internal/services/gmtool/api_v1.go#L96) |
| GET | `/api/v1/characters/{characterId}/inventory` | `s.v1Inventory` | [api_v1.go:108](../gateway/internal/services/gmtool/api_v1.go#L108) |
| GET | `/api/v1/characters/{characterId}/inventory-operations` | `s.v1InventoryOperationLog` | [api_v1.go:62](../gateway/internal/services/gmtool/api_v1.go#L62) |
| POST | `/api/v1/characters/{characterId}/inventory-operations` | `s.v1InventoryOperation` | [api_v1.go:65](../gateway/internal/services/gmtool/api_v1.go#L65) |
| POST | `/api/v1/characters/{characterId}/inventory-operations/preview` | `s.v1InventoryOperation` | [api_v1.go:64](../gateway/internal/services/gmtool/api_v1.go#L64) |
| GET | `/api/v1/characters/{characterId}/inventory/{container}/{slot}` | `s.v1InventoryDetail` | [api_v1.go:109](../gateway/internal/services/gmtool/api_v1.go#L109) |
| GET | `/api/v1/characters/{characterId}/inventory/{container}/{slot}/options` | `s.v1InventoryOptions` | [api_v1.go:61](../gateway/internal/services/gmtool/api_v1.go#L61) |
| GET | `/api/v1/characters/{characterId}/level` | `s.v1Level` | [api_v1.go:81](../gateway/internal/services/gmtool/api_v1.go#L81) |
| GET | `/api/v1/characters/{characterId}/level-operations` | `s.v1LevelLog` | [api_v1.go:92](../gateway/internal/services/gmtool/api_v1.go#L92) |
| POST | `/api/v1/characters/{characterId}/level-operations` | `s.v1LevelOperation` | [api_v1.go:91](../gateway/internal/services/gmtool/api_v1.go#L91) |
| POST | `/api/v1/characters/{characterId}/level-operations/preview` | `s.v1LevelOperation` | [api_v1.go:90](../gateway/internal/services/gmtool/api_v1.go#L90) |
| GET | `/api/v1/characters/{characterId}/mailbox` | `s.v1Mailbox` | [api_v1.go:118](../gateway/internal/services/gmtool/api_v1.go#L118) |
| GET | `/api/v1/characters/{characterId}/mailbox-cleanups` | `s.v1MailboxCleanups` | [api_v1.go:111](../gateway/internal/services/gmtool/api_v1.go#L111) |
| POST | `/api/v1/characters/{characterId}/mailbox-cleanups` | `s.v1CreateMailboxCleanup` | [api_v1.go:114](../gateway/internal/services/gmtool/api_v1.go#L114) |
| GET | `/api/v1/characters/{characterId}/mailbox-cleanups/{cleanupId}` | `s.v1MailboxCleanup` | [api_v1.go:112](../gateway/internal/services/gmtool/api_v1.go#L112) |
| GET | `/api/v1/characters/{characterId}/mailbox-cleanups/{cleanupId}/items` | `s.v1MailboxCleanupItems` | [api_v1.go:113](../gateway/internal/services/gmtool/api_v1.go#L113) |
| POST | `/api/v1/characters/{characterId}/mailbox-cleanups/{cleanupId}/{action}` | `s.v1AdvanceMailboxCleanup` | [api_v1.go:115](../gateway/internal/services/gmtool/api_v1.go#L115) |
| GET | `/api/v1/characters/{characterId}/mailbox-operations` | `s.v1MailboxOperationLog` | [api_v1.go:110](../gateway/internal/services/gmtool/api_v1.go#L110) |
| POST | `/api/v1/characters/{characterId}/mailbox-operations` | `s.v1MailboxOperation` | [api_v1.go:117](../gateway/internal/services/gmtool/api_v1.go#L117) |
| POST | `/api/v1/characters/{characterId}/mailbox-operations/preview` | `s.v1MailboxOperation` | [api_v1.go:116](../gateway/internal/services/gmtool/api_v1.go#L116) |
| GET | `/api/v1/characters/{characterId}/mailbox/{mailId}` | `s.v1MailboxDetail` | [api_v1.go:119](../gateway/internal/services/gmtool/api_v1.go#L119) |
| GET | `/api/v1/characters/{characterId}/profession` | `s.v1Profession` | [api_v1.go:82](../gateway/internal/services/gmtool/api_v1.go#L82) |
| GET | `/api/v1/characters/{characterId}/profession-operations` | `s.v1ProfessionLog` | [api_v1.go:85](../gateway/internal/services/gmtool/api_v1.go#L85) |
| POST | `/api/v1/characters/{characterId}/profession-operations` | `s.v1ProfessionOperation` | [api_v1.go:84](../gateway/internal/services/gmtool/api_v1.go#L84) |
| POST | `/api/v1/characters/{characterId}/profession-operations/preview` | `s.v1ProfessionOperation` | [api_v1.go:83](../gateway/internal/services/gmtool/api_v1.go#L83) |
| GET | `/api/v1/characters/{characterId}/quest-operations` | `s.v1QuestOperationLog` | [api_v1.go:66](../gateway/internal/services/gmtool/api_v1.go#L66) |
| POST | `/api/v1/characters/{characterId}/quest-operations` | `s.v1QuestOperation` | [api_v1.go:68](../gateway/internal/services/gmtool/api_v1.go#L68) |
| POST | `/api/v1/characters/{characterId}/quest-operations/preview` | `s.v1QuestOperation` | [api_v1.go:67](../gateway/internal/services/gmtool/api_v1.go#L67) |
| GET | `/api/v1/characters/{characterId}/quests` | `s.v1CharacterQuests` | [api_v1.go:120](../gateway/internal/services/gmtool/api_v1.go#L120) |
| GET | `/api/v1/characters/{characterId}/skill-point-operations` | `s.v1SkillPointLog` | [api_v1.go:93](../gateway/internal/services/gmtool/api_v1.go#L93) |
| POST | `/api/v1/characters/{characterId}/skill-point-operations` | `s.v1SkillPointOperation` | [api_v1.go:95](../gateway/internal/services/gmtool/api_v1.go#L95) |
| POST | `/api/v1/characters/{characterId}/skill-point-operations/preview` | `s.v1SkillPointOperation` | [api_v1.go:94](../gateway/internal/services/gmtool/api_v1.go#L94) |
| GET | `/api/v1/characters/{characterId}/skill-points` | `s.v1SkillPoints` | [api_v1.go:80](../gateway/internal/services/gmtool/api_v1.go#L80) |
| GET | `/api/v1/dashboard` | `s.v1Dashboard` | [api_v1.go:102](../gateway/internal/services/gmtool/api_v1.go#L102) |
| GET | `/api/v1/items` | `s.v1Items` | [api_v1.go:134](../gateway/internal/services/gmtool/api_v1.go#L134) |
| GET | `/api/v1/items/{itemId}` | `s.v1ItemDetail` | [api_v1.go:135](../gateway/internal/services/gmtool/api_v1.go#L135) |
| GET | `/api/v1/items/{itemId}/grant-options` | `s.v1MailGrantOptions` | [api_v1_operations.go:26](../gateway/internal/services/gmtool/api_v1_operations.go#L26) |
| POST | `/api/v1/mail/online-preview` | `s.v1OnlineMailPreview` | [api_v1_operations.go:39](../gateway/internal/services/gmtool/api_v1_operations.go#L39) |
| POST | `/api/v1/mail/preview` | `s.v1MailPreview` | [api_v1_operations.go:38](../gateway/internal/services/gmtool/api_v1_operations.go#L38) |
| GET | `/api/v1/presence` | `s.v1Presence` | [api_v1.go:101](../gateway/internal/services/gmtool/api_v1.go#L101) |
| GET | `/api/v1/quests` | `s.v1Quests` | [api_v1.go:132](../gateway/internal/services/gmtool/api_v1.go#L132) |
| GET | `/api/v1/quests/{questId}` | `s.v1QuestDetail` | [api_v1.go:133](../gateway/internal/services/gmtool/api_v1.go#L133) |
| GET | `/api/v1/settings/pvf` | `s.v1PVFMode` | [api_v1.go:130](../gateway/internal/services/gmtool/api_v1.go#L130) |
| POST | `/api/v1/settings/pvf` | `s.v1PVFMode` | [api_v1.go:131](../gateway/internal/services/gmtool/api_v1.go#L131) |
| GET | `/api/v1/tasks` | `s.v1Tasks` | [api_v1_operations.go:30](../gateway/internal/services/gmtool/api_v1_operations.go#L30) |
| GET | `/api/v1/tasks/{id}` | `s.v1Task` | [api_v1_operations.go:31](../gateway/internal/services/gmtool/api_v1_operations.go#L31) |
| GET | `/api/v1/tasks/{id}/confirmation` | `s.v1Task` | [api_v1_operations.go:32](../gateway/internal/services/gmtool/api_v1_operations.go#L32) |
| GET | `/api/v1/tasks/{id}/recipients` | `s.v1Recipients` | [api_v1_operations.go:33](../gateway/internal/services/gmtool/api_v1_operations.go#L33) |
| POST | `/api/v1/tasks/{id}/run` | `s.v1RunTask` | [api_v1_operations.go:40](../gateway/internal/services/gmtool/api_v1_operations.go#L40) |

## 玩家入口与Hub

以下依据`playergateway/main.go`、`launcherhub/admin.go`、`hub.go`和`bundle.go`的条件分派人工核对；不是上述Go注册扫描器的输出。

| 表面 | 方法/路径 | 用途 |
| --- | --- | --- |
| 玩家 | POST `/api/account/login`、`/api/account/register` | 白名单代理到游戏账号API |
| 玩家 | GET `/api/status` | 游戏状态白名单代理 |
| 玩家 | GET/HEAD `/launcher/v2/bootstrap.json` | 签名更新与公告元数据 |
| 玩家 | GET/HEAD `/launcher/v2/bundles/{sha256}.zip` | 已缓存安装包 |
| 玩家 | GET/HEAD `/launcher/*`白名单文件 | 兼容旧签名更新文件；精确文件正则见main.go |
| Hub私有 | POST `/admin/api/login`、`/admin/api/logout` | Hub管理会话 |
| Hub私有 | GET `/admin/api/state` | 发布/源/公告状态及修订 |
| Hub私有 | PUT `/admin/api/source` | 更新源配置 |
| Hub私有 | POST `/admin/api/check`、`/admin/api/publish` | 检查并发布 |
| Hub私有 | PUT `/admin/api/news` | If-Match+CSRF公告更新 |
| Hub私有 | POST `/admin/api/bundle/{version}` | 安装包上传 |

Hub与GM有独立会话处理；`/console/`公告页面整合尚未完成。公开玩家路径与私有管理路径分属不同监听。

## 网关到游戏服务

`POST /api/game/admin`为网关内部配置的运行适配器，版本1 JSON，Bearer鉴权。现有管理使用character_edit_status、account_edit_status、online_snapshot和catalog；详细字段见[协议](../gateway/internal/modules/dnf/adminapi/protocol.go)。角色／账号准入协议见GM_EDIT_CONCURRENCY.md；角色协议联合上线、多游戏进程汇总、通关采集和会话释放继续接入。

## API完善工作

本目录是当前实际路由清单，不是完整OpenAPI规范。下一轮统一请求/响应schema、错误码、权限、能力协商、批次限制及幂等重放语义，并以实际接口生成客户端契约。
