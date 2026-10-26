#!/usr/bin/env python3
# gen_golden.py — 用 Python 版 ASC 在真实 APK 语料上生成 findrefs 差分基线。
# 用法:PYTHONPATH=<ASC仓库>/src/asc_core:<ASC仓库> python3 .../gen_golden.py
# 产物:golden_real.json(提交入库,Go 测试无需 Python 环境)。
# 说明:直接驱动 FindRefManager(不经过 AscHandler,避免 mutf8 依赖);
#       聚合行格式逐行复制自 asc_handler.py findrefs(aggregate=True)。

import copy
import glob
import json
import mmap
import os
import sys
from collections import defaultdict

from src.asc_client.apk_handler import _parse_cd_dex_entries, _inflate_dex
from src.asc_client.dex_container import iter_logical_dex_buffers
from src.asc_core.findrefs.findrefs_manager import FindRefManager
from src.asc_core.utils.tinydex import DEX

# 差分查询集:命中/脱靶、四类维度、精确/模糊类名全覆盖
QUERIES = {
    "q01_string_read": ("string", {"string": "read"}),
    "q02_string_value": ("string", {"string": "value"}),
    "q03_string_miss": ("string", {"string": "zzz_not_exist"}),
    "q04_string_desc": ("string", {"string": "Ljava/io/IOException;"}),
    "q05_string_short": ("string", {"string": "a"}),
    "q06_type_exact": ("type", {"type": "Ljava/io/InputStream;"}),
    "q07_type_fuzzy": ("type", {"type": "Ljava/io/"}),
    # 注:原版把查询串当正则编译,"[B" 会使 Python ASC 崩溃(已记录的偏离点,
    # Go 侧为字面量匹配,见 apkengine README);差分集不包含该类查询。
    "q09_type_miss": ("type", {"type": "Lno/such/Type;"}),
    "q10_method_name": ("method", {"method": {"class": None, "method": "read"}}),
    "q11_method_short": ("method", {"method": {"class": None, "method": "write"}}),
    "q12_method_miss": ("method", {"method": {"class": None, "method": "zzz"}}),
    "q13_field_name": ("field", {"field": {"class": None, "field": "value"}}),
    "q14_field_miss": ("field", {"field": {"class": None, "field": "zzz"}}),
}


def aggregate_lines(dex_name, dex, find_type, matched_idxs, mids):
    """逐行复制 asc_handler.py AscHandler.findrefs aggregate=True 分支。"""

    def format_matched_name(idx):
        if find_type == "string":
            return str(dex.strings[idx])
        if find_type == "type":
            return dex.types[idx].descriptor
        if find_type == "method":
            method = dex.methods[idx]
            return f"{method.cls.fullname}->{method.name}"
        field = dex.fields[idx]
        return f"{field.cls.fullname}->{field.name}"

    def format_method(midx):
        method = dex.methods[midx]
        return f"{method.cls.fullname}->{method.name}"

    grouped = defaultdict(set)
    for i in range(len(mids)):
        midx = mids[i]
        if midx is None:
            continue
        idx = matched_idxs[i]
        midxs = midx if isinstance(midx, list) else [midx]
        for mid in midxs:
            grouped[mid].add(idx)

    ret = []
    for mid in sorted(grouped):
        matched = "; ".join(format_matched_name(idx) for idx in sorted(grouped[mid]))
        ret.append(f"{dex_name} | {format_method(mid)} | matched=({matched})")
    return ret


def scan_apk(apk_path, find_type, find):
    fp = open(apk_path, "rb")
    mm = mmap.mmap(fp.fileno(), 0, access=mmap.ACCESS_READ)
    try:
        entries = _parse_cd_dex_entries(mm)
        entries.sort(key=lambda x: x[2])
        lines = []
        for entry in entries:
            data = _inflate_dex(mm, entry)
            for dex_name, dex_buf in iter_logical_dex_buffers(entry[0], data):
                dex = DEX.parse(memoryview(dex_buf), dex_name)
                manager = FindRefManager(dex)
                q = copy.deepcopy(find)
                matched_idxs = manager.find_ref(q, True)
                if matched_idxs is None:
                    matched_idxs = []
                mids = q[find_type]
                lines.extend(aggregate_lines(dex_name, dex, find_type, matched_idxs, mids))
        return lines
    finally:
        mm.close()
        fp.close()


def main():
    here = os.path.dirname(os.path.abspath(__file__))
    apk_paths = sorted(glob.glob(os.path.join(here, "*.apk")))
    apk_paths = [p for p in apk_paths if os.path.basename(p) != "golden.apk"]
    golden = {}
    for qid, (find_type, find) in QUERIES.items():
        all_lines = []
        for apk_path in apk_paths:
            for line in scan_apk(apk_path, find_type, find):
                all_lines.append(f"{os.path.basename(apk_path)}::{line}")
        golden[qid] = sorted(all_lines)

    out_path = os.path.join(here, "golden_real.json")
    with open(out_path, "w", encoding="utf-8") as fp:
        json.dump(golden, fp, ensure_ascii=False, indent=1, sort_keys=True)
    print(f"golden written: {out_path} ({len(apk_paths)} apks)")
    for qid, lines in golden.items():
        print(f"  {qid}: {len(lines)} lines")


if __name__ == "__main__":
    sys.exit(main())
