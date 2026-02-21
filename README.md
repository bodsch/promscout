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

listen_address: ":8080"

log_format: json
log_level: info

max_workers: 50
```

------------------------------------------------------------------------

## 🏃 Running

    ./promscout --config config.yaml

CLI flags override YAML configuration.

------------------------------------------------------------------------

## 🌐 Endpoints

  Endpoint     Description
  ------------ -----------------------------------
  `/`          Prometheus HTTP Service Discovery
  `/metrics`   PromScout self-monitoring metrics

------------------------------------------------------------------------

## 🔗 Prometheus Integration

Add to your `prometheus.yml`:

``` yaml
scrape_configs:
  - job_name: "dynamic"
    http_sd_configs:
      - url: "http://promscout:8080/"
```

------------------------------------------------------------------------

## 📊 Self Monitoring Metrics

PromScout exposes internal metrics:

-   sd_discovery_runs_total
-   sd_discovery_skipped_total
-   sd_discovery_duration_seconds
-   sd_targets_discovered_total
-   sd_targets_valid_total
-   sd_last_discovery_timestamp
-   sd_active_discovery

------------------------------------------------------------------------

## 🛡 Design Principles

-   Deterministic configuration merging
-   No overlapping discovery cycles
-   Strict metrics validation (no HTML false positives)
-   Context-aware shutdown
-   Production-ready logging

------------------------------------------------------------------------

## 📜 License

MIT License
