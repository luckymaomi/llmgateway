"""Beginner-friendly Windows entry point for the LLMGateway development stack."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import shutil
import subprocess
import sys


ROOT = Path(__file__).resolve().parent


def power_shell() -> str:
    command = shutil.which("powershell.exe") or shutil.which("powershell")
    if command is None:
        raise RuntimeError("未找到 Windows PowerShell。请确认正在 Windows 10/11 上运行。")
    return command


def run_script(script_name: str, *arguments: str) -> int:
    command = [
        power_shell(),
        "-NoProfile",
        "-ExecutionPolicy",
        "Bypass",
        "-File",
        str(ROOT / "scripts" / script_name),
        *arguments,
    ]
    return subprocess.call(command, cwd=ROOT)


def port(value: str) -> int:
    parsed = int(value)
    if not 1 <= parsed <= 65535:
        raise argparse.ArgumentTypeError("端口必须在 1 到 65535 之间")
    return parsed


def main() -> int:
    parser = argparse.ArgumentParser(description="检查环境并启动 LLMGateway 管理网页")
    parser.add_argument("--check", action="store_true", help="只检查环境，不启动服务")
    parser.add_argument("--no-browser", action="store_true", help="启动后不自动打开浏览器")
    parser.add_argument("--gateway-port", type=port, default=8080, help="Gateway 端口，默认 8080")
    parser.add_argument("--web-port", type=port, default=5173, help="管理网页端口，默认 5173")
    args = parser.parse_args()

    if os.name != "nt":
        print("这个友好入口目前只支持 Windows。正式 Linux 部署请查看 deploy/README.md。", file=sys.stderr)
        return 2

    print("LLMGateway 环境检查")
    print("需要：Git、Docker Desktop、Go 1.26.5+、Node.js 22.12+、pnpm 10.33.0。")
    print("正常启动会保留已有账号、Provider、配置、Key 和账本，不会调用真实 Provider。\n")
    try:
        status = run_script("verify-environment.ps1", "-SkipServices")
    except RuntimeError as error:
        print(f"环境检查无法开始：{error}", file=sys.stderr)
        return 2
    if status != 0:
        print("\n环境检查未通过。按上方提示安装或启动缺少的软件后重试。", file=sys.stderr)
        return status
    if args.check:
        print("\n环境已经准备好。运行 python .\\start_dev.py 即可启动并打开网页。")
        return 0

    print("\n正在启动 PostgreSQL、Valkey、Gateway 和管理网页。首次构建可能需要几分钟。")
    print("启动完成后请保持本窗口运行；结束时按 Ctrl+C，数据会继续保留。\n")
    arguments = ["-GatewayPort", str(args.gateway_port), "-WebPort", str(args.web_port)]
    if not args.no_browser:
        arguments.append("-OpenBrowser")
    try:
        return run_script("dev.ps1", *arguments)
    except KeyboardInterrupt:
        print("\n已请求停止。Gateway 和网页会退出，PostgreSQL/Valkey 数据继续保留。")
        return 130


if __name__ == "__main__":
    raise SystemExit(main())
