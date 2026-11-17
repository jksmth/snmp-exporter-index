#!/usr/bin/env python3
"""Copy the index source onto an snmp_exporter tree and wire it into main.go.

The Go under exporter/ is the source of the change. This script is the only
place that knows where snmp_exporter v0.31.0 calls into a scrape. A newer
upstream tag that moves those call sites fails here, with the anchor named,
instead of producing a patch that silently does nothing.
"""

import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
UPSTREAM_VERSION = "v0.31.0"

COPIES = {
    "exporter/collector/index.go": "collector/index.go",
    "exporter/collector/index_test.go": "collector/index_test.go",
    "exporter/collector/raw.go": "collector/raw.go",
    "exporter/collector/raw_test.go": "collector/raw_test.go",
    "exporter/index_param.go": "index_param.go",
    "exporter/raw.go": "raw.go",
    "exporter/raw_test.go": "raw_test.go",
}


def replace_once(text: str, old: str, new: str, label: str) -> str:
    found = text.count(old)
    if found != 1:
        sys.exit(f"apply: {label}: found {found} times in main.go, want 1 (written against snmp_exporter {UPSTREAM_VERSION})")
    return text.replace(old, new, 1)


def wire_main(text: str) -> str:
    text = replace_once(
        text,
        "\tmodules, err := parseModules(query)\n"
        "\tif err != nil {\n"
        "\t\thttp.Error(w, err.Error(), http.StatusBadRequest)\n"
        "\t\tsnmpRequestErrors.Inc()\n"
        "\t\treturn\n"
        "\t}\n"
        "\tsc.mu.RLock()\n",
        "\tmodules, err := parseModules(query)\n"
        "\tif err != nil {\n"
        "\t\thttp.Error(w, err.Error(), http.StatusBadRequest)\n"
        "\t\tsnmpRequestErrors.Inc()\n"
        "\t\treturn\n"
        "\t}\n"
        "\tindexes, ok := takeIndex(w, query)\n"
        "\tif !ok {\n"
        "\t\treturn\n"
        "\t}\n"
        "\tsc.mu.RLock()\n",
        "parseModules",
    )
    text = replace_once(
        text,
        "\t\tnmodules = append(nmodules, collector.NewNamedModule(m, module))\n",
        "\t\tmoduleForScrape, err := moduleForRequest(m, module, indexes)\n"
        "\t\tif err != nil {\n"
        "\t\t\tsc.mu.RUnlock()\n"
        "\t\t\thttp.Error(w, err.Error(), http.StatusBadRequest)\n"
        "\t\t\tsnmpRequestErrors.Inc()\n"
        "\t\t\treturn\n"
        "\t\t}\n"
        "\t\tnmodules = append(nmodules, collector.NewNamedModule(m, moduleForScrape))\n",
        "NewNamedModule",
    )
    text = replace_once(
        text,
        "\tlogger = logger.With(\"auth\", authName, \"target\", target)\n"
        "\tregistry := prometheus.NewRegistry()\n",
        "\tlogger = logger.With(\"auth\", authName, \"target\", target)\n"
        "\tlogger = loggerWithIndex(logger, indexes)\n"
        "\tregistry := prometheus.NewRegistry()\n",
        "request logger",
    )
    text = replace_once(
        text,
        '\t\t\t\t\t\tValue:       "if_mib",\n'
        "\t\t\t\t\t},\n"
        "\t\t\t\t},\n",
        '\t\t\t\t\t\tValue:       "if_mib",\n'
        "\t\t\t\t\t},\n"
        "\t\t\t\t\tindexLandingInput(),\n"
        "\t\t\t\t},\n",
        "landing page",
    )
    text = replace_once(
        text,
        "\thttp.HandleFunc(proberPath, func(w http.ResponseWriter, r *http.Request) {\n"
        "\t\thandler(w, r, logger, exporterMetrics)\n"
        "\t})\n",
        "\thttp.HandleFunc(proberPath, func(w http.ResponseWriter, r *http.Request) {\n"
        "\t\thandler(w, r, logger, exporterMetrics)\n"
        "\t})\n"
        "\thttp.HandleFunc(rawPath, func(w http.ResponseWriter, r *http.Request) {\n"
        "\t\trawHandler(w, r, logger)\n"
        "\t})\n",
        "raw endpoint",
    )
    return text


def wire_collector(text: str) -> str:
    return replace_once(
        text,
        "\t\t\tif head.metric != nil {\n"
        "\t\t\t\t// Found a match.\n"
        "\t\t\t\tsamples := pduToSamples(oidList[i+1:], &pdu, head.metric, oidToPdu, logger, c.metrics)\n",
        "\t\t\tif head.metric != nil {\n"
        "\t\t\t\t// Found a match. A longer suffix is a different object under\n"
        "\t\t\t\t// this column; keeping the leading sub-identifiers would\n"
        "\t\t\t\t// collapse those objects onto one label set.\n"
        "\t\t\t\tif !instanceSuffixMatches(oidList[i+1:], head.metric) {\n"
        "\t\t\t\t\tlogger.Debug(\"Skipping PDU whose index is longer than the metric\", \"oid\", oid, \"metric\", head.metric.Name)\n"
        "\t\t\t\t\tbreak\n"
        "\t\t\t\t}\n"
        "\t\t\t\tsamples := pduToSamples(oidList[i+1:], &pdu, head.metric, oidToPdu, logger, c.metrics)\n",
        "pduToSamples",
    )


def main() -> None:
    if len(sys.argv) != 2:
        sys.exit(f"usage: {sys.argv[0]} <snmp_exporter tree>")
    tree = Path(sys.argv[1]).resolve()
    main_go = tree / "main.go"
    collector_go = tree / "collector" / "collector.go"
    if not main_go.is_file():
        sys.exit(f"apply: {main_go} does not exist")
    if not collector_go.is_file():
        sys.exit(f"apply: {collector_go} does not exist")
    for src, dest in COPIES.items():
        source = ROOT / src
        if not source.is_file():
            sys.exit(f"apply: missing {source}")
        target = tree / dest
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target)
    main_go.write_text(wire_main(main_go.read_text()))
    collector_go.write_text(wire_collector(collector_go.read_text()))


if __name__ == "__main__":
    main()
