#!/usr/bin/env python3
"""Scrape a patched snmp_exporter against ifsim and compare a full walk with one index.

Packet counts are the result that matters. On loopback a walk looks cheaper than
it is, because each SNMP packet would wait a round trip on a real device.
"""

import argparse
import json
import os
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime
from pathlib import Path
from zoneinfo import ZoneInfo

ASSUMED_RTT = 0.020
CASES = (
    ("if_mib", False),
    ("if_mib", True),
    ("if_index", False),
    ("if_index", True),
    ("if_index,system", True),
)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--exporter", required=True)
    parser.add_argument("--sim", required=True)
    parser.add_argument("--snmp-yml", required=True)
    parser.add_argument("--if-index", required=True)
    parser.add_argument("--counts", default="1024,8192", help="core interface counts")
    parser.add_argument("--edge", type=int, default=48)
    parser.add_argument("--subscriber", type=int, default=8192)
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--write", help="write the markdown report here")
    parser.add_argument("--sample", help="write one if_index index scrape here")
    parser.add_argument("--sim-listen", default="127.0.0.1:11611")
    parser.add_argument("--exporter-listen", default="127.0.0.1:19116")
    parser.add_argument("--rtt", default="0s")
    args = parser.parse_args()

    jobs = []
    if args.edge:
        jobs.append(("edge", args.edge))
    for part in args.counts.split(","):
        part = part.strip()
        if part:
            jobs.append(("core", int(part)))
    if args.subscriber:
        jobs.append(("subscriber", args.subscriber))
    if not jobs:
        sys.exit("no devices to scrape")

    stats_path = Path(".cache/sim-stats.json")
    stats_path.parent.mkdir(parents=True, exist_ok=True)
    log_path = Path(".cache/exporter-bench.log")
    exporter = start_exporter(args, log_path)
    rows = []
    sample_body = None
    try:
        wait_healthy(exporter, args.exporter_listen, log_path)
        for profile, count in jobs:
            sim, ready = start_sim(args, profile, count, stats_path)
            try:
                before = 0
                device_packets = 0.0
                for module, indexed in CASES:
                    index = str(ready["sample_index"]) if indexed else ""
                    body = scrape(args.exporter_listen, ready["listen"], module, index)
                    row = measure(profile, ready, module, index, body)
                    check_row(row, ready, body, indexed, module)
                    rows.append(row)
                    device_packets += row["packets"]
                    report(row)
                    if sample_body is None and module == "if_index" and indexed:
                        sample_body = body
                sim_packets = read_stats(sim, stats_path, before)
                if sim_packets != int(device_packets):
                    raise SystemExit(
                        f"{profile} {count}: simulator answered {sim_packets} packets, "
                        f"exporter sent {int(device_packets)}"
                    )
            finally:
                stop(sim)
    finally:
        stop(exporter)

    if args.check:
        return
    if args.sample and sample_body is not None:
        Path(args.sample).write_text(sample_body)
    if args.write:
        Path(args.write).write_text(render(rows, sample_body))
        print(f"wrote {args.write}", file=sys.stderr)


def start_exporter(args, log_path: Path) -> subprocess.Popen:
    log = log_path.open("w")
    return subprocess.Popen(
        [
            args.exporter,
            f"--config.file={args.snmp_yml}",
            f"--config.file={args.if_index}",
            f"--web.listen-address={args.exporter_listen}",
        ],
        stdout=log,
        stderr=subprocess.STDOUT,
    )


