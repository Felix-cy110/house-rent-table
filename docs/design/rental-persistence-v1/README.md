# 租房业务数据持久化 V1

初版日期：2026-10-15；更新日期：2026-10-18。状态：V1 字段契约及总租期增量已实现。运行配置、故障处理及验证方式见 [持久化说明](../../persistence.md)。本目录保留设计与示例，可执行版本位于 internal/extraction 和 internal/persistence。

用户点击 AI 分析，后端校验本次输入后产生独立提取任务。一次任务对应一次业务快照；同一房源再次分析产生新快照，后台重试沿用同一个任务 ID。业务数据来自填写内容，不使用联网估价替换报价，也不把 AI 核算结果覆盖到用户填写的总额。

本目录文件：

- `schema.sql`：MySQL 8.4 的 V1 初始三张表及约束，与 `001_rental.sql` 保持一致；后续字段通过增量迁移升级。
- `extraction.schema.json`：AI 输出对象的 JSON Schema，所有对象禁止额外键，规定的键必须出现，未知标量显式为 null。该文件使用通用 Draft 2020-12；模型调用适配层需要验证当前提供方所支持的子集，不承诺直接传入任意供应商即可使用。
- `input.example.json`：虚构的 15 项模板输入和固定提取上下文，与输出示例成对对应。
- `extraction.example.json`：完全虚构的提取结果示例，含面积范围、比例中介费、单价、免费、已含和未知费用。
- `types.go.txt`：与 JSON 一一对应的 Go DTO 参考；应用使用 internal/extraction/types.go。

## 一、关系与职责

```text
rental_extraction_task（提取任务）
    1 ── 0..1 rental_import_record（本次房源业务快照）
                  1 ── 0..N rental_charge（本次收费明细）
```

保留此前的两张核心表，补充一张费用明细表：水电单价、按面积收取的物业费、月租比例中介费具有不同单位，不能混装到一个含义不清的金额列。收费类型、金额、单位、周期均为明确的业务列。

任务表的 JSON 用于原文溯源和重试；常用房源属性在业务表中直接查询，费用通过明细表查询。`structured_result` 中的元数据和业务列必须来自同一份通过验证的结果，不能分别编辑。

## 二、模板 15 个字段的落点

| Excel 字段 | 业务落点 |
| --- | --- |
| 租几个月 | `rental.lease_months`，总租期，与押付月数独立 |
| 房租多少钱一个月 | `rental.monthly_rent`、`currency` |
| 押几付几 | `deposit_months`、`prepay_months`、明确写出的 `deposit_amount` |
| 中介费多少 | `charges[]`，`charge_type = AGENCY` |
| 水费 | `charges[]`，`charge_type = WATER` |
| 电费 | `charges[]`，`charge_type = ELECTRICITY` |
| 保洁费 | `charges[]`，`charge_type = CLEANING` |
| 物业费多少 | `charges[]`，`charge_type = PROPERTY` |
| 燃气费 | `charges[]`，`charge_type = GAS` |
| 首月总共要付多少 | `rental.quoted_first_month_total`，仅照录报价，不代表已核实的首次应付款 |
| 以后每个月要付多少钱 | `rental.quoted_monthly_total`，仅照录报价 |
| 小区地点 | `rental.community_location` |
| 房间大小/平方 | `area_sqm` / `area_min_sqm` / `area_max_sqm`、`area_qualifier`、`area_scope` |
| 朝向 | `rental.orientation` |
| 中介姓名 | `rental.broker_name` |

识别按语义进行，不绑定 Excel 行号。重复费用保留多条；多个不同报价竞争同一个标量字段时，该标量置空并记录冲突，不能让后一行覆盖前一行。新增费用归入 `OTHER`；尚未支持的非费用业务字段列入 `unmapped_field_ids`，原文完整保存在输入快照。

## 三、房源快照表 `rental_import_record`

除注明必填的系统列外，下列业务标量均允许 SQL NULL，对应 JSON null、Go 指针 nil。

