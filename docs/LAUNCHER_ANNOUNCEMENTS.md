2026-11-04：GM 登录后可直接管理公告，无需额外运营管理 Token；见 [统一会话](GM_SESSION.md)。

# 登录器公告

[统一公告模板与 API 发布工具](GM_ANNOUNCEMENT_PUBLISHING.md)：按修复、调整、新增整理更新内容，使用 Python 脚本直接发布 Markdown，无需 RPA。

## 现行 GM 公告管理

在 GM 工作台“公告与下载源 → 公告管理”新增、编辑或删除公告。正文可选 Markdown 或纯文本，右侧切换“编辑 / 预览”；支持标题、强调、列表、引用、代码、表格、网页链接和图片。既有公告保留纯文本格式；可主动切换为 Markdown。每篇正文最多 64 KiB，标题最多 160 字。发布、删除及调整顺序沿用操作确认与独立回执；删除本地草稿不会删除已发布原文。

后台保存全部历史公告，按 20 条分页，可以搜索标题和正文。公告总数没有 30 条限制。玩家签名清单只同步列表最前面的近期公告（最多 30 条、JSON 最多 512 KiB），历史公告不会拖慢登录器检查更新。上移、下移调整发布顺序；删除近期公告后，下一条历史公告自动补入窗口。只修改历史条目时，玩家清单内容未变就不增加签名序号。

新登录器在单篇详情中渲染 Markdown，公告中心提供逐篇列表；旧登录器继续读取同一签名协议并显示正文文本。Markdown 不执行 HTML，只允许普通 HTTP/HTTPS 网页链接和 HTTPS 图片地址。本次不改登录、更新信任根或游戏通信。

### 历史存储与逐条接口

- 原 `published.news` 在第一次修改时迁入 Hub `state.json` 的 `newsArchive`；稳定条目 ID、原顺序、正文与分类保留。启动和只读请求不改现有签名清单。
- `GET /admin/api/state` 增加 `newsManager: true`、`newsTotal` 和完整历史的 `newsRevision`，`published.news` 仍为玩家窗口。
- `GET /admin/api/news?offset=0&limit=20&q=关键词` 返回 `rows / total / archiveTotal / next / revision`，最大单页 50 条；可带 `revision` 检测翻页期间变化。
- `POST /admin/api/news/entries`，请求体 `{action:"save", id:"", news:{title,body,category,date,format}}` 新增；带条目 ID 则编辑。`format` 为 `text` 或 `markdown`，省略按纯文本解释。
- 删除使用 `{action:"delete", id:"条目ID"}`；排序使用 `{action:"move", id:"条目ID", direction:"up"}`（或 `down`）。
- 写请求需要同源管理会话、`X-CSRF-Token`、`If-Match: "<revision>"` 和固定 `Idempotency-Key`。最近 128 个操作回执随历史原子保存；同编号同请求重试直接返回原结果，不重复新增或删除。更旧的请求仍受原修订值约束。
- 保存失败恢复内存中的历史、清单和回执；成功后重启保留。部署及备份应完整保留 Hub state，不能仅重建 `published` 字段。
- 旧 `PUT /admin/api/news` 数组接口继续兼容早期调用；存在窗口之外的历史时返回 409，提示使用新版逐条管理，避免旧编辑器覆盖未读取的历史。

以下为早期完整数组接口与原实现的历史说明。

在私有 Hub 的 `/gm/console/#gateway` 打开“网关与更新 → 登录器公告”。可新增、编辑、预览、删除和调整顺序，分类沿用登录器的“综合、更新、指南”。标题、显示日期和正文均为文本；正文保留换行。

编辑后点击“发布公告”保存。上移、下移会立即发布新的顺序，删除需要确认；预览与取消删除保持已发布内容。多个窗口同时编辑时，旧窗口收到冲突提示并保留草稿，通过“重新载入”获取最新公告。

登录器在检查更新时读取签名元数据中的公告，验签后更新列表和详情，并保存本地签名缓存。重启可显示已有缓存；下次成功检查更新后获取最新内容。页面加载与主动重新载入触发管理端读取，空闲时没有公告轮询。

## 接口

| 入口 | 用途 |
| --- | --- |
| `GET /admin/api/state` | 私有 Hub 会话读取 `published.news`、`newsRevision` 和 `csrf` |
| `PUT /admin/api/news` | 私有 Hub 发布完整公告数组，保留现有接口格式 |
| `GET /launcher/v2/bootstrap.json` | 玩家读取 RSA-PSS 签名元数据，其中包含 `news` |

GM 页面使用 GM 会话及 `X-CSRF-Token`，访问 `/api/v1/hub/`；下表 `/admin/api/` 为旧 Hub 管理器及自动化入口。发布时同时发送 `If-Match: "<newsRevision>"`，修订值由公告内容和顺序计算，与客户端版本发布的序号独立。

请求体示例：

```json
[
  {
    "title": "版本更新",
    "body": "本次更新内容\n第二行说明",
    "category": "更新",
    "date": "10 / 15"
  }
]
```

- 旧数组接口的完整 JSON 请求体上限为 128 KiB；不再按 30 条拒绝请求，标题/正文按上述现行限制校验。日常管理使用逐条接口。
- 内容变化且修订匹配时，持久保存数组并递增签名发布序号，返回 `{"ok":true,"newsRevision":"..."}`。
- 重复提交当前内容返回成功，序号保持，包括响应丢失后携带旧修订值重试的情况。
- 修订已过期且提交内容不同，返回 HTTP 409；保存失败返回 HTTP 500，并恢复当前内存中的发布内容。
- 空数组 `[]` 清空公告；`null`、未知字段、非法分类和超限内容返回 HTTP 400。

此候选的 GM 页面与 Hub 配套使用，Hub 提供 `newsRevision`。原 `/admin/` 公告编辑器也已发送修订值；既有调用方仍可沿用不带 `If-Match` 的完整数组接口。公开监听提供签名读取，管理操作位于私有监听。

## 验证

从项目根目录执行：

```text
python scripts/verify_component.py gateway
python scripts/verify_component.py launcher --dotnet <dotnet8>
python scripts/verify_integration.py announcements
python scripts/verify_integration.py news-interop --dotnet <dotnet8>
```

浏览器测试使用实际 Hub 和内嵌工作台，验证发布、多窗口冲突、排序、删除、预览不提交、暖色主题、移动布局与空闲请求数。跨语言测试从真实 Go 公告接口发布，经 TLS 固定证书和 RSA-PSS 验签进入 C# 登录器状态，并验证重启后的缓存读取。结果与截图保存在 `runtime/verification/`。
