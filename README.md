# vrf-ping

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/brand/vrf-ping-logo-dark.svg">
  <img src="docs/brand/vrf-ping-logo-light.svg" alt="vrf-ping" width="420">
</picture>

A lightweight SmokePing alternative with optional per-target VRF and source-address binding. ICMP/TCP probes, latency distributions, packet loss and history. Single Go binary, plain JSONL storage.

![vrf-ping](docs/images/vrf-ping-graph.png)

*Actual UI from a native development build, measuring six TCP targets in an isolated loopback lab.*

## Install

Linux amd64; other builds are available in [Releases](https://github.com/githubflyideas/vrf-smogping/releases).

```bash
set -e
curl -fLO https://github.com/githubflyideas/vrf-smogping/releases/download/v3.3.4/vrf-ping-linux-amd64.tar.gz
curl -fLO https://github.com/githubflyideas/vrf-smogping/releases/download/v3.3.4/SHA256SUMS
sha256sum --ignore-missing -c SHA256SUMS
tar -xzf vrf-ping-linux-amd64.tar.gz
./vrf-ping --listen 127.0.0.1:8517
```

Build from source: `go build -o vrf-ping .`. Run the binary directly on the host. **Docker is not supported.**

## Targets

Paths are relative to the working directory. Changes reload automatically, normally within three seconds.

A line that does not parse never takes the service down. At startup it is skipped, logged as a `WARNING` and shown in the web UI; the other targets start. While running, a list with a bad line is refused as a whole and the last good set keeps probing — so no other edit to the lists applies either until the line is fixed. The web UI shows that refusal (and `GET /api/health` reports it) for as long as it lasts.

**`targets/ping.list`**

```text
1.1.1.1 ens18
1.1.1.1 eth0-zone1 vrf=zone1
8.8.8.8 ens18-source source=192.168.10.2
8.8.8.8 ens18-base vrf=base_vrf0 source=192.168.10.2
```

**`targets/tcp.list`**

```text
1.1.1.1:443 eth0-zone1-https vrf=zone1 source=192.168.10.2
```

Format: `target[:port] name [vrf=NAME] [source=LOCAL_IP]`.

- Names must be unique. `ens18` and `eth0-zone1` are labels, not interface bindings.
- Both options are independent and optional. Omit them for the default routing domain and automatic source selection. Replace example source IPs with local addresses valid in the selected routing domain.
- VRF requires Linux, an existing VRF and sufficient socket permissions. The program does not configure networking or silently fall back on binding failure. ICMP is currently IPv4-only.

## Run and Web UI

```bash
./vrf-ping --listen 0.0.0.0:8517 user=admin passwd='YOUR_PASSWORD'
```

Use `--localhost` for loopback-only access; `--help` lists options. Restrict access to trusted management networks.

After 5 wrong passwords from one client address, that address is locked out for 30 s, doubling per further miss up to 15 min; even the right password is refused (HTTP 429) until the lock runs out. The session cookie is `vrfping_session` (it was `pingping_session`, which clashed with a pingping console on the same host); signing in again after the upgrade is expected.

Authenticated users can edit an existing target under **Probe routing**. Web writes are disabled when login is not configured.

Web settings are stored in `targets/routing-options.json` and **override list-file routing options, including empty values**. Remove a target's JSON entry to restore list-file control. Lists use `source=`; JSON uses `source_address`. Failed reloads retain the active configuration and retry.

## Data and upgrades

`data/` contains daily JSONL files with a default retention of 300 days. Different VRF/source contexts have separate histories. Clearing both options returns to the target's default-context history.

Back up the complete `targets/` and `data/` directories, stop the old process, replace the binary and retain the working directory. Configuration and data remain compatible with json-ping v3.2.0; no format migration is required.

[Testing](docs/testing-routing.md) · [Apache-2.0](LICENSE). Linux VRF behavior is tested; native VyOS images are not certified.
