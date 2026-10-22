# NetAssets

集团级轻量网络资产管理、数据填报与安全运营平台。

NetAssets 面向集团总部与下属子公司、办事处等多组织场景：集团统一维护组织、权限和数据治理规则，组织用户维护本组织业务系统及网络资产，通过填报任务选择上报内容，经集团审核后形成集团可见台账；集团安全运营人员可基于已上报的互联网资产开展安全测试、漏洞登记和安全工单闭环。

> **项目定位**：轻量、可私有化部署、以台账和协作为核心。不包含默认运行的自动资产发现插件；自动发现能力预留为后续可插拔扩展。

## 核心业务闭环

```text
组织与权限
    ↓
业务系统 / 网络资产登记
    ↓
组织工作版本与填报上报
    ↓
集团审核、发布与数据汇总
    ↓
数据质量巡检与整改
    ↓
安全测试、漏洞关联与安全工单
（子单位上线新系统可反向发起测试申请，批准上线后自动上报）
    ↓
子公司整改、集团复测、审计留痕
```

## 功能模块

### 集团与多组织管理

- 集团、子公司、办事处、部门等多级组织树。
- 集团管理员可新增、编辑、启用、停用、恢复、迁移和调整组织类型。
- 停用组织前检查其用户、资产、业务系统和未完成任务；空组织可删除或放入回收站。
- 支持组织范围内的多组织任职，一个用户可以在授权范围内承担多个组织岗位。
- 集团管理员拥有集团范围管理能力；子公司管理员在被授权的组织树范围内管理本组织用户、组织和台账。
- 组织用户只能访问授权组织范围内的数据，服务端同时执行组织和角色权限校验。
- 用户可在账户设置中修改自己的密码和联系方式；角色、组织任职等授权信息由有权限的管理员维护。
- 集团基本信息可在组织架构页面维护，初始化环境变量不会覆盖后续页面上的人工修改。

### 资产台账与工作版本

- 分开管理业务系统和网络资产，业务系统编码、名称、负责人和安全属性独立维护。
- 网络资产支持 URL、域名、IP 等类型；资产值、资产名称、关联业务系统分别展示和管理。
- 网络范围支持公网、内网、未知；网络区域支持由集团按组织配置的办事处、园区、云 VPC 和网络分区等自定义项。
- 业务系统支持个人信息、敏感个人信息、重要数据、核心数据等属性；个人信息记录数与“数据量是否超过 10 万条”联动校验。
- 资产支持首次发现、最近发现、来源和可选有效期 `expires_at`；数据质量规则可检查资产到期情况。
- 正式台账中的域名/URL 可通过 RDAP 与公网 TLS 握手补充“域名注册到期”和“TLS 证书到期”；两类外部时间与人工维护的 `expires_at` 分离保存，不互相覆盖。
- 组织用户在日常台账中新增或修改的内容默认进入组织工作版本，不会因为保存就直接对集团可见。
- 组织用户可在填报任务中选择需要上报的系统和资产；已上报资源再次上报时形成更新，未发生变化的资源无需重复操作。
- 集团审核通过后发布到正式台账；支持差异查看、提交快照、退回和审计留痕。
- 支持归档、恢复、软删除、回收站和资源变更历史；集团批量操作返回逐项处理结果。

### 导入、导出与分页

- 业务系统和网络资产支持 CSV/XLSX 导入、标准模板、列映射、逐行错误反馈、重复检测、部分成功和导入历史。
- 所有主要列表统一使用 `page`、`page_size`、`total`、`items` 分页契约，默认每页 20 条，单页最多 100 条。
- CSV 导出直接流式写入；大型 XLSX 使用后台导出任务，按数据库分页生成文件，避免一次性占用大量内存。
- 网络资产、业务系统和填报任务导出均沿用当前筛选条件，并受服务端组织权限约束。
- 后台导出任务完成或失败会生成站内消息，文件默认保留 7 天。

### 填报任务

- 集团创建任务、选择目标组织、选择填报资源类型并立即下发或保存草稿。
- 网络资产专项任务支持核查范围：全部、仅公网、仅内网。
- 创建任务时可按组织、业务系统、网络区域等条件预览资源规模；正式提交时重新按最新台账生成不可变快照。
- 子公司可选择本次上报的业务系统和网络资产；未选择的资源不进入本次任务快照、统计和审核范围。
- 支持草稿、开始填报、提交、集团审核、退回、差异查看和结果导出。
- 提交前复用数据质量规则检查必填字段、负责人、安全属性和公网资产关联关系。
- 任务截止提醒、提交待审核、退回和审核结果接入消息中心。

