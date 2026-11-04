# GM 冒险团改名

在「GM → 成长与职业 → 冒险团改名」输入新名字，确认一次即可。冒险团属于账号，同账号的所有角色共用名称；操作前需退出该账号的游戏连接，重新登录后读取新名称。仅修改已创建的冒险团。

名称遵循游戏现有冒险团规则：1–16 个 GB18030 字节，最多 8 个常用汉字或 16 个英文字母，不含空白、控制字符。重名判据使用数据库排序规则，并跨所有存档分片检查。角色名与冒险团名使用各自的规则。

私有管理接口要求管理会话和 CSRF：

- `GET /api/v1/accounts/{accountId}/adventure-name`：读取 `{accountId, name}`。
- `POST /api/v1/accounts/{accountId}/adventure-rename-operations/preview`：预览。
- `POST /api/v1/accounts/{accountId}/adventure-rename-operations`：提交，必须带 `Idempotency-Key`。
- `GET /api/v1/accounts/{accountId}/adventure-rename-operations`：游标分页记录。

写请求为 `{expectedName, newName, reason}`。能力键为 `adventure.read` 和 `adventure.rename`；旧网关无能力时隐藏表单。确认后保留原请求编号，响应丢失后重试返回原回执。页面及时更新冒险团名，登录账号名和角色名保持。

账号行、名称范围和永久回执在同一事务中加锁，复用游戏已有的 `account_edit_status` 和登录编辑锁，写前、提交前检查账号占用。SQL 只更新账号的 `represent_account_name`、`updated_at`；冒险团经验、创建日期、点券、账号元数据、角色和既有回执保持。

新增表为 `adventure_rename_operations`，独立于角色改名和点券记录。接续部署时先用累计版 PlayerGateway 的 `--init-management-schema` 初始化管理表，再更新网关和 `gm-console/dist`。游戏程序与登录器无需修改。本候选的实现、检查和上线状态以私有交接回执为准。
