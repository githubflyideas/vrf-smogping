# Routing regression tests

The product remains a lightweight SmokePing-style monitor. These tests cover only
optional per-target VRF/source-address behavior, compatibility and persistence.

## Unit and integration tests

```sh
go test -count=1 ./...
go vet ./...
# With a native C toolchain:
go test -race -count=1 ./...
```

A normal test run skips the opt-in Linux VRF fixture and scoped-hostname fixture.
Windows runs exercise TCP and the web/backend workflow; they do not establish
Linux ICMP or VRF support. CI supplies `jp-multi.test` with ordered loopback
addresses on its disposable hosted runner so scoped TCP multi-address fallback
runs rather than skips. Linux CI also exercises descriptor exhaustion in an
isolated subprocess and symbolic-link retention protection.

## Real Linux VRF acceptance

Use a disposable Linux host with iproute2, a kernel with VRF support and permission
to create a network namespace. The application itself does not run these commands
or modify networking.

```sh
sudo modprobe vrf
go test -c -o /absolute/disposable/path/vrf-ping.test
sudo env JSON_PING_DISPOSABLE_NETNS=1 unshare --net \
  sh scripts/test-vrf.sh /absolute/disposable/path/vrf-ping.test
```

The script creates its devices only inside the new namespace, cleans up devices
it created, and runs these acceptance checks:

- ICMP and TCP success in an actual VRF, including exact explicit source selection.
- VRF-only probing with automatic source selection.
- The same destination in another VRF does not fall back to the reachable context.
- A source assigned only to another VRF is rejected, not counted as packet loss.
- An ordinary interface cannot be passed off as a VRF.
- The actual DNS socket is bound to the requested VRF.
- A complete DNS lookup follows a truncated UDP response with TCP, retaining the
  source and VRF socket bindings for both transports. The test redirects only the
  nameserver address to its own fixture; it does not change the system resolver.

Do not invoke the fixture in a production namespace. `JSON_PING_DISPOSABLE_NETNS`
is an explicit safety acknowledgement, not automatic isolation: `unshare --net`
is what provides isolation.

## Browser acceptance

```sh
npm ci --ignore-scripts
npx playwright install chromium
go build -o /absolute/disposable/path/vrf-ping .
JSON_PING_BINARY=/absolute/disposable/path/vrf-ping \
JSON_PING_TEST_WORKDIR=/absolute/disposable/path npm test
```

Use an existing writable disposable directory for `JSON_PING_TEST_WORKDIR`.
The real-server test creates its own child directory, login credentials, loopback
TCP listener and loopback Web listener. It preserves the evidence directory for
inspection and stops its owned processes/listeners. It never probes public targets.
`JSON_PING_BINARY` must be supplied to run the real-server test rather than skip it.
`BROWSER_CHANNEL=msedge` can select an installed Edge browser on Windows;
`PLAYWRIGHT_MODULE` optionally selects an existing Playwright installation.

The other browser cases load the production HTML/JavaScript with explicitly mocked
APIs to exercise delayed requests and UI races. These are separate from real-server
acceptance, which checks login, saved configuration, active reload, restart and
clearing both fields back to automatic routing.

## Published-package acceptance

After publishing, dispatch the `test` workflow with `release_tag=v3.3.4` (or the
version under test). It checks out the tag, downloads the actual Linux amd64
release archive and `SHA256SUMS`, verifies the checksum and embedded version,
and runs the browser/backend tests against the extracted binary. It does not
silently rebuild when a release download or checksum fails. Ordinary push/PR runs use GoReleaser snapshot archives named `vrf-ping-*`,
verify their checksums and exercise the extracted Linux amd64 executable. Successful
runs upload the candidate archives and checksums as `vrf-ping-candidate`.

The legacy `JSON_PING_*` test environment variable names remain supported for
existing runners; they are test-harness controls, not product names or user
configuration fields. Published-package acceptance supports both the old
`json-ping-*` archives and new `vrf-ping-*` archives.

## Evidence boundaries

Local verification has exercised a disposable Alpine Linux VM and Windows/Edge.
This is not native VyOS image, systemd lifecycle or image-upgrade acceptance.
The repository CI workflow registers the tests above; editing it does not mean
that a remote GitHub Actions run has passed.
