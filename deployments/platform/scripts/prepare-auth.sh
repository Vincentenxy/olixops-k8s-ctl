#!/usr/bin/env bash
# Creates only the controller namespace and its platform JWT public-key Secret.
set -euo pipefail
if [[ $# -ne 1 || ! -f "$1" ]]; then
  echo "Usage: $0 /absolute/path/platform-public.pem" >&2
  exit 2
fi
command -v kubectl >/dev/null
command -v openssl >/dev/null
openssl rsa -pubin -in "$1" -noout >/dev/null 2>&1 || {
  echo "Expected an RSA public key PEM; do not supply a private key." >&2
  exit 2
}
kubectl create namespace olixops-platform --dry-run=client -o yaml | kubectl apply -f -
kubectl -n olixops-platform create secret generic olixops-k8s-ctl-platform-auth \
  --from-file="platform-public.pem=$1" --dry-run=client -o yaml | kubectl apply -f -
echo "Public-key Secret prepared. An existing controller must be restarted after key changes."
