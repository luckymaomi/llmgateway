"""Stop every LLMGateway development process without deleting its data."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import shutil
import subprocess
import sys


ROOT = Path(__file__).resolve().parent


def main() -> int:
    parser = argparse.ArgumentParser(description="停止全部 LLMGateway 本地开发进程并保留数据")
    parser.parse_args()

    if os.name != "nt":
        print("这个友好入口目前只支持 Windows。", file=sys.stderr)
        return 2
    command = shutil.which("powershell.exe") or shutil.which("powershell")
    if command is None:
        print("未找到 Windows PowerShell。", file=sys.stderr)
        return 2
    print(
        "正在停止 LLMGateway 的 Gateway、管理网页、启动器、PostgreSQL 和 Valkey。"
        "命名数据卷会保留。",
        flush=True,
    )
    status = subprocess.call(
        [
            command,
            "-NoProfile",
            "-ExecutionPolicy",
            "Bypass",
            "-File",
            str(ROOT / "scripts" / "stop-dev.ps1"),
        ],
        cwd=ROOT,
    )
    if status == 0:
        print("已全部停止。下次运行 python .\\start_dev.py 会继续使用原有数据。")
    return status


if __name__ == "__main__":
    raise SystemExit(main())
