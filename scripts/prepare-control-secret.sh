#!/usr/bin/env bash
# Prepare the Secret referenced by deployments/catalog.yaml without printing keys.
set -euo pipefail

namespace="${1:-olixops}"
project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
key_dir="${project_root}/.local"
key_file="${key_dir}/control-api-key"

existing="$(kubectl -n "${namespace}" get secret olixops-control --ignore-not-found -o name --request-timeout=15s)"
if [[ -n "${existing}" ]]; then
  printf 'Secret %s/olixops-control already exists; kept unchanged.\n' "${namespace}"
  exit 0
fi

umask 077
mkdir -p "${key_dir}"
if [[ ! -e "${key_file}" ]]; then
  openssl rand -hex 32 > "${key_file}"
fi
if [[ ! -s "${key_file}" || ! -f "${key_file}" || -L "${key_file}" ]]; then
  printf 'Expected a nonempty regular key file at %s\n' "${key_file}" >&2
  exit 1
fi
chmod 600 "${key_file}"
kubectl -n "${namespace}" create secret generic olixops-control \
  --from-file="api-key=${key_file}" --request-timeout=15s
printf 'Key file: %s (mode 0600, ignored by Git; contents not printed).\n' "${key_file}"
