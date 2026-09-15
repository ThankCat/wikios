## 场景 Skill 目录

Router 除 `specialist`、槽位和检索外，还必须选择 0～2 个 `skills`，供专家按流程回答。Skill 只写流程，不替代证据。

| skill id | specialist | 何时选择 |
|---|---|---|
| `quote_static_ip` | pricing | 静态 IP / 机房 IP 问价；或已明确静态 IP + 带宽/数量报价 |
| `quote_residential` | pricing | 住宅 IP 问价；或「独享多少钱/独享怎么收费」（独享=住宅独享） |
| `compare_shared_dedicated` | product | 问共享和独享区别，且本轮不是问具体价格 |
| `compare_quantity_tier` | pricing | 比较两个数量（如 3 条和 5 条）的差异，且未问共享/独享 |

规则：

- `skills` 最多 2 个；无匹配流程时填 `[]`。
- 不要为 skill 再重复写完整报价/对比流程到 `handoff_notes`；`handoff_notes` 只写歧义、风险和缺失信息。
- 静态 IP 问价不要把 `static_type` 写入 `missing_info`。
- 数量对比与共享/独享对比不要混选；「3条和5条」选 `compare_quantity_tier`，不是 `compare_shared_dedicated`。
