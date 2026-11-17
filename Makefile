VERSION ?= v0.31.0
IMAGE ?= snmp-exporter-index:$(VERSION)
CACHE := .cache/snmp_exporter
STAMP := $(CACHE)/.stamp
PATCH := dist/snmp_exporter-$(VERSION)-index.patch
SIM := .cache/ifsim
EXPORTER := $(CACHE)/snmp_exporter
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: test patch image binaries bench bench-check clean

$(STAMP): exporter/collector/index.go exporter/collector/index_test.go exporter/collector/raw.go exporter/collector/raw_test.go exporter/index_param.go exporter/raw.go exporter/raw_test.go scripts/apply.py scripts/rebuild.sh
	VERSION=$(VERSION) CACHE=$(CACHE) scripts/rebuild.sh
	mkdir -p $(CACHE)
	touch $(STAMP)

$(EXPORTER): $(STAMP)
	cd $(CACHE) && CGO_ENABLED=0 go build -o snmp_exporter .

$(SIM): sim/main.go sim/agent.go sim/device.go sim/agent_test.go sim/go.mod
	mkdir -p .cache
	cd sim && go build -o ../$(SIM) .

# Unit-test the simulator, apply the source to a clean upstream tree, prove the
# generated patch round-trips, then load the example module.
test: $(STAMP)
	cd sim && go test .
	cd $(CACHE) && go test ./collector/ -count=1 -timeout 180s -run 'TestIndexSuffixes|TestModuleForIndexes|TestInstanceSuffixMatches|TestMalformedDot3|TestRaw|TestFormatRaw'
	cd $(CACHE) && go test -count=1 -timeout 120s -run 'TestRaw' .
	$(MAKE) $(EXPORTER)
	cp examples/if_index.yml $(CACHE)/if_index.yml
	cd $(CACHE) && ./snmp_exporter --dry-run --config.file=snmp.yml --config.file=if_index.yml

# Write dist/snmp_exporter-$(VERSION)-index.patch from the Go under exporter/.
patch:
	rm -f $(STAMP)
	$(MAKE) $(STAMP)
	@test -f $(PATCH)
	@echo $(PATCH)

image:
	docker build \
		--build-arg SNMP_EXPORTER_VERSION=$(VERSION) \
		--build-arg TARGETARCH=amd64 \
		--build-arg BUSYBOX_ARCH=amd64 \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(IMAGE) .

# Cross-compile the archives in release/platforms.txt. The patch must already
# exist (the stamp builds it) so sha256sums.txt covers the patch too.
binaries: $(STAMP)
	VERSION=$(VERSION) BUILD_DATE=$(BUILD_DATE) CACHE=$(CACHE) python3 scripts/release.py binaries

# A short walk-versus-index check. Used in CI.
bench-check: $(EXPORTER) $(SIM)
	python3 bench/run.py --check --counts 32,256 --edge 0 --subscriber 0 \
		--exporter $(EXPORTER) --sim $(SIM) \
		--snmp-yml $(CACHE)/snmp.yml --if-index examples/if_index.yml

# The published measurement: a switch, a core, and a subscriber device.
bench: $(EXPORTER) $(SIM)
	python3 bench/run.py --counts 1024,8192 --edge 48 --subscriber 8192 \
		--write bench/RESULTS.md --sample bench/sample_if_index.prom \
		--exporter $(EXPORTER) --sim $(SIM) \
		--snmp-yml $(CACHE)/snmp.yml --if-index examples/if_index.yml

clean:
	rm -rf .cache dist
