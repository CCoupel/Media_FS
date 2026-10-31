#!/usr/bin/env bash
# scripts/ci.sh — script CI unique (commands.ci) : appelé tel quel par
#   .github/workflows/ci-branch.yml (pré-validation), .github/workflows/release.yml (job verify),
#   et en local par qa / deploy. Aucune étape n'existe uniquement en CI ou uniquement en local.
#
# Les paquets qui importent cgofuse (internal/vfs, cmd/mediafs) exigent les en-têtes WinFSP / libfuse :
# ils sont compilés par les jobs de build de release.yml, pas ici. Tout le reste est vérifié sans CGO.
#
# Variables optionnelles :
#   SKIP_AUDIT=1   saute govulncheck (poste hors ligne)
#   AUDIT_STRICT=1 rend govulncheck bloquant (non bloquant par défaut tant que go.mod reste < 1.25.7)
set -euo pipefail

cd "$(dirname "$0")/.."

GO=${GO:-go}
command -v "$GO" >/dev/null 2>&1 || GO=/usr/local/go/bin/go
export PATH="$(dirname "$(command -v "$GO")"):$PATH"   # govulncheck invoque `go` lui-même
export CGO_ENABLED=0

step() { printf '\n=== %s ===\n' "$1"; }

step "Version : aucune chaîne vX.Y.Z codée en dur hors internal/version/version.go"
bad=$(grep -rn --include="*.go" 'Version\s*=\s*"v[0-9]' . | grep -v 'internal/version/version.go' || true)
if [ -n "$bad" ]; then
  echo "Hardcoded version string found outside internal/version/version.go:"
  echo "$bad"
  exit 1
fi
echo "ok"

PKGS=$("$GO" list ./... | grep -v -E 'github\.com/CCoupel/Media_FS/(internal/vfs|cmd/mediafs)$')

step "go build (paquets sans CGO)"
"$GO" build $PKGS

step "go vet"
"$GO" vet $PKGS

step "go test"
"$GO" test $PKGS

if [ "${SKIP_AUDIT:-0}" != "1" ]; then
  step "govulncheck"
  GOVULNCHECK=$(command -v govulncheck || echo "$("$GO" env GOPATH)/bin/govulncheck")
  [ -x "$GOVULNCHECK" ] || "$GO" install golang.org/x/vuln/cmd/govulncheck@v1.9.0
  if ! "$GOVULNCHECK" $PKGS; then
    if [ "${AUDIT_STRICT:-0}" = "1" ]; then exit 1; fi
    echo "::warning::govulncheck a trouvé des vulnérabilités (non bloquant — AUDIT_STRICT=1 pour bloquer)"
  fi
fi

printf '\nCI OK\n'
