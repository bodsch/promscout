#!/usr/bin/env python3
"""Generate the Grafana dashboard in this directory.

    python3 contrib/grafana/generate.py [output-dir]

The JSON file is output: change this script and run `make dashboards`
instead of editing it, or the next run undoes the edit.

Every promscout_* metric used here is defined in internal/discovery/scheduler.go
or internal/monitoring/buildinfo.go, and TestSchedulerExposesTheDocumentedMetrics
fails if one of them disappears. A panel reading a metric that no binary exports
shows "No data" and looks exactly like a service that was not scraped yet.
Standard library only.
"""

import json
import os
import sys

DS = {"type": "prometheus", "uid": "${datasource}"}
out_dir = sys.argv[1] if len(sys.argv) > 1 else os.path.dirname(os.path.abspath(__file__))

# Thresholds for the age of the last completed cycle. The README defines no
# alert rules, so these follow the shipped configuration: config.example sets
# `interval: 1m`. A cycle that is skipped because the previous one still runs
# does not complete, so on a large network the age legitimately reaches scan
# duration + interval. Raise these together with `interval`.
INTERVAL_S = 60
AGE_WARN_S = 5 * INTERVAL_S
AGE_CRIT_S = 15 * INTERVAL_S

# Window for duration statistics. Cycles complete about once per interval;
# $__rate_interval is often shorter than that and would leave gaps.
DURATION_WINDOW = "15m"


class B:
    """Lays panels out left to right on a 24-column grid, wrapping lines."""

    def __init__(self):
        self.panels, self.y, self.id = [], 0, 1
        self.x, self.rowh = 0, 0

    def row(self, title):
        self._nl()
        self.panels.append({"type": "row", "title": title, "id": self._nid(),
                            "collapsed": False, "gridPos": {"h": 1, "w": 24, "x": 0, "y": self.y}, "panels": []})
        self.y += 1

    def _nid(self):
        self.id += 1
        return self.id

    def _nl(self):
        if self.x:
            self.y += self.rowh
            self.x, self.rowh = 0, 0

    def add(self, p, w, h):
        if self.x + w > 24:
            self._nl()
        p["id"] = self._nid()
        p["datasource"] = DS
        p["gridPos"] = {"h": h, "w": w, "x": self.x, "y": self.y}
        self.panels.append(p)
        self.x += w
        self.rowh = max(self.rowh, h)


def t(expr, legend="", instant=False, ref="A"):
    d = {"datasource": DS, "expr": expr, "legendFormat": legend or "__auto", "refId": ref}
    if instant:
        d.update(instant=True, range=False)
    return d


def thresholds(steps):
    return {"mode": "absolute", "steps": [{"color": c, "value": v} for v, c in steps]}


def stat(title, targets, unit="none", steps=None, desc="", mappings=None, text_mode="value", decimals=None):
    d = {"unit": unit, "thresholds": thresholds(steps or [(None, "green")]), "color": {"mode": "thresholds"}}
    if mappings:
        d["mappings"] = mappings
    if decimals is not None:
        d["decimals"] = decimals
    return {"type": "stat", "title": title, "description": desc, "targets": targets,
            "fieldConfig": {"defaults": d, "overrides": []},
            "options": {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                        "colorMode": "background", "graphMode": "none", "textMode": text_mode,
                        "justifyMode": "center"}}


def ts(title, targets, unit="short", stack=False, bars=False, desc="", steps=None, min0=True,
       overrides=None, interpolation="linear"):
    custom = {"drawStyle": "bars" if bars else "line", "lineWidth": 1, "fillOpacity": 60 if bars else 10,
              "showPoints": "never", "spanNulls": False, "lineInterpolation": interpolation,
              "stacking": {"mode": "normal" if stack else "none", "group": "A"}}
    d = {"unit": unit, "custom": custom, "color": {"mode": "palette-classic"}}
    if min0:
        d["min"] = 0
    if steps:
        d["thresholds"] = thresholds(steps)
        custom["thresholdsStyle"] = {"mode": "line+area"}
    return {"type": "timeseries", "title": title, "description": desc, "targets": targets,
            "fieldConfig": {"defaults": d, "overrides": overrides or []},
            "options": {"legend": {"displayMode": "table", "placement": "bottom", "calcs": ["lastNotNull", "max"]},
                        "tooltip": {"mode": "multi", "sort": "desc"}}}


def color_override(name, color):
    return {"matcher": {"id": "byName", "options": name},
            "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": color}}]}


def regex_color_override(pattern, color):
    return {"matcher": {"id": "byRegexp", "options": pattern},
            "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": color}}]}


