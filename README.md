# NetAssets

集团级轻量网络资产管理与填报平台。集团统一维护组织与数据标准，子公司按权限登记业务系统及网络资产，后续通过填报任务完成提交、审核和汇总。

## 当前已实现

- Go + Gin 模块化单体后端
- Vue 3 + TypeScript 管理端
- PostgreSQL 17 数据库与 Goose 迁移
- 集团根组织及集团管理员首次启动初始化
- 基于服务端 Session Cookie 的登录、会话恢复和退出
- 个人 API Token：在账户设置中创建、分页查看和撤销，明文仅展示一次、数据库仅保存 SHA-256 摘要；支持 Bearer 认证，固定绑定创建时的当前工作组织并实时继承账号角色与数据权限
- 集团管理员查看组织、新增子公司/部门，并在线维护集团名称、简称、负责人和联系方式
- 集团管理员创建子公司管理员或填报员账号
- 子公司账号按所属组织隔离数据，禁止跨组织访问
- 业务系统详情、新增、编辑、归档/恢复、软删除及关联网络资产查看；支持单次 1～200 个系统批量归档或恢复，并返回逐项处理结果
- 业务系统个人信息、敏感个人信息、重要数据、核心数据属性
- 个人信息记录数与“数据量是否超过 10 万条”自动联动
- 网络资产在线新增、编辑、状态维护、归档/重新启用和软删除；支持单次 1～200 个资产批量调整状态或设置/清除网络区域
- 集团管理员回收站：统一查看已删除网络资产和业务系统；恢复资产时防止与在用台账重复，恢复业务系统后保持“已归档”并记录统一审计
- 网络暴露范围固定为公网、内网、未知；集团管理员可按组织维护网络区域，子公司可选择本组织已启用的办事处、园区、云 VPC 或网络分区
- 业务系统与网络资产 CSV/XLSX 批量导入、标准模板、集团级重复检测、逐行错误反馈、部分成功和导入历史
- 业务系统与网络资产台账 CSV/XLSX 导出；网络资产导出支持当前组织、类型、状态和关键字筛选，子公司导出范围受服务端数据权限约束
- 填报任务结果 CSV/XLSX 导出；一行对应一个目标组织，包含任务进度、提交数量快照、审核结果和填报说明
- 资产按所属组织和关联业务系统管理，集团可统管、子公司仅可维护本组织数据
- 集团管理员创建填报任务、选择目标组织并立即下发或保存草稿
- 填报任务支持“全部网络资产、仅公网资产、仅内网资产”核查范围；范围同步作用于提交前质量检查、提交数量、不可变快照、审核明细、提交差异及结果导出，未选择的资源类型不检查、不统计、不进入快照
- 集团创建填报任务时，可按目标组织、填报资源类型和网络资产核查范围预览当前台账规模，逐组织查看业务系统数与网络资产数并识别空范围组织；预览不固化数据，实际提交仍按届时最新台账重新计算
- 填报任务已接入站内消息：集团接收子公司提交待审核提醒，子公司在 `inventory.manage` 授权组织范围内接收新任务、审核退回、3 日内截止和已逾期提醒；点击消息可自动定位并展开具体任务及目标组织
- 子公司提交前自动检查必填台账、负责人、数据安全属性和公网资产关联情况；错误阻止提交、提醒项允许提交
- 集团管理员可统一维护网络资产/业务系统自定义字段，并配置“字段必填”与“长期未更新”数据质量规则
- 数据质量规则支持最多 10 个白名单生效条件，多个条件按 AND 组合，可按组织、网络区域、资产类型、网络范围、环境、重要程度、业务系统状态、数据属性及自定义字段限定适用范围；必填规则在主动巡检与填报提交前复用同一判定语义
- 数据质量问题支持单次 1～200 项批量指派负责人、批量开始整改和批量提交集团复核；服务端逐项执行组织权限与状态机校验，允许部分成功，并为每个成功项写入处理事件和审计日志
- 网络资产、安全工单和质量整改支持用户私有的保存筛选视图；每人每页最多 20 个，可设默认、应用和删除，筛选字段由服务端白名单校验且不能扩大原有组织数据范围
- 子公司开始填报并提交当前业务系统、网络资产数量快照及填报说明
- 每次提交生成不可变的业务系统与网络资产明细快照；退回重提形成新版本，并自动与本组织最近一次提交比较新增、修改和删除/归档项
- 集团审核人员和授权组织用户可在填报任务中查看提交差异，修改项按字段展示前后值；历史存量提交未生成明细快照时保持兼容且不显示差异入口
- 集团退回填报时可针对当前提交中的具体业务系统或网络资产填写逐条整改意见；意见与提交快照版本绑定，子公司可查看全部历史意见后维护台账并重新提交
- 集团审核通过或退回，子公司整改后可重新提交；全部通过后任务自动完成
- 集团与子公司工作台按权限汇总组织、业务系统、网络资产、填报待办、待审核和逾期指标
- 集团管理员可下发草稿、关闭进行中任务，并归档已关闭或已完成任务
- 子公司仅查看分配给本组织的非草稿任务，不能越权创建或审核集团任务
- 登录、组织、用户、业务系统、网络资产、导入导出和填报审核操作审计日志；填报提交审计同时记录快照 ID、版本、上一快照和资源数量
- 集团管理员审计日志查询页：支持按关键字、操作类型、资源类型、操作人、所属组织、结果和日期范围筛选，并可查看请求与扩展详情
- 具备 `security_finding.manage` 的集团管理员或集团安全运营管理员可基于子公司网络资产或业务系统登记安全发现，维护漏洞等级、证据、整改建议和风险接受
- 具备 `security_workorder.manage` 的集团管理员或集团安全运营管理员可创建安全工单草稿或立即下发；子公司可接收、更新整改进度并提交复测，集团安全运营人员可退回或复测通过后关闭
- 安全工单按责任组织隔离，支持动态逾期标识、关联发现和完整处理时间线，关键动作同步进入统一审计日志
- 子公司可上传整改证明，集团可上传复测或参考材料；附件支持受控下载、删除、20MB 限制、扩展名白名单和审计留痕，文件持久化到 `app-data` 数据卷
- 集团与子公司工作台新增安全整改待办，展示未关闭、待复测、3 日内到期和已逾期工单数量
- 安全治理工作台：按当前数据权限展示漏洞严重等级/状态分布、近 30 天新增与复测关闭趋势、安全测试公网资产覆盖率、测试发现关闭率，以及子公司整改及时率和逾期工单排行；卡片和表格可跳转至已筛选的发现/工单明细
- 轻量站内消息中心：按集团/子公司数据范围动态生成填报任务、安全工单、风险复核、数据质量和台账变更消息，支持未读筛选、分类筛选、单条/全部已读及跳转业务详情
- 具备 `security_test.manage` 的集团管理员或集团安全运营管理员可创建安全测试批次，并通过安全域只读参考接口从未归档公网 URL、IP、域名等资产中选择 1～200 项测试目标
- 安全测试批次支持草稿、已授权、测试中、已完成和已取消状态；授权后范围冻结，不能再修改
- 每个测试批次固化资产值、资产类型、责任组织、业务系统、网络区域、环境和重要程度快照，并保留状态事件与统一审计记录
- 具备 `security_test.manage` 的集团安全运营人员（含集团管理员应急接管）可在已授权、测试中或已完成的安全测试批次中上传测试报告或参考材料；支持受控下载、删除、20MB 限制、扩展名白名单、批次事件和审计留痕，文件持久化到 `app-data` 数据卷
- 安全发现可关联安全测试批次，服务端校验所选资产必须属于已授权范围
- 具备 `security_finding.manage` 的集团安全运营人员可按测试批次批量导入 CSV/XLSX 漏洞，系统自动匹配授权资产、责任组织和业务系统
- 漏洞导入支持 10MB/5000 行限制、中文严重等级、逐行错误、部分成功、重复检测、导入历史和汇总审计
- 健康检查和系统信息接口
- SPA 生产路由支持直接访问和刷新；构建静态资源使用 `/_assets/` 独立命名空间，避免与网络资产页面 `/assets` 冲突
- Podman Compose 私有化部署
- 容器化 Playwright 端到端测试