| 数据库列 / rental JSON 键 | MySQL 类型 | Go DTO 类型 | 说明 |
| --- | --- | --- | --- |
| `id` | BIGINT UNSIGNED | uint64（数据库模型） | 自增主键；后端生成，AI 不输出 |
| `task_id` | CHAR(36) | string（数据库模型） | 必填、唯一，关联提取任务 |
| `community_location` | TEXT | *string | 已知小区及地点描述；不能猜城市或门牌 |
| `area_sqm` | DECIMAL(10,2) | *DecimalString | 单值面积，单位平方米 |
| `area_min_sqm` | DECIMAL(10,2) | *DecimalString | 区间下限 |
| `area_max_sqm` | DECIMAL(10,2) | *DecimalString | 区间上限 |
| `area_qualifier` | VARCHAR(16) | *AreaQualifier | EXACT / APPROXIMATE / RANGE |
| `area_scope` | VARCHAR(24) | *AreaScope | ROOM / UNIT / GROSS / USABLE / OTHER；无依据则 null |
| `orientation` | VARCHAR(16) | *Orientation | N / NE / E / SE / S / SW / W / NW / NS / EW / OTHER |
| `broker_name` | VARCHAR(256) | *string | 中介姓名；联系号码等额外信息保留输入原文 |
| `currency` | CHAR(3) | *string | 主表各金额的共同币种，例如 CNY；不做汇率换算 |
| `monthly_rent` | DECIMAL(14,2) | *DecimalString | 明确的每月租金 |
| `lease_months` | DECIMAL(6,2) | *DecimalString | 明确的总租期（月），必须大于 0；未知为 null |
| `deposit_months` | DECIMAL(6,2) | *DecimalString | 押金对应的月租月数；允许 0、0.5 等 |
| `prepay_months` | DECIMAL(6,2) | *DecimalString | 每次预付租金覆盖的月数，必须大于 0 |
| `deposit_amount` | DECIMAL(14,2) | *DecimalString | 原文明确写出的押金金额；不凭月租乘押金月数补填 |
| `quoted_first_month_total` | DECIMAL(14,2) | *DecimalString | 用户写的首月总额；不参与费用再次相加 |
| `quoted_monthly_total` | DECIMAL(14,2) | *DecimalString | 用户写的后续月费总额；不覆盖为 AI 计算值 |
| `data_quality` | VARCHAR(16) | string（数据库模型） | 必填；NORMALIZED / NEEDS_REVIEW，由后端判定 |
| `created_at` | DATETIME(3) | time.Time（数据库模型） | 必填；实际入库时间，UTC |

面积规则：`30平` → EXACT + area_sqm="30.00"；`约30平` → APPROXIMATE + area_sqm="30.00"；`25—30平` → RANGE + min="25.00"、max="30.00"，单值为 null。无可用面积时四个数值/限定字段全部为 null。面积必须为正，不把房间面积自动当作整套或物业计费面积。`ROOM` 表示单间，`UNIT` 表示明确整套但未说明建筑/使用口径，`GROSS` 和 `USABLE` 只用于原文明确该口径的场景；其他组合保留原文并标 OTHER。

主表金额 V1 只接收明确的单值；`大概2500`、`2500—3000` 不强制压成一个精确报价，对应金额为 null 并记录 APPROXIMATE_VALUE 或 RANGE_VALUE。本版提取不执行年租折算月租等算术派生，不能直接对应的周期标为 UNSUPPORTED_PERIOD；超精度结果不静默四舍五入。

币种优先使用原文明确值；未注明时只允许使用后端在任务 `extraction_context` 中明确提供的 `default_currency`，禁止 AI 自行假定。主表金额出现混合币种时不做转换；不能确定统一口径的主表金额置空并记录 CURRENCY_CONFLICT。费用明细有独立币种。

`NORMALIZED` 只表示受支持且非空的输入均已处理、没有待复核问题，不代表房源信息齐全、报价真实或合理。空白字段保持 null，不因此判任务失败；对非空但含糊/冲突的内容记录问题。存在问题或未映射的非空字段时，后端标记 NEEDS_REVIEW。

## 四、费用表 `rental_charge`

每个可区分的收费项一行。一格内明确包含两个独立收费项时可拆两行并引用同一来源；相同类型的不同报价保留多行。禁止 `(record_id, charge_type)` 唯一约束。

