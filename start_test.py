r"""LLMGateway 唯一人工测试入口。

用法（全部命令都会实时显示输出，并写入 `.build/test-logs/`）：

    python .\start_test.py
        显示交互菜单，输入编号选择下面任一测试。

    python .\start_test.py daily
        日常确定性检查：格式、静态分析、全部 Go 测试、sqlc 漂移、前端测试/构建。

    python .\start_test.py full
        完整本机验收：daily 加有头浏览器、Docker 集成、强杀恢复、Windows SCM、
        TLS 滚动升级、加密灾备和跨平台构建。通常需要 15 分钟左右。

    python .\start_test.py provider
        使用进程内 key.txt 跑真实 Provider、标准 Go/Python SDK 和切换合同；不打印凭据。

    python .\start_test.py capacity
        跑 300 名受控用户、60 名活跃用户、双 Gateway、长流、突发和强杀恢复。
        默认稳态 60 秒；正式 15 分钟证据使用 `--capacity-duration-seconds 900`。

    python .\start_test.py release
        构建并验证测试签名发布物、重复构建、OCI 镜像、SBOM、checksum 和 provenance。

    python .\start_test.py everything
        依次运行 full、provider、capacity、release；首个失败立即停止。
        full 已包含 daily，因此不会重复跑 daily。

长测试由 owner 在自己的终端运行。完成后只需把命令最后打印的日志路径告诉 Agent；
日志不受 Git 跟踪。Agent 仅自行运行预计一分钟以内的定向测试。
"""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
from typing import BinaryIO, Sequence


ROOT = Path(__file__).resolve().parent
LOG_DIRECTORY = ROOT / ".build" / "test-logs"
MODES = ("daily", "full", "provider", "capacity", "release", "everything")
MODE_MENU = (
    ("daily", "日常确定性检查", "约 2 分钟"),
    ("full", "完整本机验收", "约 15 分钟"),
    ("provider", "真实 Provider 与标准 SDK", "取决于外部网络"),
    ("capacity", "300 用户容量与强杀恢复", "默认约 2 分钟"),
    ("release", "测试签名发布物", "约 10～20 分钟"),
    ("everything", "以上全部生产门槛", "约 30～60 分钟"),
)


def powershell_command() -> str | None:
    if os.name != "nt":
        return shutil.which("pwsh")
    return shutil.which("powershell.exe") or shutil.which("powershell")


def select_mode() -> str:
    if not sys.stdin.isatty():
        raise ValueError("非交互环境必须显式指定测试档位，例如：python start_test.py daily")
    print("请选择要运行的 LLMGateway 测试：")
    for index, (mode, description, duration) in enumerate(MODE_MENU, start=1):
        print(f"  {index}. {mode:<10} {description}（{duration}）")
    print("  0. 退出，不运行测试")
    while True:
        try:
            choice = input("请输入编号 [0-6]：").strip()
        except (EOFError, KeyboardInterrupt):
            print("\n已取消，没有启动测试。")
            raise SystemExit(0) from None
        if choice == "0":
            print("已退出，没有启动测试。")
            raise SystemExit(0)
        if choice.isdigit() and 1 <= int(choice) <= len(MODE_MENU):
            return MODE_MENU[int(choice) - 1][0]
        print("请输入 0 到 6 之间的编号。")


def release_revision() -> str:
    status = subprocess.run(
        ["git", "status", "--porcelain", "--untracked-files=all"],
        cwd=ROOT,
        check=True,
        stdout=subprocess.PIPE,
    ).stdout
    if status:
        return "working-tree"
    return subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=ROOT,
        check=True,
        stdout=subprocess.PIPE,
        text=True,
        encoding="ascii",
    ).stdout.strip()