### 数据治理

- 集团统一维护业务系统和网络资产自定义字段。
- 支持“字段必填”“长期未更新”“资产到期”等可配置数据质量规则；资产到期可分别检查人工生命周期、域名注册到期和 TLS 证书到期。
- 规则可按组织、网络区域、资产类型、网络范围、环境、重要程度、业务系统状态、数据属性和自定义字段限定适用范围。
- 支持手动巡检和按计划执行的全集团自动巡检，多实例通过 PostgreSQL 事务锁避免重复执行；启用外部到期规则时，巡检先在数据库事务外受限刷新观测，再执行质量检查。
- 支持待确认资产、质量问题指派、开始整改、提交复核、复核通过、退回和豁免。
- 质量整改支持批量操作、逐项权限校验、处理事件、审计日志和责任人通知。

### 域名与证书外部观测

- 域名注册信息通过 RDAP 获取，IANA DNS bootstrap 结果缓存 24 小时，支持 IDN/Punycode 和可注册根域提取。
- TLS 证书只做公网握手读取证书有效期和主机名/链校验结果，不发送 HTTP 请求；`http://` URL 不探测 TLS，私网资产默认不探测 TLS。
- 探测只允许解析后的公网地址，禁止回环、私网、链路本地和文档网段，降低 SSRF 风险；泛域名只做根域 RDAP，TLS 记录为不支持。
- 采集失败保留最近一次成功的到期数据，只更新失败原因和重试时间，不阻断质量巡检批次，也不修改 `assets.updated_at`。
- 手动质量巡检最多刷新 100 项、超时 25 秒；自动巡检最多 1,000 项、超时 10 分钟，并发上限 8。生产环境需要允许应用访问公网 RDAP/HTTPS；若只能经代理出网，需在部署层提供可信受控代理。

### 安全运营

- 集团安全运营角色独立于集团管理员，负责安全测试、漏洞治理和安全工单，不必由集团管理员包办全部安全运营工作。
- 安全测试批次支持按资产、业务系统、组织和网络区域圈选公网测试目标，并冻结测试授权范围。
- 子公司可为本组织新系统上线发起上线安全测试申请：圈选本组织公网资产与业务系统（含未上报工作版本）、提交前执行必填项自检，集团审批通过后自动生成已授权测试批次；测试完成且发现全部闭环后，集团批准上线即自动创建“上线发布”填报任务并提交范围内工作版本进入集团审核，全程状态、事件和站内消息留痕。
- 漏洞导入支持严重等级、逐行校验、重复识别、部分成功和导入历史。
- 精确匹配的漏洞直接入库；无匹配或多匹配项进入“待人工关联”，只能关联当前测试批次授权资产。
- 支持漏洞登记、风险接受、责任组织通知和审计留痕。
- 安全工单支持下发、接收、整改、上传附件、提交复测、集团复测、退回和取消。
- 资产归档、删除、恢复、状态或区域调整，以及资产上的安全发现会向责任组织发送站内消息，避免台账和安全处置闭环断开。

### 平台与运维

- 消息中心、审计日志、API Token、登录设备管理和账户设置。
- 台账列表页统一“筛选卡 + 数据表卡”结构；轻量统计就地并入筛选区（待确认资产筛选卡右侧显示待处理数，安全工单在保存筛选视图下方显示处理中/待复测/逾期），不设独立统计卡。
- 健康检查、系统信息、数据库迁移和备份恢复。
- SPA 生产路由支持直接访问和刷新，静态资源使用 `/_assets/` 命名空间，避免与 `/assets` API 路径冲突。
- Podman Compose 私有化部署；Playwright 端到端测试在容器中运行。

## 技术架构

| 层次 | 技术 |
| --- | --- |
| 管理端 | Vue 3、TypeScript、Vite、Element Plus、Pinia、Vue Router、Axios |
| 后端 | Go 1.26、Gin 1.12、pgx |
| 数据库 | PostgreSQL 17 |
| 数据库迁移 | Goose 迁移库与项目内置 migrate 命令 |
| 文件处理 | CSV、Excelize XLSX |
| 部署 | Podman、Podman Compose |
| 端到端测试 | Playwright 容器 |
| 持续集成 | GitHub Actions、Buildx、GHCR |

项目采用模块化单体结构，后端领域模块位于 `backend/internal/`，前端页面位于 `web/src/views/`，数据库迁移位于 `backend/migrations/`。

## 快速开始

项目优先使用 Podman 和 `podman compose`，不要求安装 Docker 或 Docker Compose。

### 1. 准备配置

```bash
cp .env.example .env
```

至少修改首次初始化管理员密码：

