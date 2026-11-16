// Copyright 2026 The snmp-exporter-index authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/prometheus/exporter-toolkit/web"

	"github.com/prometheus/snmp_exporter/collector"
	"github.com/prometheus/snmp_exporter/config"
)

// takeIndex parses the optional index query parameter.
// The bool is false when an HTTP error has already been written.
func takeIndex(w http.ResponseWriter, query url.Values) ([]string, bool) {
	indexes, err := collector.IndexSuffixes(query["index"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		snmpRequestErrors.Inc()
		return nil, false
	}
	return indexes, true
}

// moduleForRequest narrows module to indexes. An empty index list, or a
// module whose metrics do not use that index shape, keeps the loaded module.
func moduleForRequest(name string, module *config.Module, indexes []string) (*config.Module, error) {
	if len(indexes) == 0 {
		return module, nil
	}
	rewritten, applied, err := collector.ModuleForIndexes(module, indexes)
	if err != nil {
		return nil, fmt.Errorf("module %q: %w", name, err)
	}
	if !applied {
		return module, nil
	}
	return rewritten, nil
}

func loggerWithIndex(logger *slog.Logger, indexes []string) *slog.Logger {
	if len(indexes) == 0 {
		return logger
	}
	return logger.With("index", strings.Join(indexes, ","))
}

func indexLandingInput() web.LandingFormInput {
	return web.LandingFormInput{
		Label:       "Index",
		Type:        "text",
		Name:        "index",
		Placeholder: "1,3,10-20",
		Value:       "",
	}
}
