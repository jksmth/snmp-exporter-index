# snmp_exporter with an index parameter

A drop-in build of [prometheus/snmp_exporter](https://github.com/prometheus/snmp_exporter) v0.31.0. The binary, flags, port, config path, and Prometheus text are the ones you already run. The scrape URL takes one more parameter, `index`.

Your service keeps calling the exporter over mTLS and parsing the text. Add the ifIndex you already know:

```text
/snmp?target=192.0.2.1&auth=public_v2&module=if_mib&index=42
```

Leave `index` off and the scrape walks the module exactly as upstream does.

`index` is the SNMP instance suffix, the part that follows a column OID. For IF-MIB and EtherLike-MIB that is the ifIndex. Several interfaces can be named in one scrape, as `index=42&index=43` or `index=42,43`.

## What the exporter does with it

Interface tables are stored by column. `ifName.42` and `ifHCInOctets.42` are not one subtree, and the walk list in `snmp.yml` is a collapsed prefix such as `1.3.6.1.2.1.2`. Appending `.42` to that prefix does not mean ifIndex 42.

When `index` matches a metric, the exporter GETs `<column OID>.<index>` for each matching metric, plus the lookup columns (`ifName`, `ifDescr`, `ifAlias`) so the labels on the text stay the same. Scalars in that module are fetched at `oid.0`. Walks and dynamic filters in that module are skipped.

The stock `if_mib` module has 39 metrics, all indexed by a single integer except `ifNumber`. One ifIndex is two SNMP GET packets at the default `max_repetitions` of 25. A full walk of that module reads every interface on the device.

A module is narrowed only when it actually contains metrics with that index shape. `module=if_mib,system&index=42` narrows `if_mib` and still scrapes `system` normally, because `system` has no integer index. A table with two integer indexes, such as `ifStack`, is included when the suffix has two components (`index=1.42`) and is left out of a one-component request when the same module also has ifIndex columns.

An empty `index=` is ignored. A value that is not a dotted sequence of integers is HTTP 400.

## Smaller module

`examples/if_index.yml` is the column set from the request: names, speed, admin and oper status, octet counters, and `dot3StatsDuplexStatus` (EtherLike-MIB `1.3.6.1.2.1.10.7.2.1.19`, same ifIndex). Duplex is a gauge: 1 unknown, 2 halfDuplex, 3 fullDuplex. `ifOperStatus` and `ifAdminStatus` use the same numeric values as the stock module.

Eleven columns fit in one GET. `ifInOctets` is the 32-bit counter. `ifHCInOctets` and `ifHCOutOctets` are the 64-bit counters.

The image contains that file at `/etc/snmp_exporter/if_index.yml` and does not load it unless you add the flag. The default command is still `--config.file=/etc/snmp_exporter/snmp.yml`.

```text
--config.file=/etc/snmp_exporter/snmp.yml --config.file=/etc/snmp_exporter/if_index.yml
```

```text
/snmp?target=192.0.2.1&module=if_index&index=42
```

The same module without `index` walks only those columns, for every interface.

## Build

```sh
make test
make image
```

`make test` clones v0.31.0, applies `patches/0001-index-query-parameter.patch`, runs the index tests, and loads the example module beside upstream `snmp.yml`. Go 1.22 or newer will download the Go 1.26 toolchain named in upstream `go.mod`.

The image runs on port 9116, reads `/etc/snmp_exporter/snmp.yml`, and accepts the same flags, including `--web.config.file` for the mTLS you already terminate on the exporter. Version text on the landing page ends in `-index`, so a running container is easy to tell from upstream.

```sh
docker build -t snmp-exporter-index:v0.31.0 .
```

BuildKit sets `TARGETARCH`. The default is `amd64`.

## Image from GitHub

Pushes and pull requests to `main` run `make test` and do not publish an image. Pushing a tag such as `v0.31.0` publishes `ghcr.io/<owner>/<repo>:<tag>` and `:latest`. A manual run publishes `ghcr.io/<owner>/<repo>:sha-<commit>` and leaves `:latest` where it is. The image is `linux/amd64` and carries an SBOM. Build provenance is attached to the image in GHCR. A private personal repository cannot also store that attestation on GitHub. A private repository gets a private package; pull it after `docker login ghcr.io`.

```sh
git tag v0.31.0
git push origin v0.31.0
```

## Moving to a newer upstream release

Set `VERSION` to the new tag and run `make test`. If `patch` stops applying, refresh `patches/0001-index-query-parameter.patch` against that tag. The change is the handler in `main.go` plus `collector/index.go`. The config format and the Prometheus text are untouched, which is what keeps the service in front of it working.

Counters are still rendered the way snmp_exporter renders them, including the 2^53 wrap. `--no-snmp.wrap-large-counters` is the upstream switch for a consumer that is not Prometheus.
