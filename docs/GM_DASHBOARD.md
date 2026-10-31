# GM 仪表盘

第一侧栏现在是仪表盘。默认首页和 `#dashboard`、旧书签 `#query` 都打开它；账号与角色查询保留在独立模块，PVF 物品库在仪表盘快捷入口中。

布局参考 [CPA Manager Plus](https://github.com/seakee/CPA-Manager-Plus) 的仪表盘源码 `a67ed8834a914ab24c36d2490fa6bb6d4166724c`：概览与运营入口、指标卡片、趋势和热力图、公告。实现沿用 PurpleMagic 兰紫毛玻璃、深浅主题、密度与动效，没有引入 CPAMP 的组件、框架或运行依赖。CPAMP 采用 MIT 许可证。

## 数据口径

所有日期按北京时间（Asia/Shanghai，UTC+8），每周从周一开始。

| 控件 | 口径 |
| --- | --- |
| 总账号数 / 角色数 | 注册账号总数 / 当前未删除角色数 |
| 当前在线 | 完整游戏在线快照中的角色去重数 |
| 今日新注册 | 今日 00:00 至统计时刻创建的注册账号 |
| 今日日活 / 本周周活 | 对应时段成功登录过的账号，按账号去重；依据最后成功登录时间，非客户端页面访问量 |
| 注册趋势 / 热力图 | 近 14 天趋势 / 最近 364 个自然日注册数量，包含今天 |
| 疲劳消耗 | 今日已结算通关记录中的实际疲劳消耗；同时显示有疲劳记录的通关数，中途退出消耗不在此范围 |
| 副本通关 | 今日已结算的通关记录数 |
| 快速通关 | 今日耗时大于 0 且低于接口给定阈值的通关记录数；界面显示实际阈值，不自动判定作弊 |
| 最新公告 | 当前登录器发布公告按日期降序取前三条；没有日期的条目保持发布顺序 |

全服游戏内公告发送本轮未接入。现有公告入口管理登录器公告。

## 网关实现与部署

公开仓库的 `gateway/` 已包含统计实现。按现有部署流程构建并更新 PlayerGateway 与 `gm-console/dist`，即可使用仪表盘；外部 GMService 通过原有 v1 代理转发请求。统计接口仍使用管理监听和 GM 鉴权。

统计直接读取已配置的注册表和游戏数据库分片，无需新增游戏运行接口或数据库迁移。注册表沿用 `created_at`、`last_login_at` 的 UTC 时间契约；通关统计读取现有 `clear_history` 的 `finished_at`、`duration_ms`、`fatigue_spent`。缺少通关表或相关字段时省略整组通关/疲劳指标，保留可用的账号、角色统计。数据库适配范围与网关的现有 DNF90/ServerS5A3 仓储一致。

后端共享 60 秒缓存，并发读取合并为一次刷新；北京时间午夜使缓存失效，每次读取总预算 4 秒。每个角色分片只查询角色数及通关聚合，不读取存档正文或逐玩家明细。

实现入口：[`api_v1_dashboard.go`](../gateway/internal/services/gmtool/api_v1_dashboard.go)、[`DashboardReader`](../gateway/internal/modules/dnf/repository/dashboard.go) 和 [`mysql_dashboard.go`](../gateway/internal/modules/dnf/repository/mysql/mysql_dashboard.go)。在 `gateway/` 下执行 `python scripts/test_mysql.py --run Dashboard` 可用一次性 MariaDB 验证单/双分片、日期边界及旧表兼容。

## 兼容与负载

- `GET /api/v1/dashboard` 是可选、经过 GM 鉴权的聚合接口；旧服务返回 404/405/501 后，本次页面会话不再探测它。缺字段、null 或非法值对应的卡片隐藏，真实 0 正常显示。
- 在线快照使用现有 `GET /api/v1/presence`。公告只读取同源 HTTPS `/launcher/v2/bootstrap.json` 中的已发布文本，不写 Hub，不改变签名、资源、最低版本或发布序列；页面展示不作为登录器的签名验证结果。
- 页面每分钟刷新，隐藏标签页暂停，切页取消请求。浏览器不会分页拉取账号、角色或通关明细来凑统计。
- 统计服务应聚合后缓存，限制读超时及并发合并；权限错误返回登录流程。暂时失败保留有时间标记的旧结果，缺能力不显示业务错误。
- 游戏或接口能力未升级时，独立可用的在线、公告与运营入口继续显示。

## 后续增删控件

`gm-console/src/dashboard/model.ts` 是控件注册入口：

1. 调整 `dashboardLayout` 增删或重排一级区块，实际区块组件在 `pages/Dashboard.tsx` 的 `widgets` 中注册。
2. 调整 `dashboardMetrics` 增删、重排指标，定义标题、单位、说明、图标和跳转模块。
3. 扩展 `MetricKey`、数据解析及接口契约；解析器只接收有限非负安全整数。不要把缺失值转成 0。
4. 新的数据源在 `dashboard/client.ts` 中接入，保留取消、缺能力和登录失效语义；不要引入全量玩家列表读取。

接口示例使用合成数据：

```json
{
  "data": {
    "observedAt": "2026-10-31T02:00:00Z",
    "timezone": "Asia/Shanghai",
    "metrics": {
      "accounts": 20, "characters": 32, "registrationsToday": 2,
      "dailyActive": 8, "weeklyActive": 15,
      "clearsToday": 40, "fatigueToday": 240, "fastClearsToday": 1
    },
    "fastThresholdMs": 30000,
    "fatigueSamples": 40,
    "registrations": [{"date": "2026-10-31", "count": 2}]
  }
}
```

未采集疲劳的历史通关不参与疲劳总数；热力图服务明确返回真实零日期，遗漏日期显示为空白。通用统计实现随公开网关提供，前端继续兼容没有该接口的旧版本。
