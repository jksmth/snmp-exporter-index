# Contributing

The change to snmp_exporter is the Go under `exporter/`. `scripts/apply.py` copies those files onto a clean upstream tree and wires three call sites in `main.go`. `make patch` clones the upstream tag, runs that, and writes `dist/snmp_exporter-<version>-index.patch`. The patch is a build product. Edit the Go, then regenerate it.

`scripts/apply.py` matches snmp_exporter v0.31.0. A newer tag that moves the handler or the landing page fails the apply with the anchor named. Update the anchor, keep the behavior in `exporter/`, and run `make test`.

```sh
make test         # simulator, index tests, patch round-trip, config load
make bench-check  # walk 32 and 256 interfaces, then fetch one
make bench        # switch, core, and subscriber sizes; rewrites bench/RESULTS.md
make patch        # dist/snmp_exporter-v0.31.0-index.patch
make image
```

`VERSION` selects the upstream tag. The image tag and the patch are that same version.

The simulator speaks SNMPv2c only. `make bench` is the measurement checked into `bench/RESULTS.md`. Packet counts should stay stable. Loopback times will not.
