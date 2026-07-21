# NetAssets

集团级轻量网络资产管理与填报平台。集团统一维护组织与数据标准，子公司按权限登记业务系统及网络资产，后续通过填报任务完成提交、审核和汇总。

## 当前已实现

- Go + Gin 模块化单体后端
- Vue 3 + TypeScript 管理端
- PostgreSQL 17 数据库与 Goose 迁移
- 集团根组织及集团管理员首次启动初始化
- 基于服务端 Session Cookie 的登录、会话恢复和退出
- 集团管理员查看组织、新增子公司/部门，并在线维护集团名称、简称、负责人和联系方式
- 集团管理员创建子公司管理员或填报员账号
- 子公司账号按所属组织隔离数据，禁止跨组织访问
- 业务系统登记及个人信息、敏感个人信息、重要数据、核心数据属性
- 个人信息记录数与“数据量是否超过 10 万条”自动联动
- 网络资产在线新增、编辑、状态维护、归档/重新启用和软删除
- CSV/XLSX 批量导入、集团级重复检测、部分成功反馈和导入历史
- 资产按所属组织和关联业务系统管理，集团可统管、子公司仅可维护本组织数据
- 集团管理员创建填报任务、选择目标组织并立即下发或保存草稿
- 子公司仅查看分配给本组织的填报任务，不能越权创建集团任务
- 登录、组织、用户、业务系统和网络资产操作审计日志
- 健康检查和系统信息接口
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
- `group_admin` 系统角色及基础权限

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

## 当前 API

```text
POST /api/v1/auth/login
GET  /api/v1/auth/me
POST /api/v1/auth/logout
GET    /api/v1/organizations
POST   /api/v1/organizations
PATCH  /api/v1/organizations/:id
GET  /api/v1/users
POST /api/v1/users
GET    /api/v1/business-systems
POST   /api/v1/business-systems
GET    /api/v1/assets
POST   /api/v1/assets
GET    /api/v1/assets/imports
GET    /api/v1/assets/import-template
POST   /api/v1/assets/import
GET    /api/v1/assets/:id
PATCH  /api/v1/assets/:id
DELETE /api/v1/assets/:id
GET    /api/v1/inventory-tasks
POST   /api/v1/inventory-tasks
```

用户管理、集团基本信息维护和填报任务创建仅允许集团管理员操作。集团管理员可维护所有组织数据；组织用户只能查看所属组织，并只能在授权组织内登记业务系统、维护网络资产、查看本组织导入记录及集团下发的填报任务。资产删除采用软删除，资产类型与标准化后的资产值在集团范围内去重。

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
