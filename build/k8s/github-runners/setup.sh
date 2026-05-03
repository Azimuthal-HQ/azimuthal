#!/usr/bin/env bash
# setup.sh — register the Azimuthal GitHub Actions runner on your k8s cluster
#
# Prerequisites:
#   - kubectl configured for grimmkube.lan
#   - helm 3.x installed
#   - A GitHub Fine-Grained PAT (see instructions below)
#
# HOW TO CREATE THE PAT (org-level scope, serves both repos):
#   1. Go to: https://github.com/settings/tokens?type=beta
#   2. Click "Generate new token"
#   3. Token name: azimuthal-arc-runner
#   4. Resource owner: Azimuthal-HQ  (switch from personal to org)
#   5. Repository access: Public Repositories AND Private Repositories
#      (or "All repositories" — the runner is registered at the ORG level
#      so the same pool serves azimuthal AND azimuthal-private)
#   6. Permissions → Organization permissions:
#        - Self-hosted runners: Read and write   ← REQUIRED for org runners
#   7. Permissions → Repository permissions:
#        - Actions: Read and write
#        - Administration: Read (so the listener can read repo metadata)
#   8. Generate token → copy it
#
# Then run:
#   GITHUB_PAT=github_pat_xxxx bash build/k8s/github-runners/setup.sh

set -euo pipefail

if [[ -z "${GITHUB_PAT:-}" ]]; then
  echo "ERROR: Set GITHUB_PAT before running this script."
  echo "  export GITHUB_PAT=github_pat_xxxx"
  echo "  bash build/k8s/github-runners/setup.sh"
  exit 1
fi

NAMESPACE="github-runners"
SECRET_NAME="azimuthal-runner-secret"

echo "=== Creating/updating runner PAT secret ==="
kubectl create secret generic "$SECRET_NAME" \
  --namespace "$NAMESPACE" \
  --from-literal=github_token="$GITHUB_PAT" \
  --dry-run=client -o yaml | kubectl apply -f -

echo ""
echo "=== Installing runner scale set ==="
helm upgrade --install azimuthal-runners \
  --namespace "$NAMESPACE" \
  --version "0.9.3" \
  --values "$(dirname "$0")/runner-scale-set.yaml" \
  oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set

echo ""
echo "=== Patching ARC controller to bypass cluster DNS ==="
# The controller chart doesn't expose dnsConfig, but the controller itself
# needs to resolve api.github.com to mint runner registration tokens. On
# k3s with systemd-resolved, cluster DNS frequently breaks (host resolv.conf
# only has 127.0.0.53, which doesn't work in pod netns), so the controller
# stalls forever waiting for DNS. Patching it to use public resolvers
# directly. Idempotent — safe to re-run.
kubectl patch deployment arc-gha-rs-controller -n "$NAMESPACE" --type=strategic -p '{
  "spec": {
    "template": {
      "spec": {
        "dnsPolicy": "None",
        "dnsConfig": {
          "nameservers": ["1.1.1.1", "8.8.8.8", "9.9.9.9"],
          "options": [
            {"name": "ndots", "value": "1"},
            {"name": "timeout", "value": "3"},
            {"name": "attempts", "value": "3"}
          ]
        }
      }
    }
  }
}' 2>/dev/null || echo "  (controller deployment not present yet — will be patched on next setup.sh run)"

echo ""
echo "=== Applying coredns-custom ConfigMap (per-zone DNS overrides) ==="
# Survives k3s addon-manager reconciliation because it's a USER ConfigMap.
# The default Corefile has `import /etc/coredns/custom/*.server` which picks
# up these per-zone forwarder blocks for github.com, ghcr.io, docker, etc.
kubectl apply -f "$(dirname "$0")/coredns-custom.yaml"

echo ""
echo "=== Done ==="
echo "Watch runners come up:"
echo "  kubectl get pods -n $NAMESPACE -w"
echo ""
echo "Once a runner pod appears, check GitHub:"
echo "  https://github.com/organizations/Azimuthal-HQ/settings/actions/runner-groups"