def test_commands(mode: str, powershell: str, capacity_duration_seconds: int, run_id: str) -> list[tuple[str, list[str]]]:
    prefix = [powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File"]
    commands: dict[str, list[tuple[str, list[str]]]] = {
        "daily": [
            (
                "daily",
                prefix
                + [
                    str(ROOT / "scripts" / "verify.ps1"),
                    "-SkipIntegration",
                    "-SkipBrowser",
                    "-SkipBuildMatrix",
                ],
            )
        ],
        "full": [("full", prefix + [str(ROOT / "scripts" / "verify.ps1")])],
        "provider": [("provider", prefix + [str(ROOT / "scripts" / "test-provider-real.ps1")])],
        "capacity": [
            (
                "capacity",
                prefix
                + [
                    str(ROOT / "scripts" / "test-capacity.ps1"),
                    "-DurationSeconds",
                    str(capacity_duration_seconds),
                ],
            )
        ],
    }

    if mode in ("release", "everything"):
        version = f"0.1.0-acceptance.{run_id}"
        release_directory = ROOT / ".build" / f"release-{version}"
        revision = release_revision()
        commands["release"] = [
            (
                "release-build",
                prefix
                + [
                    str(ROOT / "scripts" / "build-release.ps1"),
                    "-Version",
                    version,
                    "-OutputDirectory",
                    str(release_directory),
                    "-SigningMode",
                    "Test",
                ],
            ),
            (
                "release-verify",
                prefix
                + [
                    str(ROOT / "scripts" / "verify-release.ps1"),
                    "-Directory",
                    str(release_directory),
                    "-ExpectedVersion",
                    version,
                    "-ExpectedRevision",
                    revision,
                ],
            ),
            (
                "release-supply-chain",
                prefix
                + [
                    str(ROOT / "scripts" / "test-supply-chain.ps1"),
                    "-ReleaseDirectory",
                    str(release_directory),
                    "-SkipImage",
                ],
            ),
        ]
    if mode == "everything":
        return commands["full"] + commands["provider"] + commands["capacity"] + commands["release"]
    return commands[mode]


def write_line(log: BinaryIO, message: str, *, stderr: bool = False) -> None:
    data = (message + "\n").encode("utf-8")
    log.write(data)
    log.flush()
    stream = sys.stderr.buffer if stderr else sys.stdout.buffer
    stream.write(data)
    stream.flush()


def run_logged(command: Sequence[str], log: BinaryIO) -> int:
    creation_flags = subprocess.CREATE_NEW_PROCESS_GROUP if os.name == "nt" else 0
    process = subprocess.Popen(
        command,
        cwd=ROOT,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        creationflags=creation_flags,
    )
    assert process.stdout is not None
    try:
        while chunk := process.stdout.read1(64 * 1024):
            log.write(chunk)
            log.flush()
            sys.stdout.buffer.write(chunk)
            sys.stdout.buffer.flush()
        return process.wait()
    except KeyboardInterrupt:
        write_line(log, "\n收到中断，正在请求测试脚本执行自己的清理逻辑……", stderr=True)
        if os.name == "nt":
            process.send_signal(signal.CTRL_BREAK_EVENT)
        else:
            process.send_signal(signal.SIGINT)
        try:
            return process.wait(timeout=30)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
            return 130


def main() -> int:
    parser = argparse.ArgumentParser(
        description="运行 LLMGateway 统一测试并实时写日志",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    parser.add_argument("mode", nargs="?", choices=MODES, help="测试档位；省略时显示交互菜单")
    parser.add_argument(
        "--capacity-duration-seconds",
        type=int,
        default=60,
        metavar="SECONDS",
        help="capacity/everything 的稳态时长，10-43200 秒；正式 15 分钟证据用 900",
    )
    args = parser.parse_args()
    if not 10 <= args.capacity_duration_seconds <= 43_200:
        parser.error("--capacity-duration-seconds 必须在 10 到 43200 之间")

    try:
        mode = args.mode or select_mode()
    except ValueError as error:
        parser.error(str(error))

    powershell = powershell_command()
    if powershell is None:
        print("未找到 PowerShell。Windows 安装 Windows PowerShell，Linux CI 安装 pwsh。", file=sys.stderr)
        return 2
    if mode in ("provider", "everything") and not (ROOT / "key.txt").is_file():
        print("真实 Provider 验收需要仓库根目录 key.txt；入口只检查文件存在，不读取或打印内容。", file=sys.stderr)
        return 2

    started_at = datetime.now(timezone.utc)
    run_id = started_at.strftime("%Y%m%dT%H%M%S%fZ")
    LOG_DIRECTORY.mkdir(parents=True, exist_ok=True)
    log_path = LOG_DIRECTORY / f"{run_id}-{mode}.log"
    commands = test_commands(mode, powershell, args.capacity_duration_seconds, run_id)

    with log_path.open("xb") as log:
        write_line(log, f"LLMGateway test mode: {mode}")
        write_line(log, f"Started at (UTC): {started_at.isoformat()}")
        display_log_path = log_path.relative_to(ROOT)
        write_line(log, f"Log: {display_log_path}")
        if mode in ("capacity", "everything"):
            write_line(log, f"Capacity steady duration: {args.capacity_duration_seconds}s")

        for index, (name, command) in enumerate(commands, start=1):
            write_line(log, f"\n==> [{index}/{len(commands)}] {name}")
            status = run_logged(command, log)
            if status != 0:
                write_line(log, f"\nFAILED: {name} exited with code {status}", stderr=True)
                write_line(log, f"把这个日志路径告诉 Agent：{display_log_path}", stderr=True)
                return status

        finished_at = datetime.now(timezone.utc)
        write_line(log, f"\nPASSED: {mode}")
        write_line(log, f"Finished at (UTC): {finished_at.isoformat()}")
        write_line(log, f"Duration: {finished_at - started_at}")
        write_line(log, f"把这个日志路径告诉 Agent：{display_log_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