| 数据库列 / charges 元素键 | MySQL 类型 | Go DTO 类型 | 说明 |
| --- | --- | --- | --- |
| `id` | BIGINT UNSIGNED | uint64（数据库模型） | 自增主键 |
| `record_id` | BIGINT UNSIGNED | uint64（数据库模型） | 必填，关联房源快照 |
| `item_index` | SMALLINT UNSIGNED | uint16（数据库模型） | 必填；后端按数组顺序生成，从 0 开始 |
| `charge_type` | VARCHAR(24) | ChargeType | 必填；AGENCY / WATER / ELECTRICITY / CLEANING / PROPERTY / GAS / OTHER |
| `charge_name` | VARCHAR(256) | string | 必填；具体项目名称 |
| `direction` | VARCHAR(8) | Direction | 必填；CHARGE / CREDIT / UNKNOWN；抵扣用 CREDIT + 正金额 |
| `pricing_mode` | VARCHAR(16) | PricingMode | 必填；FIXED / UNIT_PRICE / RENT_RATIO / INCLUDED / FREE / UNKNOWN |
| `currency` | CHAR(3) | *string | 币种，与主表相同的确定规则 |
| `amount` | DECIMAL(14,2) | *DecimalString | 固定金额或明确免费时的 0；不把用量单价放这里 |
| `unit_price` | DECIMAL(18,6) | *DecimalString | 单位价格，如电费 0.623 元/度 |
| `unit` | VARCHAR(16) | *ChargeUnit | TON / M3 / KWH / SQM / PERSON / HOUSEHOLD / OTHER |
| `ratio` | DECIMAL(10,6) | *DecimalString | 月租等基准的比例，50% 为 "0.500000" |
| `ratio_base` | VARCHAR(16) | *RatioBase | MONTHLY_RENT / ANNUAL_RENT / OTHER |
| `billing_period` | VARCHAR(16) | *BillingPeriod | ONCE / DAY / WEEK / MONTH / QUARTER / YEAR / OTHER；不明为 null |
| `payment_timing` | VARCHAR(16) | *PaymentTiming | MOVE_IN / RECURRING / MOVE_OUT / OTHER；不明为 null |
| `source_field_ids` | JSON | []string | 必填非空数组，引用本次输入的 field.id |
| `notes` | TEXT | *string | 特殊条件、未支持的复杂口径、含于哪个项目等说明 |

费用模式互斥：

| pricing_mode | 金额字段的约定 | 示例 |
| --- | --- | --- |
| FIXED | amount 必填；unit_price、unit、ratio、ratio_base 为空 | 保洁费 50 元/月 |
| UNIT_PRICE | unit_price、unit 必填；amount、ratio、ratio_base 为空 | 水费 5 元/吨；物业费 2 元/平方米/月 |
| RENT_RATIO | ratio、ratio_base 必填；amount、unit_price、unit 为空 | 中介费为月租的 50% |
| INCLUDED | 全部金额/单价/比例相关字段为空，notes 说明包含关系 | 物业费含在租金中；不等于费用为 0 |
| FREE | amount="0.00"，其他金额/单位/比例字段为空 | 明确写免费/免收 |
| UNKNOWN | 金额/单位/比例字段全部为空，原文和 issues 保留原因 | 阶梯价格、待确认、含糊报价 |

`0元/度` 为 UNIT_PRICE + unit_price="0.000000"，可以保留原文明确的计价形式；`免费` 为 FREE。空白水费可保留 UNKNOWN 行，但不得转为 FREE。FIXED/UNIT_PRICE/RENT_RATIO/FREE 需有明确币种；确实无法确定币种时使用 UNKNOWN 并记录问题。

周期与付款时间分开：`1200元/年` 说明 YEAR，不能自动认定 MOVE_IN。水电只给单位价格时不默认 MONTH，也不能没有用量就生成总费用。OTHER 单位或比例基准必须在 notes 说明；阶梯单价、区间价和混合无法拆解口径 V1 不强行取值，使用 UNKNOWN + 问题记录。

## 五、任务表 `rental_extraction_task`

全部由后端管理，AI 不生成任务 ID、状态、重试次数或时间。

