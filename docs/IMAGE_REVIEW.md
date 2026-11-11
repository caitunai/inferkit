# 图片识别结果采集与人工纠错

## 启用与部署

配置了 `db.database` 时，图片 API 默认开启结果采集。也可在 `[image.filter]` 显式设置 `recordResults=true` 或 `false`。仅采集成功且调用方提供了 `url` 的 `POST /api/image/filter` 请求；CLI、仅上传文件的请求、失败的推理均不写入结果表。

审核页面使用独立 Basic Auth，第三方检测 API 的 Bearer Token 无法查看或修改审核数据。在 `.app.toml` 中配置：

```toml
[db.table]
prefix="ifk"

[image.filter]
model="/path/to/artwork_fp32"
recordResults=true

[image.review]
enabled=true
username="image-review-admin"
password="替换为至少16个字符的独立管理密码"
```

数据库连接继续使用已有 `[db]` 配置。审核用户名最长 128 个字符，不能包含冒号或首尾空白；密码为 16–256 个字符。通过 HTTPS 访问管理页面。默认 `image.review.enabled=false`，关闭审核页面不会停止已启用的结果采集；关闭采集也可继续审核已保存的历史记录。

发布前先执行迁移，并按顺序构建前端及 Go 二进制：

```bash
atlas migrate validate --env local
atlas migrate apply --env local
cd storage/ui
npm ci
npm run build
cd ../..
go build
```

Atlas 命令需要对应的 `DB_DEV_*` / `DB_LOCAL_*` 环境变量。仓库的 Atlas HCL 当前使用 `ifk_` 表前缀，必须与应用的 `[db.table].prefix="ifk"` 一致；其他前缀需同步调整 HCL，再生成迁移。生产环境使用对应的 `--env prod`。

迁移目录保留空 baseline，随后由 `20261104100407_config_center.sql` 创建配置中心的三张表，由 `20261104100450_image_review.sql` 创建图片结果表。当前 Atlas 只管理这四张业务表，不包含认证占位表。空库按顺序应用即可。**对于已经通过其他方式创建配置中心表、尚未采用此迁移历史的数据库**，先核对其配置中心表与 `config_center` 迁移完全一致，随后才可使用下面的 baseline 方式跳过配置中心表创建、应用图片结果表迁移：

```bash
atlas migrate apply --env local --baseline 20261104100407
```

不要在缺少基础表或结构不一致的数据库上直接设置此 baseline。启用采集但未迁移结果表时，服务启动检查会失败。`serve` 和 `image-filter serve` 均支持采集；后者继续不初始化 Redis / Queue。无数据库的纯推理部署可显式关闭 `recordResults`。

`20261104114005_drop_prediction_json.sql` 删除冗余的 `prediction_json` 列，保留原始类别、置信度及所有人工标注。已有环境升级时先备份该表，停止旧服务，执行迁移后启动新二进制；旧版本仍会读写该列，不能与此次删列同时使用。历史初始化迁移保持原样，新环境按完整迁移顺序创建后删除该列。

这次清理调整的是尚未发布的初始化迁移草稿。此前在本地验证时创建的认证占位表继续保留，移除 HCL 不会执行删除表的 SQL。若其他环境已应用旧草稿，需备份并核对迁移记录后再同步历史；已经发布的迁移不应照此重写，也不要通过删除业务表来绕过校验。

## 数据与写入规则

结果表为 `ifk_image_predictions`，通过 GORM 的全局数据库连接池访问。

应用连接同时设置驱动 `loc=UTC` 和 MySQL 会话 `time_zone='+00:00'`，每个新建连接均生效；数值偏移不依赖 MySQL 时区表。`TIMESTAMP` 时间按 UTC 读取并通过带时区的 JSON 返回，页面再按浏览器本地时区显示。升级后需重启应用以重建连接池，无需修改 MySQL 全局时区或执行表结构迁移。

