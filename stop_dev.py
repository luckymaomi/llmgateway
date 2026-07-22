"""Stop LLMGateway development infrastructure without deleting its data."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys


ROOT = Path(__file__).resolve().parent


def port(value: str) -> int:
    parsed = int(value)
    if not 1 <= parsed <= 65535:
        raise argparse.ArgumentTypeError("端口必须在 1 到 65535 之间")
    return parsed


def is_listening(port_number: int) -> bool:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as connection:
        connection.settimeout(0.25)
        return connection.connect_ex(("127.0.0.1", port_number)) == 0


def main() -> int:
    parser = argparse.ArgumentParser(description="停止本地基础设施并保留 LLMGateway 数据")
    parser.add_argument("--gateway-port", type=port, default=8080, help="Gateway 端口，默认 8080")
    parser.add_argument("--web-port", type=port, default=5173, help="管理网页端口，默认 5173")
    args = parser.parse_args()

    if os.name != "nt":
        print("这个友好入口目前只支持 Windows。", file=sys.stderr)
        return 2
    active = [number for number in (args.gateway_port, args.web_port) if is_listening(number)]
    if active:
        joined = "、".join(str(number) for number in active)
        print(
            f"检测到开发端口 {joined} 仍在使用。请先回到 start_dev.py 窗口按 Ctrl+C，"
            "确认网页和 Gateway 已停止后再运行本命令。",
            file=sys.stderr,
        )
        return 2

    command = shutil.which("powershell.exe") or shutil.which("powershell")
    if command is None:
        print("未找到 Windows PowerShell。", file=sys.stderr)
        return 2
    print("正在停止 LLMGateway 的 PostgreSQL 和 Valkey 容器。命名数据卷会保留。")
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
        print("已停止。下次运行 python .\\start_dev.py 会继续使用原有数据。")
    return status


if __name__ == "__main__":
    raise SystemExit(main())
