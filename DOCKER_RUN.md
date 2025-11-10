# Docker 运行指南

## 前置要求

1. 安装 Docker 和 Docker Compose
2. 确保配置文件 `config.json` 存在（可以从 `config.json.example` 复制）

## 快速开始

### 1. 准备配置文件

```bash
# 如果 config.json 不存在，从示例文件复制
cp config.json.example config.json

# 根据需要编辑配置文件
# 注意：config.json 会被挂载到容器中，修改后需要重启容器
```

### 2. 构建 Docker 镜像

```bash
# 方式一：使用提供的脚本
./docker-build.sh

# 方式二：手动构建
docker-compose build
```

### 3. 启动服务

```bash
# 后台运行
docker-compose up -d

# 或者前台运行（查看实时日志）
docker-compose up
```

### 4. 查看服务状态

```bash
# 查看运行状态
docker-compose ps

# 查看日志
docker-compose logs -f

# 只查看后端日志
docker-compose logs -f nofx

# 只查看前端日志
docker-compose logs -f nofx-frontend
```

### 5. 停止服务

```bash
# 停止服务
docker-compose down

# 停止并删除数据卷
docker-compose down -v
```

## 服务端口

- **后端 API**: `http://localhost:8080` (可通过环境变量 `NOFX_BACKEND_PORT` 修改)
- **前端界面**: `http://localhost:3000` (可通过环境变量 `NOFX_FRONTEND_PORT` 修改)

## 环境变量

可以在 `docker-compose.yml` 中或通过 `.env` 文件设置：

- `NOFX_BACKEND_PORT`: 后端端口（默认: 8080）
- `NOFX_FRONTEND_PORT`: 前端端口（默认: 3000）
- `NOFX_TIMEZONE`: 时区（默认: Asia/Shanghai）

## 数据持久化

以下目录/文件会被挂载到容器中，数据会持久化：

- `./config.json` - 配置文件
- `./config.db` - SQLite 数据库
- `./beta_codes.txt` - Beta 代码文件
- `./decision_logs/` - 决策日志目录
- `./prompts/` - 提示词模板目录

## 健康检查

容器包含健康检查功能：

- 后端健康检查: `http://localhost:8080/api/health`
- 前端健康检查: `http://localhost:3000/health`

## 常见问题

### 1. 端口被占用

如果端口被占用，可以修改 `docker-compose.yml` 中的端口映射，或设置环境变量：

```bash
NOFX_BACKEND_PORT=8081 NOFX_FRONTEND_PORT=3001 docker-compose up -d
```

### 2. 构建失败

如果构建失败，尝试：

```bash
# 清理旧的镜像和缓存
docker-compose down
docker system prune -a

# 重新构建（不使用缓存）
docker-compose build --no-cache
```

### 3. 权限问题

如果遇到权限问题：

```bash
# 确保脚本有执行权限
chmod +x docker-build.sh

# 确保配置文件可读
chmod 644 config.json
```

### 4. 查看容器内部

```bash
# 进入后端容器
docker-compose exec nofx sh

# 进入前端容器
docker-compose exec nofx-frontend sh
```

## 更新代码

如果修改了代码，需要重新构建：

### 方式一：使用更新脚本（推荐）

```bash
# 一键更新（停止、重建、重启）
./docker-update.sh
```

### 方式二：手动更新

```bash
# 停止服务
docker-compose down

# 重新构建（不使用缓存，确保包含最新代码）
docker-compose build --no-cache

# 启动服务
docker-compose up -d

# 查看日志确认更新成功
docker-compose logs -f nofx
```

**重要提示**：修改代码后必须重新构建镜像，否则容器中运行的仍然是旧代码！

## 日志位置

- 容器日志: `docker-compose logs`
- 决策日志: `./decision_logs/` 目录（挂载到容器中）

## 注意事项

1. **配置文件**: `config.json` 修改后需要重启容器才能生效
2. **数据库**: `config.db` 会持久化保存，删除容器不会丢失数据
3. **时区**: 默认使用 Asia/Shanghai，可通过环境变量修改
4. **网络**: 后端和前端在同一个 Docker 网络中，前端通过内部网络访问后端 API

