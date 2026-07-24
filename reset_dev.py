"""Explicitly reset only LLMGateway-owned local development data."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import shutil
import subprocess
import sys


ROOT = Path(__file__).resolve().parent


def main() -> int:
    parser = argparse.ArgumentParser(description="停止 LLMGateway 并清空全部本地开发数据")
    parser.add_argument("--yes", action="store_true", help="跳过 RESET 输入确认，仅用于明确的自动化操作")
    args = parser.parse_args()

    if os.name != "nt":
        print("这个友好入口目前只支持 Windows。", file=sys.stderr)
        return 2
    command = shutil.which("powershell.exe") or shutil.which("powershell")
    if command is None:
        print("未找到 Windows PowerShell。", file=sys.stderr)
        return 2

    print("警告：这会先停止 LLMGateway，再永久删除本项目 PostgreSQL/Valkey 中的全部本地开发数据。")
    print("将丢失管理员、成员、资源池、套餐、订阅、上游 API Key、API 密钥、额度和账本。")
    print("不会删除源码、.env、Key 文件或其他 Docker 项目；完成后不会自动启动。")
    if not args.yes:
        try:
            confirmation = input("确认从零开始请输入 RESET：").strip()
        except EOFError:
            confirmation = ""
        if confirmation != "RESET":
            print("输入不匹配，未做任何更改。")
            return 1

    status = subprocess.call(
        [
            command,
            "-NoProfile",
            "-ExecutionPolicy",
            "Bypass",
            "-File",
            str(ROOT / "scripts" / "reset-dev.ps1"),
            "-ConfirmDataLoss",
        ],
        cwd=ROOT,
    )
    if status != 0:
        print("重置没有完成；请按上方错误处理，现有数据不会被本脚本继续清理。", file=sys.stderr)
        return status
    print("本地数据已清空，所有服务保持停止。运行 python .\\start_dev.py 可从首位管理员初始化开始。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