```env
BOOTSTRAP_ADMIN_PASSWORD=请替换为至少10位的强随机密码
```

生产环境建议同时修改数据库密码、集团初始化信息，并设置：

```env
APP_ENV=production
SESSION_COOKIE_SECURE=true
```

### 2. 启动本地环境

```bash
podman machine start             # Podman machine 未运行时执行
podman compose up -d --build
```

应用启动后访问：

```text
http://127.0.0.1:8080
```

也可以使用 Makefile：

```bash
make up
make ps
make logs
```

### 3. 检查服务状态

```bash
curl http://127.0.0.1:8080/health/live
curl http://127.0.0.1:8080/health/ready
```

首次启动会自动执行数据库迁移，并根据 `BOOTSTRAP_*` 配置创建集团根组织、首个集团管理员及内置安全运营角色。`BOOTSTRAP_GROUP_*` 只用于首次初始化；初始化完成后，应在“组织架构”页面编辑集团基本信息。

### 持久化要求

`compose.yaml` 默认使用两个命名卷：

- `postgres-data`：PostgreSQL 数据库；
- `app-data`：应用附件、导出文件和备份等数据。

正式环境必须将数据库卷和应用 `/data` 卷纳入备份策略。不要使用 `podman compose down -v` 作为普通停止命令，它会删除本地数据卷。

## GHCR 镜像构建与使用

GitHub Actions 配置位于 [`.github/workflows/container-image.yml`](.github/workflows/container-image.yml)，发布镜像：

```text
ghcr.io/atsud0/netassets
```

工作流行为：

- Pull Request：构建多架构镜像进行验证，不推送；
- 推送到 `main`：发布 `latest`、`main` 和 `sha-*`；
- 推送 `v1.2.3`：发布 `1.2.3`、`1.2`、`1` 和 `sha-*`；
- 平台：`linux/amd64`、`linux/arm64`；
- 包含 provenance 和 SBOM；
- 支持 `workflow_dispatch` 手动运行。

拉取镜像：

```bash
podman pull ghcr.io/atsud0/netassets:latest
```

如果 GHCR 包为私有包：

```bash
printf '%s' "$GITHUB_TOKEN" | podman login ghcr.io \
  --username YOUR_GITHUB_USERNAME --password-stdin
```

当前仓库的 `compose.yaml` 默认使用 `deploy/Containerfile` 从源码构建应用，适合开发、验收和需要本地构建的部署场景。若使用 GHCR 镜像部署，请在运维侧 Compose 文件中将 `app` 服务的 `build` 替换为 `image: ghcr.io/atsud0/netassets:<tag>`，同时保留 PostgreSQL 与 `/data` 的持久化配置。

## 常用命令

```bash
make help                 # 查看全部命令
make up                   # 构建并启动应用
make down                 # 停止服务但保留数据卷
make build                # 只构建应用镜像
make ps                   # 查看容器状态
make logs                 # 跟踪应用日志
make migrate-up           # 执行一次数据库迁移
make migrate-down         # 回滚一条迁移
make test                 # Podman 内后端单元/数据库集成测试及前端测试/构建
make e2e                  # 容器内运行全部 Playwright 测试
make e2e-roles            # 容器内运行角色边界回归测试
make acceptance-preflight # 生产上线前配置验收
make acceptance-running   # 对运行中的部署执行验收
```

## 备份与恢复

备份包含 PostgreSQL 数据库和应用 `/data` 附件卷。备份脚本会通过 `podman compose` 协调应用和数据库，确保数据库记录与附件尽量保持一致。

```bash
# 默认生成到 ./backups/netassets-时间戳/
make backup

# 只校验备份清单、归档结构和 SHA-256，不修改当前环境
make restore-verify BACKUP=backups/netassets-20260723T000000Z

# 恢复前自动创建安全备份，并要求显式确认
NETASSETS_RESTORE_CONFIRM=YES make restore \
  BACKUP=backups/netassets-20260723T000000Z
```

恢复会覆盖当前数据库和附件。正式备份应复制到受控、加密并与应用主机隔离的存储中；不要将生产备份提交到 Git。

## 测试

### Podman 开发与测试

**容器是本项目测试、构建和应用运行的唯一入口。** 宿主机仅用于编辑代码、Git 和调用 `make`；不要在宿主机直接运行 Go、Node.js、测试、构建或应用服务器。开发运行使用 `make up`，停止使用 `make down`；后端/前端测试使用 `make test-backend`、`make test-frontend`，全量测试使用 `make test`，Playwright 使用 `make e2e` 或 `make e2e-roles`，镜像构建使用 `make build`。迁移等运行时操作也应通过 Makefile/Podman Compose 入口执行。

