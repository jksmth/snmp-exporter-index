#!/usr/bin/env python3
"""Read release/platforms.txt and build release binaries.

    python3 scripts/release.py matrix
    VERSION=v0.31.0 BUILD_DATE=... CACHE=.cache/snmp_exporter python3 scripts/release.py binaries
"""

import hashlib
import json
import os
import shutil
import subprocess
import sys
import tarfile
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PLATFORMS = ROOT / "release" / "platforms.txt"


def rows():
    found = []
    for raw in PLATFORMS.read_text().splitlines():
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        parts = line.split()
        if len(parts) not in (5, 6):
            raise SystemExit(f"bad platform line: {raw}")
        name, busybox, platform, goos, goarch = parts[:5]
        goarm = parts[5] if len(parts) == 6 else ""
        if goarm and not goarm.isdigit():
            raise SystemExit(f"bad goarm in: {raw}")
        found.append(
            {
                "name": name,
                "busybox": busybox,
                "platform": platform,
                "goos": goos,
                "goarch": goarch,
                "goarm": goarm,
            }
        )
    if not found:
        raise SystemExit(f"no platforms in {PLATFORMS}")
    return found


def matrix():
    include = [
        {"name": row["name"], "busybox": row["busybox"], "platform": row["platform"]}
        for row in rows()
        if row["platform"] != "-"
    ]
    if not include:
        raise SystemExit("no image platforms")
    json.dump(include, sys.stdout, separators=(",", ":"))
    sys.stdout.write("\n")


def binaries():
    version = os.environ.get("VERSION", "").strip()
    if not version.startswith("v"):
        raise SystemExit("VERSION must look like v0.31.0")
    build_date = os.environ.get("BUILD_DATE", "").strip() or "unknown"
    cache = Path(os.environ.get("CACHE", ROOT / ".cache" / "snmp_exporter"))
    if not (cache / "go.mod").is_file():
        raise SystemExit(f"no patched tree at {cache}; run make patch first")

    ver = version[1:]
    dist = ROOT / "dist"
    stage_root = dist / "bin"
    if stage_root.exists():
        shutil.rmtree(stage_root)
    stage_root.mkdir(parents=True)

    ldflags = " ".join(
        [
            "-s -w",
            f"-X github.com/prometheus/common/version.Version={version}-index",
            "-X github.com/prometheus/common/version.Revision=index",
            "-X github.com/prometheus/common/version.BuildUser=snmp-exporter-index",
            f"-X github.com/prometheus/common/version.BuildDate={build_date}",
        ]
    )
    extras = [
        cache / "snmp.yml",
        cache / "LICENSE",
        cache / "NOTICE",
        ROOT / "examples" / "if_index.yml",
    ]
    for extra in extras:
        if not extra.is_file():
            raise SystemExit(f"missing {extra}")

    archives = []
    for row in rows():
        folder = f"snmp_exporter-{ver}-index.{row['goos']}-{row['name']}"
        stage = stage_root / folder
        stage.mkdir()
        binary_name = "snmp_exporter.exe" if row["goos"] == "windows" else "snmp_exporter"
        env = os.environ.copy()
        env["CGO_ENABLED"] = "0"
        env["GOOS"] = row["goos"]
        env["GOARCH"] = row["goarch"]
        if row["goarm"]:
            env["GOARM"] = row["goarm"]
        else:
            env.pop("GOARM", None)
        print(f"build {folder}", flush=True)
        subprocess.run(
            ["go", "build", "-trimpath", "-ldflags", ldflags, "-o", str(stage / binary_name), "."],
            cwd=cache,
            env=env,
            check=True,
        )
        for extra in extras:
            shutil.copy(extra, stage / extra.name)
        tar_path = dist / f"{folder}.tar.gz"
        with tarfile.open(tar_path, "w:gz") as tar:
            tar.add(stage, arcname=folder)
        archives.append(tar_path)
        if row["goos"] == "windows":
            zip_path = dist / f"{folder}.zip"
            with zipfile.ZipFile(zip_path, "w", compression=zipfile.ZIP_DEFLATED) as zf:
                for path in sorted(stage.rglob("*")):
                    if path.is_file():
                        zf.write(path, Path(folder) / path.relative_to(stage))
            archives.append(zip_path)

    patch = dist / f"snmp_exporter-{version}-index.patch"
    files = list(archives)
    if patch.is_file():
        files.append(patch)
    lines = []
    for path in sorted(files, key=lambda item: item.name):
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        lines.append(f"{digest}  {path.name}")
    (dist / "sha256sums.txt").write_text("\n".join(lines) + "\n")
    shutil.rmtree(stage_root)
    print(f"wrote {len(archives)} archives and dist/sha256sums.txt", flush=True)


def main():
    if len(sys.argv) != 2 or sys.argv[1] not in {"matrix", "binaries"}:
        raise SystemExit("usage: release.py matrix|binaries")
    if sys.argv[1] == "matrix":
        matrix()
    else:
        binaries()


if __name__ == "__main__":
    main()
