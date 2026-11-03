# 参考来源

## dforigin.online

- 页面：<https://dforigin.online/forum?line=90>，2026-10-19 实际截图与公开加载资源。
- `src/origin.css` 提取原站 `experience-5d6569f05c04.css` 的深浅色、字号/间距、圆角/阴影、玻璃变量及悬浮侧栏/顶栏/背景规则。仅调整本地资产 URL；此后使用 Prettier 格式化。
- `public/assets/dnf90-origin.webp` 来自原站公开 `dnf90-origin-a1f02622c24e-original.png`，等比缩放、转 WebP。原站署名为游戏美术 © NEOPLE。
- 原始 URL 与 SHA256 见 `reference-manifest.json`。本地原资源保存在主工作区 `local-notes/dforigin-reference-20261019/`。
- 新客户端组件、七区功能组织及本地交互为 PurpleMagic 实现。

## 功能与事务参考

- 背包编辑器 `[250124]背包编辑工具完整包` 的截图 1–8（含 2-1）、`python36/sqlManager2.py` 及界面模块，仅用于功能/字段含义核对；没有复制该工具的数据库 SQL、二进制布局或可执行文件。
- <https://github.com/Liuxiny/86jp_DfoGmTool>，固定提交 `13a45286166643f9e0f7bfce81c9ac67134dd848`。参考容器、实例选项、邮件附件、任务状态与事务组织。该固定版本使用 Microsoft.Data.Sqlite；新客户端的数据契约按本服 Go/MariaDB API 定义。

## 工程依赖

React、React DOM、Vite、TypeScript 及相应类型包按其发布包许可证使用。图标采用 Lucide（ISC），与参考站使用的图标库一致。精确版本见 `package-lock.json`。

## PurpleMagic 品牌

witch 与 hat 来自项目所有者提供的品牌图标包；用于品牌标志、登录器图标与 favicon。来源摘要见 [品牌清单](../docs/brand-assets.json)。

## Markdown

公告预览使用 react-markdown 与 remark-gfm（MIT），精确版本和传递依赖见 package-lock.json。原始 HTML 不作为可执行内容渲染。
