#!/bin/sh
# Prints the digest an AIR service stores for a raw API key: sha256(salt + key),
# hex. The classifier, RAG service, tools gateway and orchestrator all use this
# form. The raw key goes to the caller; only the digest goes in the service's
# key records.
#
#   gke/scripts/hash-key.sh <salt> <raw-key>
#
# Both arguments end up in your shell history: for a production key, read them
# from files instead:  gke/scripts/hash-key.sh "$(cat salt)" "$(cat key)"
set -eu
salt=${1:?usage: hash-key.sh <salt> <raw-key>}
key=${2:?usage: hash-key.sh <salt> <raw-key>}
if command -v sha256sum >/dev/null 2>&1; then
  printf '%s%s' "$salt" "$key" | sha256sum | cut -d' ' -f1
else
  printf '%s%s' "$salt" "$key" | shasum -a 256 | cut -d' ' -f1
fi