| 列 | MySQL 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `id` | CHAR(36) | 是 | 后端生成 UUID；后台重试保持不变 |
| `file_name` | LONGTEXT | 是 | 从输入 Document 提取的文件名，兼容分析接口已有的输入上限 |
| `input_document` | JSON | 是 | 点击分析时的完整 Document；保留顺序、原文、null、bold 和单元格位置 |
| `extraction_context` | JSON | 是 | 任务创建时固定的补充口径；没有默认币种时 default_currency 为 null |
| `schema_version` | SMALLINT UNSIGNED | 是 | 本提取契约的版本，目前 1；独立于输入 Document 的 schemaVersion |
| `extractor_version` | VARCHAR(128) | 是 | 提取规则/提示词版本，不在重试时静默升级 |
| `provider` | MEDIUMTEXT | 否 | 实际调用的提供方；尚未调用时 null，兼容已有配置名称长度 |
| `model` | TEXT | 否 | 实际调用的模型；尚未调用时 null，不截断供应商模型名称 |
| `stage` | VARCHAR(16) | 是 | EXTRACT / PERSIST |
| `status` | VARCHAR(16) | 是 | PENDING / RUNNING / RETRY_WAIT / SUCCEEDED / FAILED |
| `attempt_count` | INT UNSIGNED | 是 | 每次开始 EXTRACT 或 PERSIST 阶段递增，初始 0 |
| `structured_result` | JSON | 否 | 校验通过的完整标准 JSON；用于入库重试、证据及问题查询 |
| `last_error_code` | VARCHAR(64) | 否 | 最近一次错误分类 |
| `last_error_message` | TEXT | 否 | 脱敏错误信息，不保存令牌或认证配置 |
| `next_retry_at` | DATETIME(3) | 否 | 待重试时间，UTC |
| `created_at` | DATETIME(3) | 是 | 后端接受任务的时间，UTC，重放时保留原时间 |
| `updated_at` | DATETIME(3) | 是 | 最近一次状态更新时间，UTC |
| `completed_at` | DATETIME(3) | 否 | 成功或最终失败的完成时间，UTC |

EXTRACT/PENDING → EXTRACT/RUNNING → PERSIST/PENDING → PERSIST/RUNNING → PERSIST/SUCCEEDED。可重试失败保留阶段、设 RETRY_WAIT；永久失败设 FAILED。已有合法 structured_result 的 PERSIST 重试不能再次调用 AI。接收任务后由独立生命周期执行，不绑定 HTTP 请求的取消。

这张表记录任务；MySQL 不可用时由独立的本地持久化队列保留输入及通过校验的模型结果，恢复后只重试入库。队列投递失败会通过响应头和页面提示明确告知，主报告仍可继续。

## 六、标准 JSON、Go 与数据库映射

AI 顶层仅输出：`schema_version`、`rental`、`charges`、`evidence`、`issues`、`unmapped_field_ids`。完整定义见同目录 JSON Schema、示例和 Go DTO。

- `rental` 的键直接对应房源快照业务列；`charges[]` 的键直接对应费用业务列。数据库系统列由后端补充。
- `evidence[]` 每项含 `path`、`source_field_ids`、`note`，为主表非空业务值指明输入依据，也可补充费用推导的说明。path 使用以 / 开头的属性路径，例如 /rental/monthly_rent、/charges/0/ratio。费用项的共同依据由其 source_field_ids 提供，不要求逐键重复列出同一来源。
- `issues[]` 每项含 `path`、`source_field_ids`、`code`、`message`，区分 AMBIGUOUS、CONFLICT、UNSUPPORTED_UNIT、UNSUPPORTED_PERIOD、UNSUPPORTED_RULE、APPROXIMATE_VALUE、RANGE_VALUE、CURRENCY_UNKNOWN、CURRENCY_CONFLICT、INVALID_VALUE、PRECISION_LOSS。
- `unmapped_field_ids` 保存尚未落到受支持业务结构中的输入字段 ID；不能删除原文。
- evidence、issues、未映射 ID 随 structured_result 保存，通过 task_id 查询。它们不是任由 AI 扩展数据库列的入口。
- DecimalString 的 JSON 表示为十进制字符串：金额/面积/月数固定 2 位小数，单价/比例固定 6 位小数。MySQL 使用相应 DECIMAL；Go 校验/计算使用十进制定点或有理数，数据库写入传验证后的字符串，不经 float32/float64。
- `*DecimalString`、枚举指针和 `*string` 表示可空标量。所有键必须出现；Go 解码器需显式拒绝缺键与未知键，普通指针字段的 Unmarshal 本身不能区分缺键与 null。
- 所有数组必须输出 [] 而不是 null；未知值不使用 0、空字符串或字符串 "null" 代替。不能仅凭一次 json.Unmarshal 成功就入库。

