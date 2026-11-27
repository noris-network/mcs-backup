#!/bin/sh
# Checks the examples and the docs: the shell scripts, the Kubernetes
# manifests, the Prometheus rules (with a unit test of the alerts), the
# property lists and the links between the Markdown files.
#
#   scripts/docs-check.sh
#
# The tools run as containers in fixed versions. DOCKER names the command
# (default docker, for example DOCKER="sudo -n docker"). kubeconform loads the
# schemas from the internet.
set -eu
cd "$(dirname "$0")/.."
DOCKER=${DOCKER:-docker}

# run starts a tool on the working tree, as the calling user
run() { $DOCKER run --rm -u "$(id -u):$(id -g)" -v "$PWD:/w" -w /w "$@"; }

echo "== shellcheck"
run --entrypoint shellcheck koalaman/shellcheck:v0.10.0 docs/examples/*/*.sh scripts/*.sh

echo "== kubeconform"
run ghcr.io/yannh/kubeconform:v0.6.7 -strict -summary \
	-schema-location default \
	-schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
	docs/examples/kubernetes/*.yaml \
	docs/examples/monitoring/servicemonitor.yaml docs/examples/monitoring/prometheusrule.yaml

echo "== promtool"
# promtool reads plain rule files: the rules are taken out of the PrometheusRule
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
run mikefarah/yq:4.44.3 '.spec' docs/examples/monitoring/prometheusrule.yaml >"$tmp/rules.yaml"
cp docs/examples/monitoring/prometheusrule_test.yaml "$tmp/"
promtool() { $DOCKER run --rm -u "$(id -u):$(id -g)" -v "$tmp:/t" -w /t --entrypoint promtool prom/prometheus:v3.5.0 "$@"; }
promtool check rules rules.yaml
promtool test rules prometheusrule_test.yaml

echo "== property lists"
python3 -I -c 'import plistlib, sys
for f in sys.argv[1:]:
    with open(f, "rb") as fh:
        plistlib.load(fh)
    print(f, "ok")' docs/examples/launchd/*.plist

echo "== links"
run lycheeverse/lychee:0.15.1 --offline --include-fragments --no-progress README.md 'docs/**/*.md'
