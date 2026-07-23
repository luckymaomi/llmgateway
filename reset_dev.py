"""Explicitly reset only LLMGateway-owned local development data."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import shutil
import subprocess
import sys


ROOT = Path(__file__).resolve().parent


def port(value: str) -> int:
    parsed = int(value)
    if not 1 <= parsed <= 65535:
        raise argparse.ArgumentTypeError("端口必须在 1 到 65535 之间")
    return parsed


def main() -> int:
    parser = argparse.ArgumentParser(description="清空 LLMGateway 本地开发数据并从零开始")
    parser.add_argument("--yes", action="store_true", help="跳过 RESET 输入确认，仅用于明确的自动化操作")
    parser.add_argument("--no-start", action="store_true", help="清空后不重新启动网页")
    parser.add_argument("--gateway-port", type=port, default=8080, help="Gateway 端口，默认 8080")
    parser.add_argument("--web-port", type=port, default=5173, help="管理网页端口，默认 5173")
    args = parser.parse_args()

    if os.name != "nt":
        print("这个友好入口目前只支持 Windows。", file=sys.stderr)
        return 2
    command = shutil.which("powershell.exe") or shutil.which("powershell")
    if command is None:
        print("未找到 Windows PowerShell。", file=sys.stderr)
        return 2

    print("警告：这会永久删除本项目本地 PostgreSQL/Valkey 中的所有开发数据。")
    print("将丢失管理员、成员、Provider、模型、上游 API Key、配置版本、API 密钥、额度和账本。")
    print("不会删除源码、.env、Key 文件或其他 Docker 项目。启动窗口仍在运行时会拒绝重置。")
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
            "-GatewayPort",
            str(args.gateway_port),
            "-WebPort",
            str(args.web_port),
        ],
        cwd=ROOT,
    )
    if status != 0:
        print("重置没有完成；请按上方错误处理，现有数据不会被本脚本继续清理。", file=sys.stderr)
        return status
    if args.no_start:
        print("本地数据已清空。下次运行 python .\\start_dev.py 会进入首位管理员初始化。")
        return 0

    print("\n本地数据已清空，正在重新启动。浏览器打开后请创建新的首位管理员。\n")
    start_command = [
        sys.executable,
        str(ROOT / "start_dev.py"),
        "--gateway-port",
        str(args.gateway_port),
        "--web-port",
        str(args.web_port),
    ]
    return subprocess.call(start_command, cwd=ROOT)


if __name__ == "__main__":
    raise SystemExit(main())