## 技术栈

- Frontend: Vue 3、TypeScript、Vite、Element Plus、Pinia、Vue Router、Axios
- Backend: Go、Gin、pgx、Goose
- Database: PostgreSQL 17
- Deployment: Podman Compose
- E2E: Playwright（仅在容器内运行）

## 首次启动

项目只使用 Podman 和 `podman compose`，不要求安装 Docker。

```bash
cp .env.example .env
```

启动前必须编辑 `.env`，至少将以下值替换为长度不少于 10 位的密码：

```env
BOOTSTRAP_ADMIN_PASSWORD=请替换为至少10位的强密码
```

然后启动服务：

```bash
podman machine start
podman compose up -d --build
```

访问：`http://127.0.0.1:8080`

首次启动时系统会创建：

- 集团根组织：由 `BOOTSTRAP_GROUP_*` 配置决定
- 集团管理员：由 `BOOTSTRAP_ADMIN_*` 配置决定
- `group_admin` 集团管理员和 `security_operator` 集团安全运营管理员系统角色及基础权限

> 初始化密码只用于创建首个管理员；用户已存在时不会在后续启动中重置其密码。生产环境请使用密钥管理或受控环境变量，不要提交 `.env`。

> `BOOTSTRAP_GROUP_*` 只负责首次初始化集团根组织。初始化完成后，请在“组织机构 → 编辑集团信息”中维护集团名称等资料，应用重启不会再用 `.env` 覆盖人工修改。

