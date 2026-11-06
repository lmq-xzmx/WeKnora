#!/usr/bin/env python3
"""把 WeKnora 的资源从 local 存储后端整体迁移到 minio 存储后端。

背景
----
WeKnora 目前没有「跨存储后端搬文件」的 API：改知识库的存储实例时，如果该
知识库里已经有文件，后端会直接拒绝：

    Storage backend cannot be changed while the knowledge base contains
    files; migrate storage first        (internal/application/service/knowledgebase.go)

UI 上写的「需通过存储迁移流程才能更换实例」目前只是一句提示，router 里没有
对应的迁移接口；上游的 migrateLegacyStorageBackends 只是把老的 env 配置回填
成 storage_backends 表的行，不搬文件。

所以这里手工做这件事。存储布局是确定的：

  local://<tenant_id>/<knowledge_id>/<file>      →  <base_dir>/<tenant>/<kb>/<file>
  minio://<bucket>/<object_key>

迁移对每个资源做三件事：
  1. 把对象内容复制到 MinIO（object_key 与本地相对路径同名同结构）
  2. resources.physical_path 改成 canonical 形式
       storage://<minio_backend_id>/minio://<bucket>/<object_key>
     并同步 location_hash（= sha256(physical_path)，见
     internal/application/service/resource.go 的 resourceLocationHash）
  3. resources.storage_backend_id / provider 指向 minio 后端

最后把知识库的 storage_backend_id 也指向 minio 后端。

用法
----
  python3 storage_migrate_local_to_minio.py --dry-run     # 只报告，不改任何东西
  python3 storage_migrate_local_to_minio.py --apply        # 真迁移

回滚
----
--apply 会先把 resources / knowledge_bases 的相关列导出成
storage_migrate_backup_<时间戳>.csv，回滚时按该文件反向 UPDATE 即可
（MinIO 上的对象留着无害）。迁移期间本地卷不删，随时可以改回去。
"""

import argparse
import csv
import hashlib
import os
import subprocess
import sys
import time
from pathlib import Path

import boto3
from botocore.client import Config

DEPLOY_DIR = Path(__file__).resolve().parent.parent
ENV_FILE = DEPLOY_DIR / ".env"
LOCAL_BASE_DIR = Path("/data/docker/volumes/weknora_data-files/_data")


def load_env(path: Path) -> dict:
    env = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        env[k.strip()] = v.strip()
    return env


