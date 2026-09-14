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
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"syscall"

	"github.com/nelkinda/health-go"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
	"gopkg.in/alecthomas/kingpin.v2"

	"github.com/prometheus-community/avalanche/metricsgen"
)

func main() {
	kingpin.Version(version.Print("avalanche"))
	log.SetFlags(log.Ltime | log.Lshortfile) // Show file name and line in logs.

	// Let every flag registered below (here and in metricsgen.NewConfigFromFlags/
	// NewWriteConfigFromFlags) also be set via an AVALANCHE_<FLAG_NAME> env var
	// (e.g. --series-count <-> AVALANCHE_SERIES_COUNT), so the same config (a
	// shared ConfigMap of env vars, say) can be handed to many differently
	// --role'd instances. An explicit CLI flag always overrides the env var.
	// Exclude --help/--version: DefaultEnvars() would otherwise also wire them
	// up, and an accidental AVALANCHE_HELP/AVALANCHE_VERSION in a shared
	// ConfigMap would make every instance sharing it print help/version and
	// exit instead of running.
	kingpin.CommandLine.Name = "avalanche"
	kingpin.CommandLine.DefaultEnvars()
	kingpin.CommandLine.HelpFlag.NoEnvar()
	kingpin.CommandLine.VersionFlag.NoEnvar()

	kingpin.CommandLine.Help = "avalanche - metrics test server\n" +
		"\n" +
		"Capable of generating metrics to server on \\metrics or send via Remote Write.\n" +
		"\n" +
		"\nOptionally, on top of the --value-interval, --series-interval, --metric-interval logic, you can specify advanced --series-operation-mode:\n" +
		"  double-halve:\n" +
		"    Alternately doubles and halves the series count at regular intervals.\n" +
		"    Usage: ./avalanche --series-operation-mode=double-halve --series-change-interval=30 --series-count=500\n" +
		"    Description: This mode alternately doubles and halves the series count at regular intervals.\n" +
		"                 The series count is doubled on one tick and halved on the next, ensuring it never drops below 1.\n" +
		"\n" +
		"  gradual-change:\n" +
		"    Gradually changes the series count by a fixed rate at regular intervals.\n" +
		"    Usage: ./avalanche --series-operation-mode=gradual-change --series-change-interval=30 --series-change-rate=100 --max-series-count=2000 --min-series-count=200\n" +
		"    Description: This mode gradually increases the series count by seriesChangeRate on each tick up to maxSeriesCount,\n" +
		"                 then decreases it back to the minSeriesCount, and repeats this cycle indefinitely.\n" +
		"                 The series count is incremented by seriesChangeRate on each tick, ensuring it never drops below 1." +
		"\n" +
		"  spike:\n" +
		"    Periodically spikes the series count by a given multiplier.\n" +
		"    Usage: ./avalanche --series-operation-mode=spike --series-change-interval=180 --series-count=100 --spike-multiplier=1.5\n" +
		"    Description: This mode periodically increases the series count by a spike multiplier on one tick and\n" +
		"                 then returns it to the original count on the next tick. This pattern repeats indefinitely,\n" +
		"                 creating a spiking effect in the series count.\n"

	cfg := metricsgen.NewConfigFromFlags(kingpin.Flag)
	port := kingpin.Flag("port", "Port to serve at").Default("9001").Int()
	writeCfg := metricsgen.NewWriteConfigFromFlags(kingpin.Flag)
	var role string
	kingpin.Flag("role", "Exclusive role this instance plays. \"\" (default) runs every "+
		"subsystem gated only by its own trigger flag (--remote-url, --rules-endpoint-path), "+
		"exactly as before this flag existed. \"scrape-target\", \"remote-writer\" or \"ruler\" "+
		"runs ONLY that one subsystem, ignoring the others' trigger flags -- lets many instances "+
		"share one common config (e.g. via AVALANCHE_* env vars) and differ only in "+
		"--role/AVALANCHE_ROLE. \"querier\" is reserved for a future querier subsystem.").
		Default("").
		EnumVar(&role, "", "scrape-target", "remote-writer", "ruler")

	kingpin.Parse()
	if err := cfg.Validate(); err != nil {
		kingpin.FatalUsage("configuration error: %v", err)
	}
	if err := writeCfg.Validate(); err != nil {
		kingpin.FatalUsage("remote write config validation failed: %v", err)
	}
	switch role {
	case "remote-writer":
		if writeCfg.URL == nil {
			kingpin.FatalUsage("--role=remote-writer requires --remote-url to be set")
		}
	case "ruler":
		if cfg.RulesEndpointPath == "" {
			kingpin.FatalUsage("--role=ruler requires --rules-endpoint-path to be set (non-empty)")
		}
	}

	doScrape := role == "" || role == "scrape-target"
	doRemoteWrite := (role == "" || role == "remote-writer") && writeCfg.URL != nil
	doRules := (role == "" || role == "ruler") && cfg.RulesEndpointPath != ""
	needsCollector := doScrape || doRemoteWrite

	var rulesYAML []byte
	if doRules {
		var err error
		rulesYAML, err = metricsgen.GenerateRules(*cfg)
		if err != nil {
			log.Fatalf("generating rules: %v", err)
		}
	}

	reg := prometheus.NewRegistry()

	log.Println("initializing avalanche...")

	var g run.Group
	g.Add(run.SignalHandler(context.Background(), os.Interrupt, syscall.SIGTERM))

	if needsCollector {
		collector := metricsgen.NewCollector(*cfg)
		reg.MustRegister(collector)
		writeCfg.UpdateNotify = collector.UpdateNotifyCh()
		g.Add(collector.Run, collector.Stop)
	}

	// One-off remote write send mode.
	if doRemoteWrite {
		ctx, cancel := context.WithCancel(context.Background())
		g.Add(func() error {
			if err := metricsgen.RunRemoteWriting(ctx, slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})), writeCfg, reg); err != nil {
				return err
			}
			return nil // One-off.
		}, func(error) { cancel() })
	}

	httpSrv := &http.Server{Addr: fmt.Sprintf(":%v", *port)}
	g.Add(func() error {
		if doScrape {
			fmt.Printf("Serving your metrics at :%v/metrics\n", *port)
			http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
				EnableOpenMetrics: true,
			}))
		}
		http.HandleFunc("/health", health.New(health.Health{}).Handler)
		if doRules {
			fmt.Printf("Serving generated Prometheus rules at :%v%v\n", *port, cfg.RulesEndpointPath)
			http.HandleFunc(cfg.RulesEndpointPath, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/yaml")
				_, _ = w.Write(rulesYAML)
			})
		}
		return httpSrv.ListenAndServe()
	}, func(_ error) {
		_ = httpSrv.Shutdown(context.Background())
	})

	log.Println("starting avalanche...")
	if err := g.Run(); err != nil {
		//nolint:errcheck
		log.Fatalf("running avalanche failed %v", err)
	}
	log.Println("avalanche finished")
}
