#!/bin/bash

# 脚本：清理并重新构建 Docker 容器和镜像

set -e

echo "========================================"
echo "开始清理旧的容器和镜像..."
echo "========================================"

# 清空 decision_logs 目录
echo "Cleaning decision_logs directory..."
rm -rf decision_logs/*

# 停止并删除容器
echo "停止容器..."
docker-compose down || true

# 删除指定容器（如果还存在）
echo "删除容器..."
docker rm -f nofx-trading nofx-frontend 2>/dev/null || true

# 删除镜像
echo "删除镜像..."
docker rmi -f mynof-nofx mynof-nofx-frontend 2>/dev/null || true

# 清理悬空镜像
echo "清理悬空镜像..."
docker image prune -f

echo ""
echo "========================================"
echo "开始重新构建和启动..."
echo "========================================"

# 重新构建并启动
docker-compose up -d --build

echo ""
echo "========================================"
echo "查看容器状态..."
echo "========================================"
docker-compose ps

echo ""
echo "========================================"
echo "完成！"
echo "========================================"
echo "使用 'docker-compose logs -f' 查看日志"
EOF