## 常用命令

```bash
# 查看状态
podman compose ps

# 查看应用日志
podman compose logs -f app

# 停止服务（保留数据卷）
podman compose down

# 停止并删除本地数据卷（会清空数据库）
podman compose down -v
```

健康检查：

```bash
curl http://127.0.0.1:8080/health/live
curl http://127.0.0.1:8080/health/ready
```

## 备份与恢复

备份同时包含 PostgreSQL 数据库和 `/data` 附件卷。为保证数据库记录与附件一致，备份期间会短暂停止应用容器，完成后自动恢复；所有容器操作均通过 `podman compose` 执行。

```bash
# 生成备份，默认写入 ./backups/netassets-时间戳/
make backup

# 仅校验归档结构和 SHA-256，不修改当前数据
make restore-verify BACKUP=backups/netassets-20260723T000000Z

# 恢复前默认自动再做一份安全备份，并要求显式确认
NETASSETS_RESTORE_CONFIRM=YES make restore BACKUP=backups/netassets-20260723T000000Z
```

恢复会覆盖当前数据库和全部附件。备份目录权限默认为仅当前用户可访问，`backups/` 已加入 Git 忽略列表；请将正式备份复制到受控、加密且与应用主机隔离的存储位置。

## 当前 API

```text
POST   /api/v1/auth/login
GET    /api/v1/auth/me
POST   /api/v1/auth/logout
GET    /api/v1/auth/api-tokens?page=&page_size=
POST   /api/v1/auth/api-tokens
DELETE /api/v1/auth/api-tokens/:id
GET    /api/v1/organizations
POST   /api/v1/organizations
PATCH  /api/v1/organizations/:id
GET  /api/v1/users
POST /api/v1/users
GET    /api/v1/business-systems
POST   /api/v1/business-systems
GET    /api/v1/business-systems/export?format=csv|xlsx
GET    /api/v1/business-systems/imports
GET    /api/v1/business-systems/:id/change-history
GET    /api/v1/business-systems/import-template
POST   /api/v1/business-systems/import
GET    /api/v1/business-systems/:id
GET    /api/v1/business-systems/:id/assets?page=&page_size=
PATCH  /api/v1/business-systems/:id
POST   /api/v1/business-systems/batch/archive
POST   /api/v1/business-systems/:id/archive
POST   /api/v1/business-systems/:id/restore
DELETE /api/v1/business-systems/:id
GET    /api/v1/assets
POST   /api/v1/assets
GET    /api/v1/network-areas?organization_id=
POST   /api/v1/network-areas
PATCH  /api/v1/network-areas/:id
GET    /api/v1/assets/export?format=csv|xlsx&organization_id=&type=&status=&q=
GET    /api/v1/assets/imports
GET    /api/v1/assets/:id/change-history
GET    /api/v1/assets/import-template
POST   /api/v1/assets/import
PATCH  /api/v1/assets/batch
GET    /api/v1/assets/:id
PATCH  /api/v1/assets/:id
DELETE /api/v1/assets/:id
GET    /api/v1/recycle-bin
POST   /api/v1/recycle-bin/assets/:id/restore
POST   /api/v1/recycle-bin/business-systems/:id/restore
GET    /api/v1/dashboard/summary
GET    /api/v1/audit-logs/options
GET    /api/v1/audit-logs?page=&page_size=&action=&resource_type=&result=&user_id=&organization_id=&q=&start_at=&end_at=
GET    /api/v1/data-quality-issues?page=&page_size=&organization_id=&status=&severity=&resource_type=&assignee=
GET    /api/v1/data-quality-issues/:id
POST   /api/v1/data-quality-issues/batch
GET    /api/v1/data-quality-issues/:id/assignees?page=&page_size=
GET    /api/v1/data-quality-issues/:id/events?page=&page_size=
POST   /api/v1/data-quality-issues/:id/assign
POST   /api/v1/data-quality-issues/:id/start
POST   /api/v1/data-quality-issues/:id/submit
POST   /api/v1/data-quality-issues/:id/review
POST   /api/v1/data-quality-issues/:id/exempt
POST   /api/v1/data-quality-issues/:id/reopen
GET    /api/v1/data-quality-scan-batches?page=&page_size=
GET    /api/v1/inventory-tasks?page=&page_size=
POST   /api/v1/inventory-tasks/scope-preview
POST   /api/v1/inventory-tasks
GET    /api/v1/inventory-tasks/:taskID/export?format=csv|xlsx
GET    /api/v1/inventory-tasks/:taskID/organizations?page=&page_size=
GET    /api/v1/inventory-tasks/:taskID/organizations/:organizationID/quality-check
GET    /api/v1/inventory-tasks/:taskID/organizations/:organizationID/submission-diff
GET    /api/v1/inventory-tasks/:taskID/organizations/:organizationID/submission-diff/items?resource_type=&change_type=&page=&page_size=
GET    /api/v1/inventory-tasks/:taskID/organizations/:organizationID/review-context
GET    /api/v1/inventory-tasks/:taskID/organizations/:organizationID/review-resources?q=&page=&page_size=
GET    /api/v1/inventory-tasks/:taskID/organizations/:organizationID/review-comments?page=&page_size=
POST   /api/v1/inventory-tasks/:taskID/organizations/:organizationID/start
POST   /api/v1/inventory-tasks/:taskID/organizations/:organizationID/submit
POST   /api/v1/inventory-tasks/:taskID/organizations/:organizationID/review
PATCH  /api/v1/inventory-tasks/:taskID/status

GET    /api/v1/security-findings?organization_id=&status=
POST   /api/v1/security-findings
PATCH  /api/v1/security-findings/:id
GET    /api/v1/security-work-orders?organization_id=&status=
GET    /api/v1/security-work-orders/:id
POST   /api/v1/security-work-orders
POST   /api/v1/security-work-orders/:id/issue
POST   /api/v1/security-work-orders/:id/accept
POST   /api/v1/security-work-orders/:id/progress
POST   /api/v1/security-work-orders/:id/submit
POST   /api/v1/security-work-orders/:id/review
POST   /api/v1/security-work-orders/:id/cancel
POST   /api/v1/security-work-orders/:id/attachments
GET    /api/v1/security-work-orders/:id/attachments/:attachmentID/download
DELETE /api/v1/security-work-orders/:id/attachments/:attachmentID
GET    /api/v1/dashboard/summary
GET    /api/v1/dashboard/security-overview
GET    /api/v1/notifications?unread_only=&category=&limit=
GET    /api/v1/notifications/summary
POST   /api/v1/notifications/read
POST   /api/v1/notifications/read-all
```