```bash
make test-backend          # 全部后端单元测试 + 数据库集成测试
make test-frontend         # npm ci、前端单元测试及生产构建
make fmt                   # 容器内 gofmt
```

上述命令使用 `compose.dev.yaml` 和独立的 `netassets-dev` 项目。测试 PostgreSQL 使用临时内存卷，不连接现有应用数据库；Go 与 npm 依赖使用独立缓存卷。后端先运行单元测试，再注入 `NETASSETS_TEST_DATABASE_URL` 对全部包执行数据库集成测试；第二轮使用 `-p 1 -count=1`，避免共用测试数据库的包并发清理数据，也避免测试缓存掩盖实际结果。

```bash
# 清理开发测试容器；保留工具链缓存卷
podman compose -p netassets-dev -f compose.dev.yaml down
```

### Playwright 端到端测试

Playwright 浏览器不安装到宿主机，统一在容器中执行：

```bash
make e2e
```

角色边界回归测试：

```bash
make e2e-roles
```

测试账号从 `.env` 的 `BOOTSTRAP_ADMIN_USERNAME` 和 `BOOTSTRAP_ADMIN_PASSWORD` 读取。E2E 使用独立的 `compose.e2e.yaml` 数据卷，测试结束后 Makefile 会清理测试容器和结果目录。

清理本地普通环境中由 E2E 生成的测试数据前，先预览：

```bash
make cleanup-e2e
```

确认结果后再显式删除：

```bash
NETASSETS_E2E_CLEANUP_CONFIRM=YES ./scripts/cleanup-e2e-data.sh --apply
```

脚本只匹配固定的 E2E 命名规则，不会删除普通业务数据。执行前建议先 `make backup`。

## 环境变量

`.env.example` 是完整配置示例，常用变量如下：

| 变量 | 作用 | 默认值 |
| --- | --- | --- |
| `APP_ENV` | 运行环境 | `development` |
| `APP_HOST_PORT` | 宿主机访问端口 | `8080` |
| `DATABASE_URL` | 应用数据库连接串 | Compose 内部 PostgreSQL 地址 |
| `POSTGRES_DB` / `POSTGRES_USER` / `POSTGRES_PASSWORD` | PostgreSQL 初始化参数 | `netassets` |
| `BOOTSTRAP_GROUP_*` | 首次创建集团根组织 | 示例值 |
| `BOOTSTRAP_ADMIN_*` | 首次创建集团管理员 | 用户自定义 |
| `SESSION_TTL` | Session 有效期 | `12h` |
| `SESSION_COOKIE_SECURE` | 是否仅通过 HTTPS 发送 Cookie | `false` |
| `DATA_QUALITY_AUTO_SCAN_ENABLED` | 是否启用每日质量巡检 | `true` |
| `DATA_QUALITY_AUTO_SCAN_TIME` | 自动巡检时间 | `02:00` |
| `DATA_QUALITY_AUTO_SCAN_TIMEZONE` | 自动巡检时区 | `Asia/Shanghai` |
| `BUILD_HTTP_PROXY` / `BUILD_HTTPS_PROXY` | 构建阶段 HTTP 代理 | `http://host.containers.internal:6152` |
| `BUILD_ALL_PROXY` | 构建阶段 SOCKS5 代理 | `socks5h://host.containers.internal:6153` |
| `E2E_HTTP_PROXY` / `E2E_HTTPS_PROXY` | Playwright 依赖安装代理 | `http://host.containers.internal:6152` |

### 安全注意事项

- `.env` 不应提交到 Git；
- `BOOTSTRAP_ADMIN_PASSWORD` 只在首次初始化时创建账号，不会在后续启动时重置已存在账号；
- 生产环境使用强随机密码和独立数据库密码；
- HTTPS 反向代理环境设置 `SESSION_COOKIE_SECURE=true`；
- 默认应用面向集团内部网络，不建议未经反向代理、访问控制和审计就直接暴露公网；
- API Token 只在创建时显示一次，数据库只保存摘要，请按最小权限和最短有效期使用。

## API 与健康检查

API 基础路径为 `/api/v1`，登录后可通过浏览器 Session Cookie 调用；个人 API Token 可用于普通业务接口的 Bearer 认证：

```bash
curl \
  -H "Authorization: Bearer nat_xxx" \
  http://127.0.0.1:8080/api/v1/auth/me
```

主要接口模块：

