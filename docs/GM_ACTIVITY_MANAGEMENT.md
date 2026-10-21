# 游戏活动管理

在 GM 侧栏打开「活动管理」（`/console/#activities`），即可管理游戏服务提供的全服活动。页面独立于角色选择，沿用兰紫毛玻璃主题、固定加载圈和操作确认窗口。

## 操作方式

1. 按名称、ID 或说明搜索，或筛选配置开启、配置关闭、等待重启的活动。
2. 点击单行「开启／关闭」，或勾选多个活动后点击「批量开启／批量关闭」。全选只选择当前筛选结果，切换筛选会保留已选项；「清空选择」取消全部选择。
3. 核对确认窗口中的活动名称、ID 和前后状态，确认一次保存整批配置。已经处于目标状态的活动会自动跳过。
4. 保存完成后查看最新配置版本、实际状态和生效情况。点击活动名称可查看完整说明、默认配置和活动来源。

| 字段 | 含义 |
| --- | --- |
| 配置状态 | 持久化的活动开关，供游戏服务下次启动加载 |
| 实际状态 | 当前游戏进程已经加载的活动状态 |
| 等待重启 | 配置或显式开关策略与当前进程不同，在游戏服务重启后生效 |
| 配置版本 | 游戏服务提供的整组活动版本，批量保存时用来检查并发修改 |

游戏服务下次重启时加载已保存的配置，GM 保存按钮只提交配置。列表来自本服当前游戏服务的活动目录；奖励、时间及活动参数以活动说明和游戏实现为准，本页当前提供启停操作。

## 冲突与操作恢复

选择操作时读取最新快照，确认时再次核对版本。版本变化会刷新页面并要求按当前状态重新选择，原确认不会自动换成新版本覆盖他人的配置。

提交时保留确认内容和已尝试标记。保存响应丢失后先回读：若版本增加且本批全部目标开关一致，显示已保存；若状态不同，显示版本冲突；若原版本未变，保留操作供继续查询／重试。刷新页面后可从顶部操作入口继续。此恢复依据当前配置状态，活动开关无需发放型永久回执。

## 接口与调用链

浏览器同源管理会话 → 外部 GMService → PlayerGateway 私有管理监听 → 游戏服务 `POST /api/game/admin`。游戏服务负责活动目录、存储和版本比较；活动配置不写入角色背包、邮箱或福利规则。

| 接口 | 用途 |
| --- | --- |
| `GET /api/v1/capabilities` | `activities.read` / `activities.write` 表示当前活动读写能力 |
| `GET /api/v1/activities` | 返回整组快照：`revision`、`restartRequired` 和 `activities` |
| `POST /api/v1/activities` | 校验并一次保存本批开关，返回保存后的完整快照 |

保存请求示例：

```json
{
  "revision": 12,
  "activities": [
    { "id": 145, "enabled": true },
    { "id": 2395, "enabled": false }
  ]
}
```

请求使用管理 Cookie 会话及 `X-CSRF-Token`，公共玩家监听不提供此路由。接口要求当前版本、1–256 个唯一且受支持的活动 ID 和显式布尔开关；未知 ID、重复项或错误字段会拒绝整批。保存复用游戏服务的 `save_activities` 动作和已有设置存储，无 schema 迁移。

`409 revision_conflict` 表示配置已变化；`503 activity_save_unconfirmed` 表示尚未取得保存结果，应先 GET 回读；`503 activities_unavailable` 表示游戏活动读取暂不可用。诊断阶段包含 `activities.read`、`activities.save`，成功日志记录配置版本、变化数量和待重启状态。

## 验证入口

- `gm-console/tests/activities.test.mjs`：全局操作、精确批次、一次确认、并发冲突、保存丢响应和刷新恢复。
- `gateway/internal/services/gmtool/api_v1_activities_test.go`：认证／CSRF、严格请求校验、整批保存、保留默认策略、私有游戏 HTTP 和外部 GM 转发。
- `gateway/cmd/playergateway/management_test.go`：管理路由与玩家监听隔离。
- `gateway/internal/modules/dnf/onlineevent/controls_test.go`：活动配置存储与重启加载契约。
