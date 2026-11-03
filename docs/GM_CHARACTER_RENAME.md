# GM 角色改名

在 GM → 成长与职业下方的「角色改名」输入新名字，确认一次即可。仅目标角色需要离线，同账号其他角色可继续在线。名称按客户端 GBK 编码校验，为 2–12 字节，最多 6 个汉字或 12 个英文字母；禁止控制字符和无法用 GBK 表示的表情，使用与创建角色相同的数据库重名判据。

接口需要管理会话和 CSRF，公开玩家入口不暴露这些路由：

- `POST /api/v1/characters/{characterId}/rename-operations/preview`：校验归属、旧名称和新名称，返回修改摘要。
- `POST /api/v1/characters/{characterId}/rename-operations`：相同请求体及 `Idempotency-Key` 提交。
- `GET /api/v1/characters/{characterId}/rename-operations`：分页读取改名回执。

请求体为 `{accountId, expectedName, newName, reason}`。能力键为 `characters.rename`；服务未提供能力时页面不显示改名表单。确认后响应丢失，使用原请求编号读取已提交结果。成功后刷新选中角色及目录中的名称。

角色行、同服名称范围和回执在同一事务中处理，沿用 `character_edit_status` 与账号/角色数据库编辑锁，前后检查目标角色的进入和退出状态。写入只涉及 `name` 与 `updated_at`；角色 ID、账号归属、资产、任务及已有回执保持。改名前后名称、操作者和原因保存在独立 `character_rename_operations` 表，与任务历史分开。

升级先用新 `PlayerGateway --init-management-schema` 对当前配置初始化追加的管理表，再发布累计网关及 `gm-console/dist`。旧版服务可继续运行，游戏程序和登录器无需更新；名称变更在下一次进入角色时读取。
