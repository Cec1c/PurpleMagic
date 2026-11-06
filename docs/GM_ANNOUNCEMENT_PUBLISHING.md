# 公告模板与 API 发布

GM 已提供公告 API。Agent、开发者和脚本可直接发布 Markdown，使用现有 GM Token 登录，无需 RPA 或第二套运营 Token。公告保存后进入登录器签名清单，玩家下次成功检查更新时获取；发布公告不需要重启游戏服务。

## 按统一格式整理更新

沿用简洁的停机更新公告格式：正文以“更新内容：”开头，每条一件事，用“修复／调整／新增／优化”描述玩家能看到的变化。小批次直接列条目，条目较多时再按职业、副本、活动、系统分组。

| 类型 | 写法 |
| --- | --- |
| 修复 | 修复【场景】下，【对象】【异常表现】的问题。 |
| 调整 | 调整【规则】，由【原行为】改为【新行为】。 |
| 新增 | 新增【功能】，可通过【入口】进行【用途】。 |
| 优化 | 优化【操作或页面】的【具体体验】，说明可观察到的变化。 |
| 活动／商店 | 写明名称；道具单独列出名称、数量或点数、限购范围和次数。 |

例如“修复完成解锁任务后，辅助装备栏未开启的问题”，比“修复任务接口、事务与回执逻辑”更适合玩家公告。性能没有测量结果时写具体变化，例如“减少打开背包时重复加载数据的等待”，不写虚构的提升百分比。

整理时使用本批变更清单和发布回执：同一个问题的多个提交合并为一条；区分已上线、计划本次上线和仍待处理。停机通知可以介绍本次计划更新；“更新已完成”只写实际已生效内容。标题明确是游戏、登录器还是 GM 工作台更新，按受众选择条目。

版本、维护时间、数量、限购与补偿从已确认信息填写。无需维护时不写停机时间；没有补偿方案就省略补偿段。需要重登、重启登录器或下载补丁时，在末尾说明一次。公开正文省略数据库表名、内网地址、凭据、玩家个人资料和未公开的反作弊判定细节。

### 可复制模板

- [常规更新正文](templates/announcement-update.md)：已上线更新、在线修复、登录器／GM 更新。
- [停机维护正文](templates/announcement-maintenance.md)：已确定维护时间和范围的批次。

标题单独填在 GM 标题字段，例如“11月6日游戏更新公告”“11月6日 05:30 停机更新公告”或“GM 工作台更新公告”。正文不必重复标题。复制模板后删去没有对应内容的条目，替换所有 `{{占位内容}}`。

格式示例（用于说明写法，不代表一次实际发布）：

```markdown
更新内容：

* 修复完成解锁任务后，辅助装备栏未开启的问题。
* 修复部分物品强化或删除时，长时间等待后操作失败的问题。
* 优化邮件附件搜索，支持按名称模糊查找并显示物品 ID 和有效期。
* 新增账号黑钻开通与续期功能，可在 GM 工作台查看到期时间。
```

### 给其他 Agent 的任务描述

```text
请按 docs/GM_ANNOUNCEMENT_PUBLISHING.md 汇总本批更新公告。
输入：本批变更清单、实际或计划发布范围、版本、维护时间和生效方式。
选择 docs/templates/ 下的公告模板，逐条写成“修复／调整／新增／优化”。
合并同一问题的重复提交，保留玩家能理解的场景和变化，删除无关模板段。
输出标题、显示日期、分类“更新”和 Markdown 正文，并列出对应的内部变更依据。
内部依据单独放入交接记录，不写进玩家正文。
如本次任务已授权发布，使用 scripts/gm_announcement.py 的 prepare/publish 完成发布及回读；
仅要求整理模板或草稿时，交付草稿文件。
```

## 两条命令发布

要求 Python 3.10 或更高版本，仅使用标准库。在仓库根目录运行。先将填好的正文保存为 UTF-8 Markdown 文件，示例文件名 `runtime/announcements/update.md`。

```powershell
python scripts/gm_announcement.py prepare --base-url https://gm.example.com --title "11月6日游戏更新公告" --date "11 / 06" --body runtime/announcements/update.md --output runtime/announcements/update.request.json
python scripts/gm_announcement.py publish --request runtime/announcements/update.request.json
```

