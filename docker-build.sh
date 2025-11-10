#!/bin/bash

# NOFX Docker 构建和运行脚本

set -e

echo "🚀 开始构建 NOFX Docker 镜像..."

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

# 检查配置文件
if [ ! -f "config.json" ]; then
    echo "⚠️  配置文件 config.json 不存在，从 config.json.example 复制..."
    if [ -f "config.json.example" ]; then
        cp config.json.example config.json
        echo "✅ 已创建 config.json，请根据需要修改配置"
    else
        echo "❌ config.json.example 不存在"
        exit 1
    fi
fi

# 构建镜像
echo "📦 构建 Docker 镜像..."
docker-compose build --no-cache

echo "✅ 构建完成！"
echo ""
echo "📝 使用以下命令运行："
echo "   docker-compose up -d"
echo ""
echo "📝 查看日志："
echo "   docker-compose logs -f"
echo ""
echo "📝 停止服务："
echo "   docker-compose down"

