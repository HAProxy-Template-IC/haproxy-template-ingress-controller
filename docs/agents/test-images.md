# Test image selection

Tests and benchmarks use the HAProxy patch selected by the checked-out Helm
chart. `HAPROXY_VERSION` selects another chart-supported series for matrix
coverage; omitting it selects `charts/haptic/values.yaml`'s `haproxyVersion`.
A series without a chart patch fails before a container is built.

`tests/kindutil.LoadChartImages` evaluates the chart's image helpers and reads
its Varnish and Valkey image values. Integration, agent, and e2e suites share
that resolver. Shared and per-test e2e HAProxy backend fixtures use the same
selection. Shared manifests must contain exactly one matching container;
missing or ambiguous matches fail before any fixture is applied.

`make docker-build` and the multi-platform build targets pass the chart-selected
image through `scripts/chart-haproxy-image.sh`. CI snapshot and release builds
use the digest-pinned image variables checked against the chart before CI
starts its image builds. `make goreleaser-snapshot` supplies its image variables
from the local chart.

E2e, conformance, lifecycle, and Gateway API benchmarks use the chart's SPOA
image. Snapshot and release publication build the hub from `versions-spoa.env`
at the published commit, so CI tests that bundle, not the last release's: every
cluster job runs `.github/scripts/spoa-test-image.sh`, which points the chart at
`haptic-spoa:test` and builds it with `make spoa-hub-image` alongside the
controller image. E2e and lifecycle suites load a local chart image into Kind
and pull a published one. Upgrade baselines install the released chart and keep
its released SPOA image. `make spoa-prep` keeps the plugin release files in
`.cache/spoa-plugins` and verifies every file on every run, cached or not. CI
restores that directory in each hub-building job; only the `spoa` job saves it.
To run the commit's bundle locally, set the same chart
values and build it with `make spoa-hub-image SPOA_DEV_IMAGE=haptic-spoa:test`.
The benchmark compares every running hub and validator's executable and plugin
hashes with the chart image before and after measurement.
E2e startup rejects container images that differ from the chart, including
overrides introduced by a test values profile.

`make test-spoa-reload` uses the chart image and rejects a conflicting
`SPOA_RELOAD_IMAGE`. The source-bundle
smoke job tests its newly built artifact separately; that result does not
establish compatibility of the bundle currently selected by the chart.

Do not add an independent image version to a test or benchmark. Add a regression
that changes the chart value and verifies that the consumer follows it.