READY_MAP = [{"type": "value", "options": {"0": {"text": "not ready", "color": "red"},
                                           "1": {"text": "ready", "color": "green"}}},
             {"type": "special", "options": {"match": "null", "result": {"text": "no scrape", "color": "red"}}}]

NO_CYCLE_MAP = [{"type": "special", "options": {"match": "null",
                                                "result": {"text": "none yet", "color": "orange"}}}]


def own(expr, ns, sel):
    """Restricts an unprefixed metric to this service's targets.

    process_* and go_* carry no service prefix: every Go exporter and
    Prometheus itself export them. A filter on instance alone matches all of
    them once the variable is "All" (allValue .*), or whenever the scrape
    config relabels instance to the host name, and the CPU panel then shows
    whatever else runs there. Joining on <service>_build_info by job and
    instance keeps exactly the targets that are this service.
    """
    return f'({expr}) and on (job, instance) {ns}_build_info{sel}'


def runtime(b, ns, sel):
    """Process and Go runtime row."""
    b.row("Process and runtime")
    b.add(ts("CPU", [t(own('rate(process_cpu_seconds_total[$__rate_interval])', ns, sel), "{{instance}}")],
             "percentunit",
             desc="1.0 = one full core. A scan dials every host in the CIDR, so a busy cycle shows here. "
                  "Values above the number of cores mean several processes share one (job, instance) — "
                  "see contrib/grafana/README.md."), 6, 7)
    b.add(ts("Memory", [t(own('process_resident_memory_bytes', ns, sel), "RSS {{instance}}"),
                        t(own('go_memstats_heap_inuse_bytes', ns, sel), "heap {{instance}}", ref="B")], "bytes",
             desc="A heap that only grows over days is a leak — the kind of failure noticed only after weeks."), 6, 7)
    b.add(ts("Goroutines", [t(own('go_goroutines', ns, sel), "{{instance}}")],
             desc="Rises with max_workers + validate_workers during a cycle and must fall back afterwards. "
                  "A baseline that keeps climbing between cycles means workers are not released."), 6, 7)
    b.add(ts("Open file descriptors", [t(own('process_open_fds', ns, sel), "open {{instance}}"),
                                       t(own('process_max_fds', ns, sel), "limit {{instance}}", ref="B")],
             desc="Every dial worker holds a socket. With max_workers close to the limit, dials fail with "
                  "'too many open files' and look like closed ports."), 6, 7)


def dashboard(uid, title, desc, ns, b, tags):
    return {
        "uid": uid, "title": title, "description": desc, "tags": tags,
        "schemaVersion": 39, "version": 1, "editable": True, "graphTooltip": 1,
        "time": {"from": "now-24h", "to": "now"}, "refresh": "1m", "timezone": "browser",
        "annotations": {"list": [{"builtIn": 1, "datasource": {"type": "grafana", "uid": "-- Grafana --"},
                                  "enable": True, "hide": True, "iconColor": "rgba(0, 211, 255, 1)",
                                  "name": "Annotations & Alerts", "type": "dashboard"}]},
        "templating": {"list": [
            {"name": "datasource", "label": "Data source", "type": "datasource", "query": "prometheus",
             "current": {}, "hide": 0, "refresh": 1},
            {"name": "instance", "label": "Instance", "type": "query", "datasource": DS,
             "query": {"query": f"label_values({ns}_build_info, instance)", "refId": "instance"},
             "definition": f"label_values({ns}_build_info, instance)",
             "includeAll": True, "multi": True, "allValue": ".*", "current": {}, "refresh": 2, "sort": 1,
             "hide": 0},
        ]},
        "panels": b.panels,
    }


def write(name, d):
    with open(os.path.join(out_dir, name), "w", encoding="utf-8") as f:
        json.dump(d, f, indent=2, ensure_ascii=False)
        f.write("\n")


# --- promscout ---------------------------------------------------------------
ns, sel = "promscout", '{instance=~"$instance"}'
b = B()

b.row("Overview")
b.add(stat("Version", [t(f'{ns}_build_info{sel}', "{{version}} ({{revision}})", instant=True)],
           text_mode="name", steps=[(None, "blue")],
           desc="After a rollout: does every instance run the same build?"), 4, 4)
b.add(stat("Ready", [t(f'min({ns}_last_discovery_timestamp{sel} > bool 0)', instant=True)],
           mappings=READY_MAP,
           desc="Mirrors /-/ready: ready once the first discovery cycle has completed. Until then the "
                "HTTP-SD endpoint serves only the warm-start cache, or nothing."), 3, 4)
b.add(stat("Uptime", [t(f'time() - max({own("process_start_time_seconds", ns, sel)})', instant=True)], "s",
           desc="A short uptime that keeps resetting is a crash loop — for example a configuration the "
                "service refuses at startup."), 3, 4)
