# 福利规则：把一次发奖变成持续运营规则

PurpleMagic 将奖励内容、受众资格和领取记录保存成一条长期规则。管理员创建并启用后，外部 GMService 的定时器自动处理符合条件的角色；关闭页面后仍继续运行。勾选“包含未来新建角色”后，后来加入的角色也会获得同一份福利。

![福利规则、未来角色与暂停恢复](images/benefits.webp)

例如创建“冒险启程福利”，每角色发放 2,000,000 金币，并选择覆盖未来角色。现有角色进入后台扫描，新建角色在后续扫描中加入；同一角色已经获得这条规则的奖励后，重复扫描只读取领取记录。规则处于暂停期间不启动新的投递，恢复后继续处理尚未领取的合格角色。

## 三个相互独立的决定

| 决定 | 行为 |
| --- | --- |
| 奖励内容 | 表单可选择金币、点券或指定道具。道具支持名称检索、直接输入 ID 和数量，按批准 PVF 的定义构造；每条规则一种道具，多种道具分别建规则 |
| 领取范围 | 金币、道具可选每角色一次或每账号一次；点券固定每账号一次，直接存入账号共享余额。账号级规则选择一个符合条件的角色收取邮件或到账通知 |
| 时间受众 | 首次自动启用时保存现有角色截止时间；“未来角色”选项独立决定后续创建角色是否加入 |

选择账号发送一次邮件会展开为该账号的全部现有角色；“每账号一次”的福利规则则按账号永久去重。这两个功能的范围不同。

## 为什么能够反复扫描

每条规则有固定 ID，数据库记录“这条规则对这个账号/角色已经发过”。投递邮件、附件和领取回执在同一事务中完成。断网、重试、服务重启后继续读取同一套记录；删除已收取的邮件不会删除领取回执。

点券规则将账号余额、余额版本、到账通知和永久回执一起提交。玩家无需领取附件，规则的发放详情显示点券数量及前后余额。同账号多个角色、未来新建角色或暂停后恢复均不会重复获得该规则的点券。自动扫描仍以已建角色为受众入口：没有角色的账号在首个符合条件的角色建立后进入扫描。

点券单次奖励为 1–2,147,483,647，余额使用现有账号点券钱包及其整数上限。异常余额、溢出或事务失败时整笔回滚，失败名单可重试。新增点券规则与任务保存在独立 v3 表中，旧扫描器不会读取；现有金币和道具规则、ID、表和回执保持原语义。

道具在确认前读取当前物品名称和完整 PVF 摘要；数量必须为正整数，装备每封邮件数量为 1。启用后仍复用既有按收件人生成附件、跨 PVF 核对、自动扫描和失败恢复流程。

暂停与恢复保留原规则 ID、首次启用时间、现有角色截止时间和全部回执。只覆盖现有角色的规则，恢复时仍沿用原截止时间；需要扩大受众时创建新的明确规则。已经启用的历史规则也不会因为打开新版页面而自动扩大受众。

调度在外部管理服务，游戏侧每次处理最多 5 个账号，单批邮件最多 10 人。完整 PVF 与检索索引留在外部，游戏侧执行有界事务和准入检查。页面查看进度，后台负责推进。

## 适合的场景

- 长期新人礼包：现有与未来角色持续覆盖。
- 活动补偿：首次启用时固定现有受众，保留可查的发放记录。
- 测试服基础资源：暂停或恢复发放，不需要手工重新整理未领名单。

当前规则按账号或角色终身一次，没有每日重置、周期领取或活动日历。新建另一条规则会产生另一份领取资格。PVF 更新需要验证奖励定义并保持原规则和回执，不通过重建索引来重置领取次数。

实现与验证入口：[规则和受众](../gateway/internal/modules/dnf/testbenefits/campaigns.go)、[发放事务](../gateway/internal/modules/dnf/testbenefits/operations.go)、[自动福利与恢复契约](GM_FEEDBACK_RULES.md)、[外部调度](GM_EXTERNAL_SERVICES.md)。

## English overview

A benefit rule stores an immutable reward, recipient eligibility and permanent delivery receipts. Once enabled, the external service scans eligible characters in bounded batches, even when the browser is closed. Operators can include future characters or keep the original existing-character cutoff.

The console supports gold, account Cera and a specific item selected by name or ID. Gold/items allow once per character or once per account. Cera is always once per account and credits the shared wallet directly; its balance, revision, notification and permanent receipt commit together. Accounts enter the scanner through an eligible character. Item rules bind the approved PVF and equipment count is one per mail. Retries, restarts, deleted mail and pause/resume preserve receipt identity. Currency rules use separate v3 tables that older scanners cannot consume. Current rules are lifetime-once per identity, not daily or recurring rewards.