普通列表接口统一采用 `page` / `page_size` 服务端分页，默认每页 20 条、单页最多 100 条，并返回真实 `total`。业务系统详情的关联资产、填报任务的目标组织进度也使用独立分页接口，详情/任务主列表不再嵌入无界集合；导出接口仍按授权范围生成完整文件。

个人 API Token 可通过 Bearer 方式调用普通业务接口，例如：

```bash
curl \
  -H "Authorization: Bearer nat_xxx" \
  http://127.0.0.1:8080/api/v1/auth/me
```

Token 管理、个人资料、修改密码、登录设备、切换当前组织和退出登录等账户安全接口仅允许浏览器 Session 调用，不能使用 API Token 自行创建或撤销 Token。

用户管理、集团基本信息维护、填报任务创建和审核、审计日志查询、回收站恢复及网络区域配置仍由集团管理员负责。安全测试、漏洞与风险接受、安全工单下发/复测分别受 `security_test.manage`、`security_finding.manage`、`security_workorder.manage` 控制，并且仅 `group_admin` 或内置 `security_operator` 集团角色可获得集团范围安全数据；组织自定义角色即使配置同名权限也不能扩大为集团范围。集团管理员创建仅承担集团职责的账号时，可只分配集团范围角色而不附加任何子公司组织任职，避免隐式获得组织岗位权限。消息中心按当前用户组织数据范围展示，集团管理员和集团安全运营管理员看到集团级复测与风险复核待办，子公司用户只看到本组织工单消息。子公司管理员及填报员可以开始和提交分配给授权组织的填报任务，也可以接收本组织安全工单、更新整改进度、上传整改证明并提交复测。集团管理员可维护所有组织数据，并可按子公司配置网络区域；组织用户只能查看所属组织，并只能在授权组织内登记或导入业务系统、维护或导入网络资产、选择本组织已启用的网络区域、查看本组织导入记录及集团下发的填报任务。网络暴露范围用于描述公网、内网、未知等安全属性，不与办公地点或网络分区混用。业务系统和资产编辑会按字段保存修改前后值，集团管理员及资源授权组织用户均可在对应台账中查看最近 100 条资源历史。业务系统和资产删除均采用软删除；业务系统仍有关联资产时禁止删除，归档不会删除关联关系。集团管理员可在回收站恢复误删资产或业务系统；恢复业务系统后默认保持“已归档”，恢复前若发现同一在用资产或系统编码冲突则会拒绝恢复。资产类型与标准化后的资产值在集团范围内去重。

