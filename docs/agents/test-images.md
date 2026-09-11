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

E2e, conformance, and Gateway API benchmarks use the chart's SPOA image, not a
bundle rebuilt from `versions-spoa.env`. The benchmark compares every running
hub and validator's executable and plugin hashes with that image before and
after measurement. Changing source pins does not change the chart's delivered
bundle until a matching image is published and selected by the chart.
E2e startup rejects container images that differ from the chart, including
overrides introduced by a test values profile.

`make test-spoa-reload` uses the chart image and rejects a conflicting
`SPOA_RELOAD_IMAGE`. The source-bundle
smoke job tests its newly built artifact separately; that result does not
establish compatibility of the bundle currently selected by the chart.

Do not add an independent image version to a test or benchmark. Add a regression
that changes the chart value and verifies that the consumer follows it.