b.add(stat("Last completed cycle", [t(f'time() - min({ns}_last_discovery_timestamp{sel} > 0)', instant=True)],
           "s", mappings=NO_CYCLE_MAP,
           steps=[(None, "green"), (AGE_WARN_S, "orange"), (AGE_CRIT_S, "red")],
           desc=f"Age of the last completed cycle of the most stale instance. Thresholds assume interval {INTERVAL_S}s (config.example): "
                f"orange from {AGE_WARN_S // 60} min, red from {AGE_CRIT_S // 60} min. Alert on the age, not "
                "on the run counter: runs_total also counts cycles cancelled by a shutdown, and a cycle "
                "that hangs never completes."), 4, 4)
b.add(stat("Valid targets", [t(f'sum({ns}_targets_valid_total{sel})', instant=True)],
           steps=[(None, "orange"), (1, "green")],
           desc="Targets handed to Prometheus by the last completed cycle. Zero after a completed cycle "
                "means nothing on the network answered with Prometheus metrics."), 3, 4)
b.add(stat("Open ports", [t(f'sum({ns}_targets_discovered_total{sel})', instant=True)],
           steps=[(None, "blue")],
           desc="Open host:port combinations found by the last completed cycle, before validation. "
                "The gap to 'Valid targets' are services that are not Prometheus endpoints."), 3, 4)
b.add(stat("Skipped cycles", [t(f'sum(increase({ns}_discovery_skipped_total{sel}[$__range]))', instant=True)],
           steps=[(None, "green"), (1, "orange")], decimals=0,
           desc="Ticks dropped in the selected range because the previous cycle was still running: a scan "
                "takes longer than `interval`. Harmless by design (no overlapping scans), but targets are "
                "then refreshed less often than configured. Shorten dial_timeout, raise max_workers, or "
                "raise interval."), 4, 4)

b.row("Discovery")
b.add(ts("Targets", [t(f'{ns}_targets_discovered_total{sel}', "open ports {{instance}}"),
                     t(f'{ns}_targets_valid_total{sel}', "valid {{instance}}", ref="B")],
         interpolation="stepAfter",
         overrides=[regex_color_override("^valid .*", "green"), regex_color_override("^open ports .*", "blue")],
         desc="Updated only when a cycle completes; a cancelled cycle keeps the previous values. "
              "A sudden drop of valid targets with stable open ports points at the exporters "
              "(or the network path to them), not at PromScout."), 12, 8)
b.add(ts("Cycle duration", [
    t(f'sum by (instance) (increase({ns}_discovery_duration_seconds_sum{sel}[{DURATION_WINDOW}])) / '
      f'sum by (instance) (increase({ns}_discovery_duration_seconds_count{sel}[{DURATION_WINDOW}]))',
      "avg {{instance}}"),
    t(f'histogram_quantile(0.95, sum by (le, instance) '
      f'(rate({ns}_discovery_duration_seconds_bucket{sel}[{DURATION_WINDOW}])))', "p95 {{instance}}", ref="B")],
    "s", steps=[(None, "green"), (INTERVAL_S, "orange")],
    desc=f"Completed cycles only, over a {DURATION_WINDOW} window. Above the interval line "
         f"({INTERVAL_S}s) ticks get skipped. Buckets end at 1024s; beyond that the p95 is capped, "
         "the average stays exact."), 12, 8)
b.add(ts("Cycles", [t(f'sum by (instance) (increase({ns}_discovery_runs_total{sel}[$__rate_interval]))',
                      "started {{instance}}"),
                    t(f'sum by (instance) (increase({ns}_discovery_skipped_total{sel}[$__rate_interval]))',
                      "skipped {{instance}}", ref="B")],
         bars=True, stack=True,
         overrides=[regex_color_override("^started .*", "green"), regex_color_override("^skipped .*", "orange")],
         desc="Started vs. skipped ticks. 'Started' includes cycles that a shutdown cancelled; those "
              "publish nothing."), 12, 7)
b.add(ts("Cycle running", [t(f'{ns}_active_discovery{sel}', "{{instance}}")],
         interpolation="stepAfter",
         desc="1 while a cycle runs. Permanently 1 means the cycle never ends — check the "
              "'Last completed cycle' age next to it."), 12, 7)

runtime(b, ns, sel)

write("promscout.json", dashboard(
    "promscout", "PromScout",
    "Network discovery cycles, discovered targets and process health of PromScout. "
    "Generated by contrib/grafana/generate.py — do not edit the JSON.",
    ns, b, ["promscout", "prometheus", "service-discovery"]))
