# Contributing

The change to snmp_exporter is the Go under `exporter/`. `scripts/apply.py` copies those files onto a clean upstream tree and wires the call sites in `main.go`. `make patch` clones the upstream tag, runs that, and writes `dist/snmp_exporter-<version>-index.patch`. The patch is a build product. Edit the Go, then regenerate it.

Do not open an upstream pull request for the index parameter. prometheus/snmp_exporter declined request-time index selection so that modules stay static ([issue 131](https://github.com/prometheus/snmp_exporter/issues/131)). The README records that decision.

`scripts/apply.py` matches snmp_exporter v0.31.0. A newer tag that moves the handler or the landing page fails the apply with the anchor named. Update the anchor, keep the behavior in `exporter/`, and run `make test`.

```sh
make test         # simulator, index tests, patch round-trip, config load
make bench-check  # walk 32 and 256 interfaces, then fetch one
make bench        # switch, core, and subscriber sizes; rewrites bench/RESULTS.md
make patch        # dist/snmp_exporter-v0.31.0-index.patch
make binaries     # dist archives for release/platforms.txt
make image        # local linux/amd64 image
```

`VERSION` selects the upstream tag. The image tag, the patch, and the binaries are that same version. A rebuild tag such as `v0.31.0-1` keeps `UPSTREAM=v0.31.0` for the clone and names the image and the archives `v0.31.0-1`.

`release/platforms.txt` is the list of image architectures and binaries. Container rows use the Prometheus busybox repository for that architecture (`busybox-linux-armv7` for arm v7). Adding a row is enough for the next tag build; the release workflow reads the file.

CI takes the Go version from the cloned upstream `go.mod`. The image's `GOLANG_VERSION` is only the compiler that starts the build. A newer `go` line is downloaded by the toolchain.

The daily follow workflow publishes an upstream tag only after `make test` passes for that tag. Update the anchors, merge to `main`, and the next run retries. It does not move a tag that already has a release.

The simulator speaks SNMPv2c only. `make bench` is the measurement checked into `bench/RESULTS.md`. Packet counts should stay stable. Loopback times will not.