def start_sim(args, profile: str, count: int, stats_path: Path) -> tuple[subprocess.Popen, dict]:
    if stats_path.exists():
        stats_path.unlink()
    proc = subprocess.Popen(
        [
            args.sim,
            "-listen", args.sim_listen,
            "-interfaces", str(count),
            "-profile", profile,
            "-rtt", args.rtt,
            "-stats", str(stats_path),
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    assert proc.stdout is not None
    line = proc.stdout.readline()
    if not line:
        err = proc.stderr.read() if proc.stderr else ""
        stop(proc)
        raise SystemExit(f"simulator produced no ready line: {err}")
    return proc, json.loads(line)


def wait_healthy(proc: subprocess.Popen, listen: str, log_path: Path) -> None:
    url = f"http://{listen}/-/healthy"
    deadline = time.time() + 90
    while time.time() < deadline:
        if proc.poll() is not None:
            raise SystemExit("exporter exited\n" + log_path.read_text()[-4000:])
        try:
            with urllib.request.urlopen(url, timeout=1) as resp:
                if resp.status == 200:
                    return
        except (urllib.error.URLError, TimeoutError):
            time.sleep(0.2)
    raise SystemExit("exporter did not become healthy\n" + log_path.read_text()[-4000:])


def scrape(listen: str, target: str, module: str, index: str) -> str:
    query = {"target": target, "auth": "public_v2", "module": module}
    if index:
        query["index"] = index
    url = f"http://{listen}/snmp?{urllib.parse.urlencode(query)}"
    try:
        with urllib.request.urlopen(url, timeout=300) as resp:
            return resp.read().decode()
    except urllib.error.HTTPError as err:
        body = err.read().decode(errors="replace")
        raise SystemExit(f"HTTP {err.code} for {url}\n{body[:2000]}") from err


def measure(profile: str, ready: dict, module: str, index: str, body: str) -> dict:
    return {
        "profile": profile,
        "interfaces": int(ready["interfaces"]),
        "objects": int(ready["objects"]),
        "module": module,
        "index": index,
        "sample_name": ready["sample_name"],
        "packets": gauge(body, "snmp_scrape_packets_sent"),
        "pdus": gauge(body, "snmp_scrape_pdus_returned"),
        "walk_s": gauge(body, "snmp_scrape_walk_duration_seconds"),
        "scrape_s": gauge(body, "snmp_scrape_duration_seconds"),
        "prom_bytes": len(body.encode()),
        "series": series(body, "ifHCInOctets"),
        "body": body,
    }


def check_row(row: dict, ready: dict, body: str, indexed: bool, module: str) -> None:
    errors = [line for line in body.splitlines() if line.startswith("snmp_error")]
    if errors:
        raise SystemExit(f"scrape reported snmp_error\n" + "\n".join(errors[:10]))
    want = 1 if indexed else int(ready["interfaces"])
    if row["series"] != want:
        raise SystemExit(
            f"{row['profile']} {module} index={row['index'] or '-'}: "
            f"ifHCInOctets series {row['series']}, want {want}"
        )
    if indexed:
        if row["packets"] > 4:
            raise SystemExit(f"index scrape sent {row['packets']} packets")
        token = f'ifIndex="{int(ready["sample_index"])}"'
        if token not in body or ready["sample_name"] not in body:
            raise SystemExit(f"index scrape missing {token} or {ready['sample_name']}")
        if "system" in module and not any(line.startswith("sysDescr{") for line in body.splitlines()):
            raise SystemExit("system module produced no sysDescr")
    elif row["packets"] < 2 and int(ready["interfaces"]) > 25:
        raise SystemExit("walk sent too few packets to be a walk")


def gauge(body: str, name: str) -> float:
    total = 0.0
    found = False
    prefix = name + "{"
    for line in body.splitlines():
        if not line.startswith(prefix):
            continue
        found = True
        total += float(line.rsplit(" ", 1)[1])
    if not found:
        raise SystemExit(f"missing {name}\n" + "\n".join(body.splitlines()[:30]))
    return total


def series(body: str, name: str) -> int:
    prefix = name + "{"
    return sum(1 for line in body.splitlines() if line.startswith(prefix))


def read_stats(proc: subprocess.Popen, path: Path, before: int) -> int:
    deadline = time.time() + 5
    while time.time() < deadline:
        os.kill(proc.pid, signal.SIGUSR1)
        time.sleep(0.05)
        if path.exists():
            data = json.loads(path.read_text())
            if int(data.get("seq", 0)) != before:
                return int(data["get"]) + int(data["get_next"]) + int(data["get_bulk"])
    raise SystemExit("simulator did not write stats")


def report(row: dict) -> None:
    index = row["index"] or "-"
    print(
        f"{row['profile']:11} {row['interfaces']:6}  {row['module']:16} {index:>12}  "
        f"packets {row['packets']:8.0f}  pdus {row['pdus']:8.0f}  "
        f"scrape {row['scrape_s']:.3f}s  bytes {row['prom_bytes']}",
        file=sys.stderr,
        flush=True,
    )


def render(rows: list[dict], sample: str | None) -> str:
    when = datetime.now(ZoneInfo("Europe/London")).strftime("%Y-%m-%d %H:%M %Z")
    lines = [
        "# Walk versus one index",
        "",
        f"Measured {when} by `make bench`, against the simulator on loopback with no added delay.",
        "The agent implements the standard ifTable and ifXTable columns, plus dot3StatsDuplexStatus.",
        "snmp_exporter walks with GetBulk and max-repetitions 25, one packet at a time.",
        "",
        "Loopback hides the cost of those packets. A device that answers in 20 ms, with one packet in flight, spends `packets × 20 ms` waiting. That column is arithmetic on the measured packet count, not a second lab.",
        "",
        "| Profile | Interfaces | Module | Index | Packets | PDUs | Prometheus text | Loopback | At 20 ms RTT |",
        "| --- | ---: | --- | --- | ---: | ---: | ---: | ---: | ---: |",
    ]
    for row in rows:
        lines.append(
            "| {profile} | {interfaces} | `{module}` | {index} | {packets} | {pdus} | {bytes} | {loop} | {rtt} |".format(
                profile=row["profile"],
                interfaces=row["interfaces"],
                module=row["module"],
                index=row["index"] or "—",
                packets=comma(row["packets"]),
                pdus=comma(row["pdus"]),
                bytes=f"{row['prom_bytes']:,} B",
                loop=duration(row["scrape_s"]),
                rtt=duration(row["packets"] * ASSUMED_RTT),
            )
        )
    lines.extend(["", prose(rows), ""])
    if sample:
        interesting = [
            line for line in sample.splitlines()
            if line.startswith(("ifName{", "ifHCInOctets{", "ifOperStatus{", "dot3StatsDuplexStatus{", "snmp_scrape_"))
        ]
        lines.extend([
            "## One interface, from the edge-switch run",
            "",
            "This is the `if_index` module with `index` set, not a walk. The full exposition is `bench/sample_if_index.prom`.",
            "",
            "```text",
            *interesting,
            "```",
            "",
        ])
    lines.append("Regenerate with `make bench`. Packet counts should match. Loopback times, and the byte size of the text, move with the machine.")
    lines.append("")
    return "\n".join(lines)


def prose(rows: list[dict]) -> str:
    walks = [row for row in rows if row["module"] == "if_mib" and not row["index"]]
    if not walks:
        return "No if_mib walk was measured."
    walk = max(walks, key=lambda row: row["interfaces"])
    index = next(
        row for row in rows
        if row["module"] == "if_mib" and row["index"] and row["interfaces"] == walk["interfaces"] and row["profile"] == walk["profile"]
    )
    ratio = (walk["packets"] / index["packets"]) if index["packets"] else 0
    return (
        f"A {walk['profile']} device with {walk['interfaces']:,} interfaces took {comma(walk['packets'])} packets "
        f"to walk `if_mib` and returned {comma(walk['pdus'])} PDUs ({walk['prom_bytes']:,} bytes of Prometheus text). "
        f"The same module with `index={index['index']}` ({index['sample_name']}) took {comma(index['packets'])} packets "
        f"and {comma(index['pdus'])} PDUs, {comma(ratio)} times fewer packets. "
        f"At 20 ms per packet that walk is {duration(walk['packets'] * ASSUMED_RTT)}; the single index is {duration(index['packets'] * ASSUMED_RTT)}."
    )


def comma(value: float) -> str:
    return f"{int(round(value)):,}"


def duration(seconds: float) -> str:
    if seconds < 0.01:
        return f"{seconds * 1000:.1f} ms"
    if seconds < 1:
        return f"{seconds * 1000:.0f} ms"
    if seconds < 60:
        return f"{seconds:.1f} s"
    minutes, rest = divmod(seconds, 60)
    return f"{int(minutes)}m {rest:.0f}s"


def stop(proc: subprocess.Popen | None) -> None:
    if proc is None or proc.poll() is not None:
        return
    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait(timeout=5)


if __name__ == "__main__":
    main()
