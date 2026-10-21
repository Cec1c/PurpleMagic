# GM 请求与福利投递诊断

本规范覆盖外部 GM、私有 PlayerGateway、福利扫描与邮件/补发队列。日志用于定位失败阶段；发放结果仍以永久回执、余额与邮件读回为准。

## 请求关联

- 每个服务生成独立 128 位 `request_id`。外部请求生成 `correlation_id`，私有客户端传递 `X-Correlation-ID` 和 `X-Parent-Request-ID`。网关验证服务身份后才接纳这些字段。
- `X-Request-ID` 保留原代理行为：转发请求返回网关编号；本地请求返回 GM 编号。`X-Correlation-ID` 在两端一致。GM 完成日志同时包含本地编号和 `peer_request_id`。
- 后台每次尝试有独立请求编号。补发、邮件带持久 `job_id`，自动扫描带单轮 `cycle_id`，经私有请求头传至网关；重启恢复沿用队列作业编号，扫描重新起一轮。
- 请求编号不参与幂等、规则版本、游标或回执键的计算。原队列文件格式保持。

## 完成日志与容量

`gm-request-complete` 包含 `service`、源码 `version`、HTTP 方法、路由模板、状态、业务错误码、总耗时、响应字节数、关联编号和阶段摘要。私有认证失败也记录；公开请求头不能伪造父关联。未知路径片段只记 `{id}`，不记录查询参数。

| 设置 | 默认 | 合法范围 |
| --- | --- | --- |
| `PURPLEMAGIC_LOG_SLOW_MS` | 1000 毫秒 | 1–60000 |
| `PURPLEMAGIC_LOG_SUCCESS_EVERY` | 每 100 次快速成功记录一次 | 1–100000 |

错误、取消、超时、慢请求全部记录。快速成功按计数采样；故障复现时可暂设成功采样为 1。每请求最多 32 个阶段、24 个上下文字段，同名阶段聚合次数、总毫秒和错误次数；超限阶段只累加 `stages_dropped`，不持久保留请求对象。

日志继续使用服务原有 logger/stdout，不建立无界内存日志缓存。运行日志的磁盘保留由部署配置控制；推荐两个服务使用独立 `purplemagic` journal namespace，单主机 128 MiB、最长 7 天。容量先到时会更早清理；查看该日志须使用 `journalctl --namespace=purplemagic`。样例位于 `deploy/gm-diagnostics/`，部署时一并核对 namespace 支持、输出和清理策略。未应用样例前仍沿用原 journal 配置。

构建脚本把完整源码提交注入 `diagnostics.BuildVersion`，脏树增加 `-dirty`。从源码归档构建时传 `python scripts/build_gateway.py linux --source-version <完整提交>`。未提供版本的非 Git 归档显示 `source-archive`。

## 阶段含义

| 阶段 | 实际测量范围 |
| --- | --- |
| `*.connection` | 获取专用数据库连接，含连接池等待、新连接建立与驱动验证 |
| `*.query_rows` | 发出查询至关闭结果集，含执行、数据库锁等待、网络传输和行读取 |
| `accounts.characters` / `benefits.characters` | 角色摘要查询与返回数据处理 |
| `benefits.account_receipts` | 账号基础信息和元数据/永久回执读取 |
| `accounts.assembly` / `benefits.assembly` | API 返回结构组装 |
| `delivery.character_locked_read` / `delivery.account_locked_read` | 事务内带锁读取完整角色/账号，含该阶段查询与对象构建 |
| `delivery.apply` | 回执去重、奖励计算、邮件追加和账号字段写入 |
| `delivery.mail_append` / `wallet_write` / `receipt_write` / `commit` | 对应事务步骤；不改变原有提交顺序 |
| `peer.response_headers` | 私有 HTTP 请求开始至收到响应头，含客户端连接、网络和对端处理 |
| `peer.response_body` | 有界响应读取与协议校验 |
| `response.serialize_write` | JSON 序列化与响应写入 |

父阶段包含子阶段，不能把各阶段相加当作总耗时。带锁读取的耗时不是纯锁等待；查询和网络各自的细分需要额外证据。不能从单次 `context deadline exceeded` 推断缓存损坏。

当前逐阶段覆盖账号列表、福利收件详情、自动扫描和投递事务。其他 v1 请求有完成日志与私有调用耗时，未声称每条 SQL 都有独立阶段。

## 错误与隐私

- 私有客户端保留可 `errors.Is/As` 的底层 cause，区分 transport、HTTP 拒绝、generation mismatch、response read/limit 和 invalid JSON。
- 日志记录错误类别、cause 类型链、网络操作、系统 errno、MySQL 错误号/SQLSTATE、对端状态与安全的业务错误码。数据库消息、完整 URL、响应正文和未知错误文本不打印。
- 不记录密码、Token、Cookie、幂等键、请求体、SQL 或 SQL 参数。分页游标只记 SHA256；规则、作业、扫描位置只记录定位需要的编号。
- 扫描 `retry=true` 是业务重试信号，通过关联编号查看网关原因；循环结束不表示所有投递成功。补发任务停止、失败样本和永久回执仍以原接口/数据库为准。

## 验收与排查

`TestV1DiagnosticsMySQL` 在一次性 MariaDB 中分别制造连接占满、`SLEEP` 慢查询和持有账号行锁的投递，验证慢阶段、失败回滚、解除阻塞后到账及重复投递去重。私有链路测试覆盖断连、超时、业务冲突、代次不符、非法 JSON、关联传播与敏感内容排除；并发阶段记录运行 race 检查。

1. 取浏览器 `X-Request-ID`、`X-Correlation-ID` 与发生时间，在两端完成日志定位对应请求。
2. 先核对状态/错误类别，再比较总耗时、私有调用和数据库阶段。
3. 福利查看 `job_id` / `cycle_id`、规则与收件对象，结合永久回执判断是否已提交。
4. 正式环境使用自然只读请求验收；故障注入和发奖测试只在隔离环境执行。

前端本地诊断导出、V148 AT-5T 专项适配、崩溃转储、逐帧采样和一般玩法联合时间线仍是独立待办，本次没有实现这些采集能力。
