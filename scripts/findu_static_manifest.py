#!/usr/bin/env python3
"""web-findu 静态资源清单。

迁移期 /findu/ 的前端仍然直接读 /app/web-findu。以前用 docker cp 手改容器里的
文件，容器 recreate 后会静默回退到镜像里的旧版本。这个脚本把目录内容固化成
可提交的基线：文件 sha256 + 总字节数 + 目录树哈希，构建/部署时用 --verify 比对。

用法：
  python3 scripts/findu_static_manifest.py --write
  python3 scripts/findu_static_manifest.py --verify
  python3 scripts/findu_static_manifest.py --root /app/web-findu \
      --baseline /app/docs/refactor/findu-static-baseline.json --verify

退出码：0 = 一致；1 = 漂移或参数/IO 错误。
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
from pathlib import Path

SCHEMA = 1
DEFAULT_ROOT = "web-findu"
DEFAULT_BASELINE = "docs/refactor/findu-static-baseline.json"


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def tree_hash(files: list[dict]) -> str:
    """按相对路径排序后哈希 path\\0sha256\\n，与平台/时间无关。"""
    h = hashlib.sha256()
    for item in sorted(files, key=lambda x: x["path"]):
        h.update(item["path"].encode("utf-8"))
        h.update(b"\0")
        h.update(item["sha256"].encode("ascii"))
        h.update(b"\n")
    return h.hexdigest()


def build_manifest(root: Path, version: str) -> dict:
    if not root.is_dir():
        raise SystemExit(f"web-findu 目录不存在: {root}")
    files: list[dict] = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames.sort()
        for name in sorted(filenames):
            full = Path(dirpath) / name
            if not full.is_file():
                continue
            rel = full.relative_to(root).as_posix()
            files.append(
                {
                    "path": rel,
                    "bytes": full.stat().st_size,
                    "sha256": sha256_file(full),
                }
            )
    files.sort(key=lambda x: x["path"])
    return {
        "schema": SCHEMA,
        "build_version": version,
        "file_count": len(files),
        "total_bytes": sum(x["bytes"] for x in files),
        "tree_sha256": tree_hash(files),
        "files": files,
    }


def load_json(path: Path) -> dict:
    try:
        with path.open("r", encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError:
        raise SystemExit(f"基线不存在: {path}")
    except json.JSONDecodeError as exc:
        raise SystemExit(f"基线不是合法 JSON: {path}: {exc}")


def compare(current: dict, baseline: dict) -> list[str]:
    problems: list[str] = []
    if baseline.get("schema") != SCHEMA:
        problems.append(
            f"schema 不一致: 基线={baseline.get('schema')} 当前={SCHEMA}"
        )
    base_files = {x["path"]: x for x in baseline.get("files", [])}
    cur_files = {x["path"]: x for x in current["files"]}

    removed = sorted(set(base_files) - set(cur_files))
    added = sorted(set(cur_files) - set(base_files))
    changed = sorted(
        p
        for p in set(base_files) & set(cur_files)
        if base_files[p]["sha256"] != cur_files[p]["sha256"]
        or base_files[p]["bytes"] != cur_files[p]["bytes"]
    )
    if removed:
        problems.append("缺失文件: " + ", ".join(removed))
    if added:
        problems.append("新增文件: " + ", ".join(added))
    if changed:
        problems.append("内容变化: " + ", ".join(changed))
    if baseline.get("file_count") != current["file_count"]:
        problems.append(
            f"文件数不一致: 基线={baseline.get('file_count')} 当前={current['file_count']}"
        )
    if baseline.get("total_bytes") != current["total_bytes"]:
        problems.append(
            f"总字节不一致: 基线={baseline.get('total_bytes')} 当前={current['total_bytes']}"
        )
    if baseline.get("tree_sha256") != current["tree_sha256"]:
        problems.append(
            f"目录树哈希不一致: 基线={baseline.get('tree_sha256')} 当前={current['tree_sha256']}"
        )
    return problems


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="web-findu 静态资源清单")
    parser.add_argument("--root", default=DEFAULT_ROOT, help="web-findu 目录")
    parser.add_argument("--baseline", default=DEFAULT_BASELINE, help="基线 JSON 路径")
    parser.add_argument("--version", default="unknown", help="构建版本（仅记录）")
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--write", action="store_true", help="写入基线")
    group.add_argument("--verify", action="store_true", help="比对基线")
    group.add_argument("--print", dest="print_only", action="store_true", help="打印当前清单")
    args = parser.parse_args(argv)

    root = Path(args.root)
    baseline_path = Path(args.baseline)
    current = build_manifest(root, args.version)

    if args.print_only:
        print(json.dumps(current, ensure_ascii=False, indent=2))
        return 0

    if args.write:
        baseline_path.parent.mkdir(parents=True, exist_ok=True)
        with baseline_path.open("w", encoding="utf-8", newline="\n") as f:
            json.dump(current, f, ensure_ascii=False, indent=2)
            f.write("\n")
        print(
            f"[manifest] wrote {baseline_path}: "
            f"{current['file_count']} files, {current['total_bytes']} bytes, "
            f"tree={current['tree_sha256']}"
        )
        return 0

    baseline = load_json(baseline_path)
    problems = compare(current, baseline)
    if problems:
        print(f"[manifest] FAIL {root} != {baseline_path}", file=sys.stderr)
        for p in problems:
            print("  - " + p, file=sys.stderr)
        return 1
    print(
        f"[manifest] OK {root}: {current['file_count']} files, "
        f"{current['total_bytes']} bytes, tree={current['tree_sha256']}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
