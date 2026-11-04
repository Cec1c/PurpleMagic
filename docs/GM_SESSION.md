# GM 统一登录

登录 GM 后即可管理公告、下载源和游戏连接，无需再次输入运营管理 Token。记住登录仍为 30 天；退出 GM 后，这些操作与其他管理功能一起失效。

公告保留一次操作确认、编辑版本检查和原请求重试。升级前保存在浏览器里的公告请求继续沿用原请求编号。

## 接口与部署

- 浏览器使用 GM Cookie 和 capabilities 中的 `csrfToken`，请求 `/api/v1/hub/state`、`news`、`news/entries`、`source`、`connection`。
- 外部模式：GMService 校验登录、同源与 CSRF，再通过现有私有服务凭据调用 PlayerGateway 的 `/api/internal/hub/`。无需增加浏览器 Token 或配置新密钥。
- 一体模式：PlayerGateway 内的 Hub 操作复用管理模块的 GM 会话。
- 公告和连接写入保留 `If-Match`，公告请求保留 `Idempotency-Key`。旧 Hub 发布自动化仍使用原有 Admin Token；安装包上传和资源发布接口不属于 GM 会话入口。

升级顺序为 PlayerGateway → GMService → `gm-console/dist`。两个后端都要包含统一会话入口。反向代理将 `/api/` 交给 GMService；旧 `/admin/` 可继续供原有 Hub 管理工具使用。无需修改数据库结构或重启游戏服务。

验证覆盖外部与一体部署的单次登录、公告发布/删除/搜索、下载源和连接保存、版本冲突、幂等重试、跨站拒绝、未登录和退出后拒绝，以及私有接口隔离。
