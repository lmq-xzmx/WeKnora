#!/bin/bash
# 小炬人 启动脚本 - 包含 SSRF 白名单配置

export SSRF_WHITELIST_EXTRA=36.140.179.34
# WeKnora 已全面退场 sqlite，只跑 PostgreSQL(pgvector)。
# 这两项由部署方的 .env 提供，这里不再硬编码（原来写死的 sqlite 路径
# 指向一个已经不存在的驱动，启动会直接失败）。
export DB_DRIVER=postgres
export DOCREADER_ADDR=localhost:50051
export DOCREADER_TRANSPORT=grpc

cd /data/小炬人
./小炬人
