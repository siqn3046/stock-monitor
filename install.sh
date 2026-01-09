#!/usr/bin/env bash
set -e

# 颜色定义
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

INSTALL_DIR="/opt/stock-monitor"
REPO_URL="https://github.com/siqn3046/stock-monitor.git"
# nginx.tmpl 的官方下载地址
TMPL_URL="https://raw.githubusercontent.com/nginx-proxy/nginx-proxy/main/nginx.tmpl"

echo -e "${GREEN}============================================${NC}"
echo -e "${GREEN}    Stock Monitor 一键安装脚本 (Pro版)       ${NC}"
echo -e "${GREEN}============================================${NC}"

# 1. 检查 root
if [[ $EUID -ne 0 ]]; then
   echo -e "${RED}Error: 请使用 root 权限运行 (sudo -i)${NC}"
   exit 1
fi

# 2. 检查 Docker
echo -e "${YELLOW}[1/6] 检查 Docker 环境...${NC}"
if ! command -v docker &> /dev/null; then
    echo "安装 Docker..."
    curl -fsSL https://get.docker.com | bash -s docker
    systemctl enable --now docker
else
    echo "Docker 已安装。"
fi

# 3. 拉取代码
echo -e "${YELLOW}[2/6] 拉取项目代码 -> $INSTALL_DIR${NC}"
if [[ -d "$INSTALL_DIR/.git" ]]; then
    cd "$INSTALL_DIR"
    git pull
else
    git clone "$REPO_URL" "$INSTALL_DIR"
    cd "$INSTALL_DIR"
fi

# 4. [关键步骤] 下载 nginx.tmpl 模板
echo -e "${YELLOW}[3/6] 准备 Docker-Gen 模板文件...${NC}"
mkdir -p docker-gen/templates
if [[ ! -f docker-gen/templates/nginx.tmpl ]]; then
    echo "正在下载 nginx.tmpl..."
    curl -sSL "$TMPL_URL" -o docker-gen/templates/nginx.tmpl
fi

# 5. 交互式配置 .env
echo -e "${YELLOW}[4/6] 配置环境变量...${NC}"
if [[ ! -f .env ]]; then cp .env.example .env; fi

read -p "请输入域名 (如 stock.example.com): " INPUT_DOMAIN
if [[ -z "$INPUT_DOMAIN" ]]; then echo -e "${RED}域名必填!${NC}"; exit 1; fi

read -p "请输入邮箱 (用于 SSL 证书): " INPUT_EMAIL
if [[ -z "$INPUT_EMAIL" ]]; then echo -e "${RED}邮箱必填!${NC}"; exit 1; fi

read -p "设置管理员密码 (留空随机): " INPUT_PASS
if [[ -z "$INPUT_PASS" ]]; then
    INPUT_PASS=$(openssl rand -base64 12)
    echo "随机密码: $INPUT_PASS"
fi

SECRET_KEY=$(openssl rand -hex 32)

# 写入 .env
sed -i "s|^WEB_DOMAIN=.*|WEB_DOMAIN=${INPUT_DOMAIN}|g" .env
sed -i "s|^LETSENCRYPT_EMAIL=.*|LETSENCRYPT_EMAIL=${INPUT_EMAIL}|g" .env
sed -i "s|^ADMIN_PASS=.*|ADMIN_PASS=${INPUT_PASS}|g" .env
sed -i "s|^COOKIE_SECRET=.*|COOKIE_SECRET=${SECRET_KEY}|g" .env

# 确保其他参数存在
grep -q "CHECK_INTERVAL_SECONDS" .env || echo "CHECK_INTERVAL_SECONDS=300" >> .env
grep -q "ALWAYS_NOTIFY" .env || echo "ALWAYS_NOTIFY=false" >> .env
grep -q "ALLOW_SIGNUP" .env || echo "ALLOW_SIGNUP=false" >> .env

# 6. 启动
echo -e "${YELLOW}[5/6] 构建并启动服务...${NC}"
docker compose pull
docker compose build
docker compose down --remove-orphans || true
docker compose up -d

echo -e "${GREEN}============================================${NC}"
echo -e "${GREEN}   安装完成! 访问: https://${INPUT_DOMAIN}   ${NC}"
echo -e "${GREEN}============================================${NC}"
