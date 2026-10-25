# AgentLab 设计元数据与验收证据

当前批准方向为黑色极客工作台，规则见 [DESIGN.md](../DESIGN.md)。本目录只保留当前规范、品牌状态、验收记录和过渡 Logo，不作为运行时资源目录。

## 保留文件

- [brand.json](brand.json)：新方向、过渡 Logo 与实施状态。
- [design-tokens.target.json](design-tokens.target.json)：目标颜色、尺寸与动效；不是当前运行 CSS。
- [style-guide.html](style-guide.html)：新规范与目标变量的导航入口。
- [design-validation.json](design-validation.json)：设计变量与对比度核对记录。
- [p0-acceptance.json](p0-acceptance.json)：脱敏的工作台 P0 验收摘要。
- [p0-backend-acceptance.json](p0-backend-acceptance.json)：脱敏的真实模型与后端验收摘要。
- [logos/learning-bloom/logo.png](logos/learning-bloom/logo.png) 和 [favicon.ico](logos/learning-bloom/favicon.ico)：过渡期品牌资产；运行时使用的副本位于 `frontend/public/brand/`。

后续导航使用统一单色 SVG 线描，目前尚未交付。Learning Bloom 图形与 favicon 作为过渡资产保留，不自动改选其他 Logo，不对原图使用 CSS 滤镜改色。

旧图标图集、探索 Logo、预览画廊和生成中间文件已从本目录移除，因为它们不参与当前产品运行，也没有被当前规范或验收流程引用。旧学习页面仍需的素材保留在 `frontend/public/`，不与当前工作台设计混用。

素材引用在运行时使用 frontend/public 的相对资源路径；文档可以引用本目录，产品页面不能依赖本机绝对路径或 .cache 文件。
