# PromScout

**PromScout** is a lightweight, network-based Prometheus HTTP Service Discovery engine written in Go.

It scans configured networks for Prometheus exporters, validates real
metrics endpoints, and exposes dynamic HTTP-SD targets.

------------------------------------------------------------------------

## ✨ Features

-   Network CIDR scanning
-   Parallel port probing (worker pool)
-   Strict Prometheus metrics validation
-   No overlapping discovery cycles
-   HTTP Service Discovery endpoint
-   Built-in self-monitoring (`/metrics`)
-   YAML configuration
-   Graceful shutdown with context support
-   Minimal dependencies

------------------------------------------------------------------------

## 🚀 How It Works

1.  PromScout scans configured CIDR networks.
2.  It probes configured TCP ports.
3.  It validates real Prometheus exposition format.
4.  It exposes valid targets via HTTP-SD.
5.  It exposes its own internal metrics.

------------------------------------------------------------------------

## 📦 Installation

### Build from source

    git clone https://github.com/bodsch/promscout.git
    cd promscout
    make build

Binary will be available in:

    bin/promscout

------------------------------------------------------------------------

## ⚙️ Configuration

PromScout uses YAML configuration.

Example:

``` yaml
network_cidr: 192.168.0.0/24

ports:
  - 9100
  - 9115
  - 9121
  - 9130

metrics_paths:
  - /
  - /metrics
  - /unpoller

interval: 30s
timeout: 3s
dial_timeout: 800ms

listen_address: ":8080"

log_format: json
log_level: info

max_workers: 512
validate_workers: 50

cache_file: ""
```

`dial_timeout` bounds the TCP port probe and is the dominant factor for
scan runtime on large networks; keep it short. `max_workers` sizes the
parallel dial stage, `validate_workers` the parallel HTTP validation
stage — both stages run as a streaming pipeline, so validation starts as
soon as the first open port is found. `cache_file`, when set, persists
validated targets after every cycle and reloads them on startup for a
warm start.

------------------------------------------------------------------------

## 🏃 Running

    ./promscout --config config.yaml

CLI flags override YAML configuration.

------------------------------------------------------------------------

## 🌐 Endpoints

  Endpoint       Description
  -------------- ---------------------------------------------------
  `/`            Prometheus HTTP Service Discovery
  `/metrics`     PromScout self-monitoring metrics
  `/-/healthy`   Liveness probe (200 while the server is serving)
  `/-/ready`     Readiness probe (200 once the first scan completed, otherwise 503)

------------------------------------------------------------------------

## 🔗 Prometheus Integration

Add to your `prometheus.yml`:

``` yaml
scrape_configs:
  - job_name: "dynamic"
    http_sd_configs:
      - url: "http://promscout:8080/"
```

### Service identity and the `job` label

For every target PromScout derives the exposed service identity from the
metrics themselves and attaches it as meta labels:

  Label                          Meaning
  ------------------------------ ------------------------------------------
  `__meta_promscout_exporter`    Derived exporter/service name
  `__meta_promscout_version`     Build version (from `*_build_info`), if any
  `__meta_promscout_source`      How it was derived: `target_info`, `build_info` or `prefix`

Derivation order (most to least authoritative):

1.  `target_info{service_name="…"}` — OpenTelemetry resource identity
2.  `<name>_build_info{version="…"}` — classic exporter build metric
3.  the dominant metric namespace prefix (ignoring `go_`/`process_`/…)

The `job` label defaults to `promscout`. Following Prometheus conventions,
map the meta label to `job` yourself via relabeling:

``` yaml
scrape_configs:
  - job_name: "dynamic"
    http_sd_configs:
      - url: "http://promscout:8080/"
    relabel_configs:
      - source_labels: [__meta_promscout_exporter]
        target_label: job
```

Alternatively, enable `guess_job` (CLI `--guess-job`) to let PromScout use
the derived exporter name as the `job` label directly — a best-effort
convenience for setups that do not relabel.

------------------------------------------------------------------------

## 📊 Self Monitoring Metrics

PromScout exposes internal metrics:

-   promscout_discovery_runs_total
-   promscout_discovery_skipped_total
-   promscout_discovery_duration_seconds
-   promscout_targets_discovered_total
-   promscout_targets_valid_total
-   promscout_last_discovery_timestamp
-   promscout_active_discovery

------------------------------------------------------------------------

## 🛡 Design Principles

-   Deterministic configuration merging
-   No overlapping discovery cycles
-   Strict metrics validation (no HTML false positives)
-   Context-aware shutdown
-   Production-ready logging

### Warm start and cancelled cycles

A cycle interrupted by shutdown does **not** publish its result. The scanner
closes its channel on cancellation, so an interrupted cycle ends with an empty
or partial target set — which says nothing about what is out there. Publishing
it would replace the live targets, answer the HTTP-SD endpoint with an empty
array, and write that emptiness over the warm-start cache, leaving the next
start with nothing to seed from.

A cycle that ran to completion and found nothing does publish that, so a
decommissioned exporter leaves the target list.

------------------------------------------------------------------------

---

## Author and License

- Bodo Schulz

## License

[Apache](LICENSE)