| 列 | 用途 |
| --- | --- |
| `id`、`created_at`、`updated_at` | 记录标识与时间 |
| `url`、`url_sha256` | 调用方提供的来源 URL 及其哈希 |
| `sha256` | 实际检测的原始图片字节 SHA-256 |
| `model_sha256` | 默认 libvips 路径为模型 SHA-256；显式 Go 比较路径使用模型与预处理 profile 的组合摘要，区分检测身份 |
| `label`、`confidence` | 不可变的模型预测及获胜类别概率 |
| `correction` | 独立人工标签；空字符串表示未标注 |
| `reviewer`、`reviewed_at`、`revision` | 管理员用户名、标注时间（Unix 毫秒）、并发修改版本号 |

每个 `(URL 哈希, 图片内容哈希, 模型哈希)` 组合只保存一次，重复调用或命中推理缓存仍会执行一次幂等插入，保证采集不受缓存影响。重复插入保留首次预测，不覆盖已有人工标签。相同 URL 的图片内容或模型版本变化时会产生新记录。

通过 CLI 参数或 API 路径选择后端时，每个请求都携带独立的检测身份，避免两条路径的不同预测被合并；两个后端的启动日志 `prediction_sha256` 对应表中身份，`model_sha256` 对应实际 ONNX 文件摘要，组合规则见[图片预处理后端对比](IMAGE_PREPROCESS_BACKENDS.md)。

上传文件与 URL 同时提交时，继续使用**上传文件的字节**进行检测和哈希计算，URL 仅为来源标识，不会为推理再次下载。仅提交 URL 时下载一次并复用该次推理结果入库。

启用采集后，带 URL 的 API 请求只有写入成功才返回 HTTP 200；存储失败返回 HTTP 503，提示 `image result storage unavailable`。客户端可有限次数退避重试，唯一索引避免重复记录。写入为一个原子 INSERT，无额外预读、无逐请求模型初始化、无后台丢失窗口；数据库操作受请求上下文及五秒上限约束。采集与审核的 SQL 日志使用参数占位符，不输出来源 URL 或管理员信息。

数据库不保存完整推理 JSON 或各类别概率向量，列表与单图详情直接读取 `label`、`confidence`。第三方检测 API 的实时响应仍包含完整类别和概率。审核类型统一由 `services/imagereview.Labels()` 定义，当前为 artwork 的 `0_invalid` / `1_valid`，页面筛选、纠错按钮与服务端校验共用这一集合。

只保存来源与结果，不存原始图片文件。页面从来源 URL 加载预览，不执行新的模型推理。请使用稳定的图片地址；URL 失效或内容变化可能影响人工复核，后续生成训练集时应将下载内容与保存的 `sha256` 核对。文件附带的 URL 来自调用方，服务不会验证它与远端内容一致。

## 页面操作

- `/image-review`：图片网格列表。支持预测类型、置信度上下限、未标注 / 已标注筛选；支持最新、最早、置信度升序、置信度降序。每张卡片直接提供「无效」「有效」「采用模型结果」三个按钮；成功后就地更新同 URL 的卡片，保持当前筛选、分页和滚动位置。点击图片可打开大图预览与审核弹窗。
- `/image-review/review`：单图标注。使用相同筛选条件选择范围，每次只显示一张图片。「无效图片」「有效图片」「跳过此图」以红、绿、蓝三个并排大按钮显示，保存成功后自动切换下一张。

概率输入使用 0–100%，对应原始预测的 `confidence`，上下限均包含边界。它是模型获胜类别的概率，不会随着人工纠错改变。列表使用固定大小的游标分页，置信度相同时以 ID 确定顺序，不做历史全量 COUNT 或深 OFFSET。

列表点击图片在当前页面打开大图与类型选择弹窗，打开、取消、Escape 或点击遮罩关闭不写数据。弹窗内保存成功后关闭。点击「逐张标注」可在整个筛选范围中逐一处理。确认模型判断正确时，可使用列表的「采用模型结果」或逐张页的「跳过此图」：两者均将所选记录的原始模型类别写入独立人工标签，记录为已审核，逐张页随后切换下一张。在「未标注」筛选下，同 URL 已完成的卡片会一起移除。

