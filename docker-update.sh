#!/bin/bash

# NOFX Docker 更新脚本 - 重新构建并重启容器

set -e

echo "🔄 开始更新 NOFX Docker 容器..."

# 检查 Docker 是否安装
if ! command -v docker &> /dev/null; then
    echo "❌ Docker 未安装，请先安装 Docker"
    exit 1
fi

# 检查 Docker Compose 是否安装
if ! command -v docker-compose &> /dev/null && ! docker compose version &> /dev/null; then
    echo "❌ Docker Compose 未安装，请先安装 Docker Compose"
    exit 1
fi

# 进入项目目录
cd "$(dirname "$0")"

# 停止现有容器
echo "⏹️  停止现有容器..."
docker-compose down

# 清理旧的镜像（可选，加快构建速度）
echo "🧹 清理旧的构建缓存..."
docker-compose build --no-cache --pull

# 重新构建镜像
echo "📦 重新构建 Docker 镜像（包含最新代码）..."
docker-compose build --no-cache

# 启动容器
echo "🚀 启动容器..."
docker-compose up -d

# 等待服务启动
echo "⏳ 等待服务启动..."
sleep 5

# 显示容器状态
echo ""
echo "✅ 更新完成！"
echo ""
echo "📊 容器状态："
docker-compose ps

echo ""
echo "📝 查看日志："
echo "   docker-compose logs -f nofx"
echo ""
echo "📝 查看后端日志："
echo "   docker-compose logs -f nofx"
echo ""
echo "📝 停止服务："
echo "   docker-compose down"

