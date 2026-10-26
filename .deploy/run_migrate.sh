#!/bin/bash
set -uo pipefail
TARGET_DB="${TARGET_DB:-findu_p4_test}"
PW=$(docker exec findu-postgres printenv POSTGRES_PASSWORD)
if [ -z "$PW" ]; then echo "!! 取不到 PG 密码"; exit 1; fi
docker run --rm --network findu_default \
  -v /mnt/e/渗透agent/findu/.deploy:/dep:ro \
  -v /opt/findu/data:/src:ro \
  -e PG_DSN="postgres://findu:${PW}@postgres:5432/${TARGET_DB}?sslmode=disable" \
  -e SRC_DB=/src/conversations.db \
  python:3.12-slim bash -c \
  "pip install --quiet --index-url https://mirrors.aliyun.com/pypi/simple/ psycopg2-binary >/dev/null 2>&1 && echo PIP_OK && python -u /dep/migrate_sqlite_to_pg.py"
