// Copyright 2022 The Prometheus Authors
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
	"os"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// configFileEnvar is --config-file's own environment variable. It can't go
// through kingpin.DefaultEnvars() like every other flag: its value is needed
// before kingpin.Parse() runs (to inject file-provided flag defaults ahead of
// parsing), so it's resolved by preScanConfigFilePath instead.
const configFileEnvar = "AVALANCHE_CONFIG_FILE"

// preScanConfigFilePath finds --config-file's value by scanning raw argv,
// without going through kingpin: at the point this needs to run, flags
// haven't been parsed yet (this result is used to inject flag *defaults*
// before kingpin.Parse() is called). Falls back to AVALANCHE_CONFIG_FILE if
// the flag isn't present in args, matching every other flag's env var
// fallback. An explicit --config-file always wins over the env var, same as
// kingpin does for every other flag.
func preScanConfigFilePath(args []string) string {
	for i, a := range args {
		if a == "--config-file" && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, "--config-file="); ok {
			return v
		}
	}
	return os.Getenv(configFileEnvar)
}

// loadConfigFileDefaults reads a YAML file mapping flag names (as in
// --help, without the leading --) to values, and returns them as the
// []string form kingpin.FlagClause.Default expects -- a single-element slice
// for a scalar, or one element per item for a YAML list (repeatable flags
// like const-label).
func loadConfigFileDefaults(path string) (map[string][]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parsing %s as YAML: %w", path, err)
	}

	defaults := make(map[string][]string, len(parsed))
	for name, value := range parsed {
		defaults[name] = configFileValueToStrings(value)
	}
	return defaults, nil
}

// configFileValueToStrings renders one YAML value as the string(s) kingpin's
// flag.Value.Set expects, the same textual form --help documents (e.g. bool
// "true"/"false", not YAML's own boolean literals if they ever diverged).
func configFileValueToStrings(value any) []string {
	list, ok := value.([]any)
	if !ok {
		return []string{configFileScalarToString(value)}
	}
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = configFileScalarToString(v)
	}
	return out
}

func configFileScalarToString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}
