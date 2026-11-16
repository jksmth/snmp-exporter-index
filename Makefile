VERSION ?= v0.31.0
IMAGE ?= snmp-exporter-index:$(VERSION)
CACHE := .cache/snmp_exporter

.PHONY: test image

# Apply the patch to a clean upstream tree, run the index tests, and load the
# example module beside upstream snmp.yml.
test:
	rm -rf $(CACHE)
	git clone --depth 1 --branch $(VERSION) https://github.com/prometheus/snmp_exporter.git $(CACHE)
	patch -p1 --batch --forward -d $(CACHE) < patches/0001-index-query-parameter.patch
	cd $(CACHE) && go test ./collector/ -count=1 -timeout 180s -run 'TestIndexSuffixes|TestModuleForIndexes'
	cd $(CACHE) && CGO_ENABLED=0 go build -o snmp_exporter_index .
	cp examples/if_index.yml $(CACHE)/if_index.yml
	cd $(CACHE) && ./snmp_exporter_index --dry-run --config.file=snmp.yml --config.file=if_index.yml

image:
	docker build \
		--build-arg SNMP_EXPORTER_VERSION=$(VERSION) \
		--build-arg BUILD_DATE=$$(date -u +%Y-%m-%dT%H:%M:%SZ) \
		-t $(IMAGE) .