## 本地后端测试

```bash
cd backend
go test ./...
```

如果宿主机没有 Node.js，可通过 Node 容器执行前端检查：

```bash
podman run --rm \
  -e HTTP_PROXY=http://host.containers.internal:6152 \
  -e HTTPS_PROXY=http://host.containers.internal:6152 \
  -v "$PWD/web:/workspace:Z" \
  -w /workspace \
  docker.io/library/node:22-alpine \
  sh -lc 'npm ci && npm run build && npm test -- --run'
```

## Playwright

Playwright 不在宿主机安装浏览器，统一通过容器运行：

```bash
podman compose --profile e2e run --rm playwright
```

或：

```bash
make e2e
```

测试账号从 `.env` 中的 `BOOTSTRAP_ADMIN_USERNAME` 和 `BOOTSTRAP_ADMIN_PASSWORD` 读取。

### 清理本地 E2E 测试数据

E2E 测试数据不会在测试结束后自动清除。需要清理本地开发库时，先预览严格匹配的测试组织、任务、资产、工单、附件及可归属审计记录：

```bash
make cleanup-e2e
# 或 ./scripts/cleanup-e2e-data.sh --dry-run
```

确认预览结果无误后，建议先执行 `make backup`，再显式执行删除：

```bash
NETASSETS_E2E_CLEANUP_CONFIRM=YES ./scripts/cleanup-e2e-data.sh --apply
```

脚本只匹配 E2E 生成的固定命名规则（如 `SUB_PW_时间戳`、`TASK_PW_时间戳`），并同步清理关联附件和可精确归属的审计记录；不会删除普通业务数据。

## 网络代理

默认按本机现有代理配置：

- 宿主机 HTTP：`127.0.0.1:6152`
- 宿主机 SOCKS5：`127.0.0.1:6153`
- 容器访问宿主机：`host.containers.internal`

可在 `.env` 中覆盖 `BUILD_*_PROXY` 和 `E2E_*_PROXY`。

## 文档

产品与技术方案位于：

```text
docs/集团级轻量网络资产管理系统建设方案.md
```
