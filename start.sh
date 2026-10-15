#!/bin/bash
# 小炬人 启动脚本 - 包含 SSRF 白名单配置

export SSRF_WHITELIST_EXTRA=36.140.179.34
export DB_DRIVER=sqlite
export DB_PATH=/data/小炬人/data/weknora.db
export DOCREADER_ADDR=localhost:50051
export DOCREADER_TRANSPORT=grpc

cd /data/小炬人
./小炬人