操作结果通过右下角悬浮 Toast 显示，不占用列表或弹窗的布局空间。成功提示三秒后自动消失；连续操作更新同一条提示。失败提示保留至关闭或重新加载，提供重新加载入口；大图弹窗内也能显示。

每次标注只更新人工列，提交时携带读取到的 `revision`。同一完整 URL（按 `url_sha256` 匹配）的所有已有识别记录在同一事务内覆盖 `correction`、`reviewer`、`reviewed_at`，并分别递增 `revision`；重复标记、纠错与采用模型结果均遵循此规则，不新增审核记录，保留各自的模型预测与内容摘要。不同 URL 独立审核，不合并查询参数或大小写。同 URL 后续采集到的新内容或新模型记录仍是未标注记录，下一次审核时统一更新。

审核事务先查询同 URL 的最早 ID，再以该主键加排他锁，随后对所选记录执行版本条件更新，最后批量更新其余同 URL 记录。所有审核入口遵循同一锁顺序；该锁依赖识别记录不删除、不修改身份字段。任一步失败整体回滚，数据库上下文保留五秒上限，无外部调用。现有身份唯一索引的 URL 哈希前缀支持查找与更新，无需修改表结构。

如果其他管理员已经修改该 URL 的记录，返回 HTTP 409 并保留当前图片；点击 Toast 中的「重新加载本页」或「重新加载记录」获取新版本后再决定是否修改。写入失败不跳到下一张。写入成功但下一张加载失败时，页面明确提示标注已保存，可重试加载，不重复写入上一条标注。

人工复核后的训练候选可从独立字段查询，例如：

```sql
SELECT url, sha256, model_sha256, label AS predicted_label,
       confidence, correction AS training_label, reviewer, reviewed_at
FROM ifk_image_predictions
WHERE correction <> '';
```

## 管理接口

以下接口均位于独立 Basic Auth 管理范围，使用标准 `status/message/data` 响应封装：

| 方法与路径 | 用途 |
| --- | --- |
| `GET /image-review/api/results` | 返回 `data.page.items` 及 `data.page.has_more` |
| `GET /image-review/api/results/:id` | 返回 `data.record` |
| `PUT /image-review/api/results/:id/annotation` | 保存人工标签，返回 `data.saved=true` |

列表参数：`label`、`min_confidence`（默认 0）、`max_confidence`（默认 1）、`review=all\|pending\|reviewed`、`sort=newest\|oldest\|confidence_asc\|confidence_desc`、`limit`（1–100，默认 24）。下一页使用上一页最后一条记录的 `after_id`、`after_confidence`，保持其原始精度，并保持相同筛选与排序。

标注请求须使用 `Content-Type: application/json`、`X-Requested-With: XMLHttpRequest`，JSON 为 `{"label":"0_invalid","revision":0}`。人工标签必须属于审核类型集合；管理员身份由 Basic Auth 获取。浏览器 Origin 存在时必须与本站点一致，反向代理应正确传递外部 Host 和 `X-Forwarded-Proto`。缺少管理凭据返回 401，跨站写入返回 403，参数错误返回 400，记录不存在返回 404，并发版本冲突返回 409，数据库失败返回 503。

## 验证

```bash
go test ./...
golangci-lint run ./...
golangci-lint run --enable-only=dupl ./...
cd storage/ui && npm run build
```

设置 `IMAGE_REVIEW_TEST_DATABASE=1` 和 `DB_LOCAL_*` 后，`go test ./db -run 'TestImage|TestConcurrentImage' -v` 额外验证真实 MySQL 的去重、模型隔离、同 URL 重复标注覆盖、同 URL 不同记录并发冲突、人工标注与四种游标排序。常规测试从已迁移的 `ifk_image_predictions` 克隆连接私有的临时表，使用单独连接池和回滚事务；并发测试使用独立随机命名的测试表及多个连接，完成后删除测试表。不写应用结果表。生产构建包含页面行为测试与生成 CSS 检查。
