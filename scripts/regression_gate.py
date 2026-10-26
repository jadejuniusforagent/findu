#!/usr/bin/env python3
# 零回归闸门：build -> vet -> layercheck -> 全仓测试 -> 与已知红灯白名单比对
# 用法: python3 scripts/regression_gate.py     SKIP_TEST=1 只做静态检查
# 铁律：测试库必须是 findu_test（禁止 findu 生产库，否则出现成批假失败）
import os
import pathlib
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
TEST_DB = os.environ.get("TEST_DB", "findu_test")
# 已知红灯白名单：目前为空。db 的 TestCompanyScopeAttribution 已改为
# 每轮唯一 CIDR + 自清理（见 db/company_scope_test.go），不再长期挂红。
KNOWN = os.environ.get("KNOWN_FAIL_PKGS", "").split()
# 偶发包：全量并行下因测试库争用而随机失败一个用例，单跑必过。
# 这类包在闸门里单跑重试一次，通过则不计失败；重试仍失败才判回归。
RETRY = os.environ.get("RETRY_PKGS", "finduai/agent").split()

if TEST_DB == "findu":
    sys.exit("拒绝：TEST_DB 指向生产库 findu")

fail = 0

def run(title, argv):
    global fail
    print("==", title)
    r = subprocess.run(argv, cwd=str(ROOT), capture_output=True, text=True)
    if r.returncode != 0:
        print((r.stdout[-3000:] + r.stderr[-3000:]))
        fail = 1
    return r

run("go build", ["go", "build", "./..."])
run("go vet", ["go", "vet", "./..."])
run("layercheck", ["go", "run", "./cmd/layercheck"])

if os.environ.get("SKIP_TEST") != "1":
    ip = subprocess.run(["docker", "exec", "findu-postgres", "hostname", "-i"],
                        capture_output=True, text=True).stdout.split()[0]
    envtxt = (ROOT / ".env").read_text(encoding="utf-8")
    def pick(key, default=""):
        for line in envtxt.splitlines():
            if line.startswith(key + "="):
                return line.split("=", 1)[1].strip()
        return default
    dsn = "postgres://%s:%s@%s:5432/%s?sslmode=disable" % (
        pick("POSTGRES_USER", "findu"), pick("POSTGRES_PASSWORD"), ip, TEST_DB)
    env = dict(os.environ, FINDU_PG_DSN=dsn)
    # 包级并行度默认 3：db.Open 每实例保留 maxOpenConns=32 个空闲连接，测试库
    # PG max_connections=100。全并行（16 核 → -p 16）时多包同时持池会打满连接，
    # 表现为 server 测试批量 FATAL: sorry, too many clients already（非代码回归）。
    par = os.environ.get("GATE_PACKAGE_PARALLEL", "3")
    tpar = os.environ.get("GATE_TEST_PARALLEL", "2")
    print("== go test (db=%s, -p %s, -parallel %s)" % (TEST_DB, par, tpar))
    r = subprocess.run(["go", "test", "./...", "-count=1", "-p", par, "-parallel", tpar], cwd=str(ROOT),
                       capture_output=True, text=True, env=env)
    lines = (r.stdout + r.stderr).splitlines()
    got = sorted({l.split()[1] for l in lines if l.startswith("FAIL") and len(l.split()) > 1})
    ok = sum(1 for l in lines if l.startswith("ok "))
    print("通过包数:", ok)
    print("失败包:", got)
    extra = [p for p in got if p not in KNOWN]
    if extra:
        still = []
        for pkg in extra:
            if pkg in RETRY:
                print("偶发包重试:", pkg)
                r2 = subprocess.run(["go", "test", pkg, "-count=1"], cwd=str(ROOT),
                                    capture_output=True, text=True, env=env)
                if r2.returncode == 0:
                    print("  单跑通过，判为并行争用偶发（不计失败）")
                    continue
                print("  单跑仍失败，判为真实回归")
            still.append(pkg)
        if still:
            print("回归：新增失败包", still)
            fails = [l for l in lines if l.startswith("--- FAIL")]
            print(chr(10).join(fails)[:3000])
            fail = 1

print("闸门通过" if fail == 0 else "闸门未通过")
sys.exit(fail)
