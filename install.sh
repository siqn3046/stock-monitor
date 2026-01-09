#!/usr/bin/env bash
set -euo pipefail

REPO_URL="${1:-}"
if [[ -z "$REPO_URL" ]]; then
  echo "Usage: bash install.sh <github_repo_https_url> [install_dir]"
  echo "Example: bash install.sh https://github.com/YOURNAME/stock-monitor.git /opt/stock-monitor"
  exit 1
fi

INSTALL_DIR="${2:-/opt/stock-monitor}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "Missing: $1"; exit 1; }; }
need git

# docker / docker compose
if ! command -v docker >/dev/null 2>&1; then
  echo "Docker not found. Please install Docker first."
  exit 1
fi
if ! docker compose version >/dev/null 2>&1; then
  echo "docker compose not found. Please install Docker Compose v2."
  exit 1
fi

echo "[1/5] Clone/Update repo -> $INSTALL_DIR"
if [[ -d "$INSTALL_DIR/.git" ]]; then
  git -C "$INSTALL_DIR" pull
else
  git clone "$REPO_URL" "$INSTALL_DIR"
fi

cd "$INSTALL_DIR"

echo "[2/5] Prepare .env"
if [[ ! -f .env ]]; then
  cp .env.example .env
fi

# 自动生成 COOKIE_SECRET（若空）
if ! grep -q '^COOKIE_SECRET=.*[^[:space:]]' .env; then
  SECRET="$(python3 - <<'PY'
import secrets
print(secrets.token_urlsafe(48))
PY
)"
  # mac/linux 兼容写法
  if grep -q '^COOKIE_SECRET=' .env; then
    sed -i.bak "s|^COOKIE_SECRET=.*|COOKIE_SECRET=${SECRET}|g" .env && rm -f .env.bak
  else
    echo "COOKIE_SECRET=${SECRET}" >> .env
  fi
fi

echo "[3/5] Pull/build images"
docker compose pull || true
docker compose build

echo "[4/5] Start services"
docker compose up -d

echo "[5/5] Done."
echo "➡️  Now open: https://$(grep '^WEB_DOMAIN=' .env | cut -d= -f2)"
echo "   (Make sure DNS A record points to this server, and ports 80/443 are open.)"
