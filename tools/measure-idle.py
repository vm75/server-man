#!/usr/bin/env python3
"""Measure a quiet server-man stdio process without opening a network socket."""
import json
import os
import subprocess
import sys
import tempfile
import time
from pathlib import Path


def read_sample(pid):
    status = Path(f"/proc/{pid}/status").read_text()
    values = {}
    for key in ("VmRSS", "Threads"):
        for line in status.splitlines():
            if line.startswith(key + ":"):
                values[key] = int(line.split()[1])
                break
    stat = Path(f"/proc/{pid}/stat").read_text()
    fields = stat[stat.rfind(")") + 2:].split()
    values["cpu_ticks"] = int(fields[11]) + int(fields[12])
    values["open_fds"] = len(list(Path(f"/proc/{pid}/fd").iterdir()))
    return values


binary = os.path.abspath(sys.argv[1] if len(sys.argv) > 1 else "bin/serverman")
duration = float(sys.argv[2] if len(sys.argv) > 2 else "12")
with tempfile.TemporaryDirectory(prefix="serverman-idle-") as root:
    root = Path(root)
    config = root / "config.json"
    config.write_text(json.dumps({
        "vmRoot": str(root / "vms"),
        "stateDir": str(root / "state"),
        "tokenFile": str(root / "unused-token"),
        "allowWrites": False,
        "sampleIntervalSeconds": 2,
    }))
    process = subprocess.Popen([binary, "-transport", "stdio", "-config", str(config)], stdin=subprocess.PIPE, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    try:
        time.sleep(1)
        start = read_sample(process.pid)
        started = time.monotonic()
        time.sleep(duration)
        elapsed = time.monotonic() - started
        end = read_sample(process.pid)
        ticks_per_second = os.sysconf("SC_CLK_TCK")
        report = {
            "mode": "stdio with no MCP requests; real Linux sampler active",
            "interval_seconds": 2,
            "duration_seconds": round(elapsed, 2),
            "binary_bytes": os.path.getsize(binary),
            "rss_kib_start": start["VmRSS"],
            "rss_kib_end": end["VmRSS"],
            "threads_end": end["Threads"],
            "open_fds_end": end["open_fds"],
            "cpu_ticks": end["cpu_ticks"] - start["cpu_ticks"],
            "cpu_seconds": round((end["cpu_ticks"] - start["cpu_ticks"]) / ticks_per_second, 3),
            "cpu_percent_of_one_core": round(100 * (end["cpu_ticks"] - start["cpu_ticks"]) / ticks_per_second / elapsed, 2),
        }
        print(json.dumps(report, indent=2, sort_keys=True))
    finally:
        if process.stdin:
            process.stdin.close()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
        if process.returncode:
            error = process.stderr.read().decode("utf-8", "replace") if process.stderr else ""
            raise SystemExit(f"server-man exited {process.returncode}: {error}")