第一条仅在本地生成草稿并显示正文；第二条提示输入 GM Token，自动登录、获取 CSRF 与公告修订、发布并逐条回读。把 `https://gm.example.com` 换成自己的 GM 站点根地址，不附加 `/console/` 或 `#gateway`。输入的 Token 不显示，也不写入草稿或回执。

自动化可用 `--token-file <受保护文件路径>`，文件为纯文本 Token 或 `{"key":"..."}`；也可由任务环境注入 `PURPLEMAGIC_GM_TOKEN`。自建 CA 可传 `--ca-file <CA证书路径>`。凭据保留在私有文件或任务密钥中，文档和提交只记录文件引用。

请求文件保存标题、正文、目标站点、操作编号、首次修订和发布结果。连接中断时重跑原 `publish` 命令：它使用同一操作编号和首次修订；已有成功回执时只继续回读。HTTP 409 时核对当前公告，再为确认后的内容生成新草稿，脚本不会自动换修订重发。新增草稿使用不同输出路径，避免覆盖未完成请求。

编辑已有公告：先从列表读取条目 ID，`prepare` 添加 `--entry-id <ID>`，其余步骤相同。脚本只实现新增和编辑；删除、排序使用下述 API 或 GM 页面。

回执保存在原 `.request.json`：`published.id` 为条目 ID，`published.newsRevision` 为发布修订，`readback.verified` 和 `verifiedAt` 表示正文回读完成。发布成功后再次修改正文，应创建新草稿并传入条目 ID。

## HTTP 接口契约

推荐复用 GM 统一会话入口，外部 GMService 和一体 PlayerGateway 都提供相同路径。

| 顺序 | 请求 | 用途 |
| --- | --- | --- |
| 1 | `POST /api/auth/login` | `{"password":"GM Token","remember":false}`；保存响应 Cookie。 |
| 2 | `GET /api/v1/capabilities` | 从 `data.csrfToken` 读取 CSRF。 |
| 3 | `GET /api/v1/hub/state` | 确认 `newsManager: true`，读取顶层 `newsRevision`。 |
| 4 | `POST /api/v1/hub/news/entries` | 新增或编辑单篇公告。 |
| 5 | `GET /api/v1/hub/news?offset=0&limit=50&q=标题` | 按返回 ID 核对标题、正文、分类、日期、格式；`next >= 0` 时继续分页。 |
| 6 | `POST /api/auth/logout` | 请求体 `{}`，结束脚本创建的会话。 |

请求使用站点根地址作为 `Origin`，沿用 Cookie。写入还需要：

```http
Content-Type: application/json
X-CSRF-Token: <capabilities.data.csrfToken>
If-Match: "<newsRevision>"
Idempotency-Key: <本次操作固定的唯一编号>
```

新增正文：

```json
{
  "action": "save",
  "id": "",
  "news": {
    "title": "游戏更新公告",
    "body": "更新内容：\n\n* 修复完成解锁任务后，辅助装备栏未开启的问题。\n",
    "category": "更新",
    "date": "11 / 06",
    "format": "markdown"
  }
}
```

成功返回 `{"ok":true,"id":"条目ID","newsRevision":"新修订"}`。这里的 Hub 响应是顶层 JSON，不使用 GM 查询常见的 `data` 包装。编辑将 `id` 改为现有条目 ID；删除为 `{"action":"delete","id":"条目ID"}`；排序为 `{"action":"move","id":"条目ID","direction":"up"}` 或 `down`。

标题最多 160 字，正文最多 64 KiB UTF-8，日期最多 32 字节；分类为“综合／更新／指南”。完整历史分页存储，新公告置顶；登录器签名清单同步近期最多 30 条且最多 512 KiB JSON。Markdown 支持列表、标题、强调等，HTML 不执行。

旧自动化仍可通过私有 Hub `/admin/api/news/entries` 及原 Admin Token 会话发布。新工具优先使用上述 GM 路径；旧 `PUT /admin/api/news` 是完整数组替换接口，不适合逐篇追加。

本接口更新所连接 Hub 的公告。多 Hub／多通道环境应逐个确认发布目标；一次成功回执不表示其他独立 Hub 同时更新。公告 API 不等同于游戏内全服广播。

实现依据：[GM 统一会话](GM_SESSION.md)、[公告存储与登录器展示](LAUNCHER_ANNOUNCEMENTS.md)、[逐条写入实现](../gateway/internal/services/launcherhub/news_archive.go)。本工具不需要更新现有服务端。