后端验证顺序：JSON Schema → 十进制精度和范围 → 枚举/字段组合 → 来源 ID 真实存在与来源语义一致 → 业务冲突规则 → 事务写入。缺键、额外键、非法格式或伪造来源属于提取失败；原文确实不明确则输出 null/UNKNOWN + issue，允许保存 NEEDS_REVIEW 快照。

每个非空输入字段至少出现在 evidence、费用来源、issues 或 unmapped_field_ids 中。每个非空主表业务标量都必须有对应 evidence；费用由 source_field_ids 支持，可用 evidence 补充推导。默认币种可引用固定的 extraction_context 并在 note 说明；后端不能只验证引用 ID 存在就认为推断正确。动态字段的 ID 只在本次输入内有效，不能单独当作跨文件关联键。

Schema 和业务规则可以阻止格式错误及已知口径冲突，但不能证明所有自然语言提取均正确；NORMALIZED 不等于人工核实。保留原文和证据就是为了能追溯、复核及按新规则重新提取。

## 七、约束、事务与索引

- 一个 task_id 最多一个房源快照；费用以 `(record_id, item_index)` 唯一。允许同文件/同小区多次分析，不能按地点、文件名或字段标签去重。
- 房源快照、全部费用、任务 SUCCEEDED 状态在一个事务中提交。任务重放发现已成功时不追加费用；读到未知提交结果时以 task_id 查询确认。
- task 索引 `(status, next_retry_at, id)` 和 `(created_at, id)`；record 索引 `(created_at, id)`；charge 索引 `(charge_type, record_id)`。跨金额/朝向筛选的组合索引待实际查询确定，V1 不创建大量未使用索引。
- 外键使用 RESTRICT；没有自动级联删除历史记录。业务快照为追加记录，任务状态允许更新。
- DATETIME(3) 统一写入 UTC；程序显式提供时间，数据库连接会话也配置 UTC，不依赖服务器本地时区。
- V1 无需原始 XLSX 的文件哈希/文件大小：当前分析接口只有已解析 Document。若以后保留原始文件，单独补充文件存储契约，不伪造该元数据。

## 八、评审和验证范围

模板字段、数据库列、JSON Schema、Go DTO、业务校验、本地队列、独立 AI 会话、迁移与 MySQL 仓储均已实现。初版历史验证：本机以隔离 MySQL 9.1 验证了迁移、事务回滚、并发幂等和精确小数，以真实 Codex + 本地模拟供应商验证了 outputSchema 传递；CI 增加 MySQL 8.4 测试配置，尚未在本轮本机执行该版本。未调用真实模型供应商，实际供应商的兼容性和自然语言提取质量仍需真实环境验收。

自动化测试覆盖契约反例、来源覆盖、业务字段组合、队列重启恢复、数据库故障补写、失败任务留档及主报告隔离。真实数据库测试验证了迁移重放、并发幂等、事务回滚、精确小数及长元数据完整保存；页面测试覆盖保存成功、后台入队失败及报告失败后继续保存。具体命令见持久化说明。

租期字段通过 `internal/persistence/migrations/003_lease_months.sql` 增量添加；本目录 `schema.sql` 保留原始 V1 DDL。历史记录和未含租期的旧提取结果按 null 处理，不从押付规则推断。新提取规则版本为 `rental-extraction-v1.1.0`。

2026-10-19 接入调整：后台提取改由 `internal/aiclient` 直连用户配置的 API，与主报告使用独立请求；Chat Completions 使用 `response_format`，Responses 使用 `text.format` 传递原有提取 Schema。CLI 协议检查已由本地 HTTP 服务测试替代，业务契约、队列和迁移保持原约定。