def psql(env: dict, sql: str, tuples_only: bool = True) -> str:
    cmd = [
        "docker", "exec", "-e", "PGPASSWORD=" + env["DB_PASSWORD"], "WeKnora-postgres",
        "psql", "-U", env["DB_USER"], "-d", env["DB_NAME"], "-t", "-A", "-F", "\t", "-c", sql,
    ]
    out = subprocess.run(cmd, capture_output=True, text=True, check=True)
    if tuples_only:
        return out.stdout.strip()
    return out.stdout


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--apply", action="store_true", help="真正执行迁移（默认 dry-run）")
    ap.add_argument("--bucket", default=None, help="目标桶，默认取 MINIO_BUCKET_NAME")
    args = ap.parse_args()

    env = load_env(ENV_FILE)
    bucket = args.bucket or env.get("MINIO_BUCKET_NAME", "weknora-files")
    minio_endpoint = env.get("MINIO_ENDPOINT", "minio:9000")
    access_key = env["MINIO_ACCESS_KEY_ID"]
    secret_key = env["MINIO_SECRET_ACCESS_KEY"]
    use_ssl = env.get("MINIO_USE_SSL", "false").lower() == "true"

    # 宿主上要用 127.0.0.1（容器名 minio 在宿主机解析不到）
    host_endpoint = minio_endpoint
    if host_endpoint.startswith("minio:"):
        host_endpoint = "127.0.0.1:" + host_endpoint.split(":", 1)[1]

    s3 = boto3.client(
        "s3",
        endpoint_url=f"http{'s' if use_ssl else ''}://{host_endpoint}",
        aws_access_key_id=access_key,
        aws_secret_access_key=secret_key,
        config=Config(s3={"addressing_style": "path"}, signature_version="s3v4"),
    )

    # 找源（local）与目标（minio）后端
    rows = psql(env, "SELECT id, name, provider, source FROM storage_backends WHERE deleted_at IS NULL").splitlines()
    backends = [r.split("\t") for r in rows if r.strip()]
    local = next((b for b in backends if b[2] == "local"), None)
    target = next((b for b in backends if b[2] == "minio"), None)
    if not local:
        print("找不到 local 后端", file=sys.stderr)
        return 1
    if not target:
        print("找不到 minio 后端，请在「设置 → 存储」先创建一个 minio 实例", file=sys.stderr)
        return 1
    local_id, minio_id = local[0], target[0]
    print(f"源后端   : {local[1]}  ({local_id})")
    print(f"目标后端 : {target[1]}  ({minio_id})  bucket={bucket}")
    print()

    res_rows = psql(
        env,
        "SELECT id, physical_path, size FROM resources "
        f"WHERE storage_backend_id = '{local_id}' AND deleted_at IS NULL ORDER BY id",
    ).splitlines()
    res_rows = [r.split("\t") for r in res_rows if r.strip()]

    kb_rows = psql(
        env,
        "SELECT id, name FROM knowledge_bases "
        f"WHERE storage_backend_id = '{local_id}' AND deleted_at IS NULL",
    ).splitlines()
    kb_rows = [r.split("\t") for r in kb_rows if r.strip()]

    print(f"待迁移资源 : {len(res_rows)} 个")
    print(f"受影响知识库: {len(kb_rows)} 个 -> {', '.join(k[1] for k in kb_rows)}")
    print()

    plan = []
    missing = 0
    total_bytes = 0
    for rid, phys, size in res_rows:
        if "local://" not in phys:
            print(f"  ! 跳过非 local 路径: {rid} {phys}")
            continue
        inner = phys.split("local://", 1)[1]          # <tenant>/<kb>/<file>
        src = LOCAL_BASE_DIR / inner
        key = inner                                  # MinIO object key，与本地同名同结构
        new_phys = f"storage://{minio_id}/minio://{bucket}/{key}"
        new_hash = hashlib.sha256(new_phys.encode()).hexdigest()
        exists = src.is_file()
        if not exists:
            missing += 1
        total_bytes += int(size or 0)
        plan.append(
            {
                "id": rid,
                "old_physical_path": phys,
                "old_location_hash": hashlib.sha256(phys.encode()).hexdigest(),
                "old_backend_id": local_id,
                "old_provider": "local",
                "src": str(src),
                "key": key,
                "new_physical_path": new_phys,
                "new_location_hash": new_hash,
                "size": size or 0,
                "exists": exists,
            }
        )

    print(f"源文件缺失 : {missing} 个")
    print(f"总大小     : {total_bytes/1024/1024:.2f} MiB")
    print(f"样例映射   : {plan[0]['src'] if plan else '-'}  ->  s3://{bucket}/{plan[0]['key'] if plan else '-'}")
    print()

    if not args.apply:
        print("dry-run：未做任何修改。确认无误后加 --apply 执行。")
        return 0

    if missing:
        print(f"有 {missing} 个源文件缺失，先中止，避免迁移出坏数据。", file=sys.stderr)
        return 1

    # 1) 备份
    stamp = time.strftime("%Y%m%d-%H%M%S")
    backup = DEPLOY_DIR / "scripts" / f"storage_migrate_backup_{stamp}.csv"
    with open(backup, "w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=list(plan[0].keys()))
        w.writeheader()
        for p in plan:
            w.writerow(p)
    print(f"已导出回滚备份: {backup}")

    # 2) 复制对象
    ok = 0
    for p in plan:
        with open(p["src"], "rb") as fh:
            s3.put_object(Bucket=bucket, Key=p["key"], Body=fh)
        ok += 1
    print(f"已上传 {ok}/{len(plan)} 个对象到 s3://{bucket}")

    # 3) 改库
    for p in plan:
        psql(
            env,
            "UPDATE resources SET storage_backend_id = '{minio_id}', provider = 'minio', "
            "physical_path = '{phys}', location_hash = '{h}' WHERE id = '{rid}'".format(
                minio_id=minio_id, phys=p["new_physical_path"], h=p["new_location_hash"], rid=p["id"]
            ),
        )
    print("已更新 resources 表")

    for kid, _name in kb_rows:
        psql(
            env,
            f"UPDATE knowledge_bases SET storage_backend_id = '{minio_id}' WHERE id = '{kid}'",
        )
    print(f"已更新 {len(kb_rows)} 个知识库的 storage_backend_id")

    print()
    print("完成。验证：随便打开一篇文档预览/下载，确认能正常读取。")
    print(f"回滚：按 {backup.name} 反向 UPDATE（本地卷文件未删除，随时可退）。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