| 模块 | 典型路径 |
| --- | --- |
| 认证与账户 | `/api/v1/auth/*` |
| 组织、用户和角色 | `/api/v1/organizations`、`/api/v1/users`、`/api/v1/roles` |
| 业务系统 | `/api/v1/business-systems/*` |
| 网络资产与网络区域 | `/api/v1/assets/*`、`/api/v1/network-areas/*` |
| 导入、导出任务 | `/api/v1/*/import`、`/api/v1/export-jobs` |
| 填报任务 | `/api/v1/inventory-tasks/*` |
| 数据治理 | `/api/v1/data-quality-rules/*`、`/api/v1/data-quality-issues/*` |
| 安全测试与漏洞 | `/api/v1/security-test-batches/*`、`/api/v1/security-findings/*` |
| 上线安全测试申请 | `/api/v1/security-test-requests/*` |
| 安全工单 | `/api/v1/security-work-orders/*` |
| 消息、审计和回收站 | `/api/v1/notifications/*`、`/api/v1/audit-logs`、`/api/v1/recycle-bin` |
| 运维 | `/health/live`、`/health/ready`、`/api/v1/system/info` |

普通列表接口统一返回真实分页结果：

```json
{
  "page": 1,
  "page_size": 20,
  "total": 123,
  "items": []
}
```

导出接口按授权范围生成完整文件，不受列表分页大小限制；大型 XLSX 会转为后台导出任务。

## 网络代理

如果本机代理监听端口与项目默认值一致，无需额外配置：

- 宿主机 HTTP：`127.0.0.1:6152`；
- 宿主机 SOCKS5：`127.0.0.1:6153`；
- 容器访问宿主机：`host.containers.internal`。

可通过 `.env` 覆盖 `BUILD_*_PROXY`、`E2E_*_PROXY` 和对应的 `*_NO_PROXY`。

## 项目结构

```text
.
├── backend/
│   ├── cmd/                 # server、migrate 入口
│   ├── internal/            # 认证、组织、资产、治理、安全等领域模块
│   ├── migrations/          # 数据库迁移
│   └── queries/             # SQL 查询
├── web/
│   ├── src/views/           # 管理端页面
│   ├── src/components/      # 通用组件
│   └── src/router/          # 路由与权限入口
├── deploy/Containerfile     # 多阶段镜像构建文件
├── compose.yaml             # 本地/源码构建部署
├── compose.e2e.yaml         # Playwright 隔离测试部署
├── e2e/tests/               # 端到端和角色回归测试
├── scripts/                 # 备份、恢复、验收和测试数据脚本
└── docs/                    # 建设方案与上线验收文档
```

## 文档

- [集团级轻量网络资产管理系统建设方案](docs/集团级轻量网络资产管理系统建设方案.md)
- [生产上线验收与全角色回归清单](docs/生产上线验收与全角色回归清单.md)
- [开发人员与外包厂商修复工作台开发方案](docs/开发人员与外包厂商修复工作台开发方案.md)（含实现说明、接口映射与验证结果）
- [环境变量示例](.env.example)
- [Podman Compose 配置](compose.yaml)
- [GitHub Actions 镜像构建](.github/workflows/container-image.yml)

## 个人修复工作台

- 集团创建安全工单时选择「派给具体人员」执行模式，下发给责任组织。
- 集团管理员统一维护厂商档案、组织合作与成员状态；组织管理员在「修复人员与厂商」创建人员或发出一次性邀请，并配置业务系统及授权期限。
- 在「修复任务」接收主工单，逐项或批量选择人员，确认共享材料后派发。
- 执行人员登录后进入 `/repair/tasks`，仅能处理当前有效授权且明确分配给自己的任务。
- 修复提交先经组织确认，再由集团逐项复测；全部漏洞处置完成后才能关闭主工单。

数据库新增迁移 `00051`、`00052`、`00053`。旧工单默认保持 `organization` 模式，新功能使用 `individual` 模式。部署时须同步更新后端和前端并执行迁移；现有数据升级前按备份流程保存数据库及附件。

## 后续规划

以下能力不属于当前默认运行闭环，后续按插件或独立迭代实施：

1. 面向指定域名、IP、URL 的自动资产发现插件；
2. 外部扫描器和 CMDB 数据源的定时同步；
3. 资产发现结果与待确认资产的自动去重、相似匹配和责任组织路由；
4. 按需接入可信外部资产观测 API 与多观测源合并策略。

修复工作台还支持预计完成时间与阻塞计划、共享材料复核及附件版本、提交差异比较、逐项审核队列、实际复测环境/时间、组织范围统计及可配置逾期汇总。完整实施和恢复演练记录见开发方案第 18 节。迁移 53 回退须恢复匹配的数据库/附件备份及旧镜像。
