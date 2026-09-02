#!/bin/sh
set -eu

if command -v go >/dev/null 2>&1; then
  go version
  exit 0
fi

version="1.27.1"
case "$(uname -m)" in
  x86_64|amd64)
    arch="amd64"
    checksum="63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445"
    ;;
  aarch64|arm64)
    arch="arm64"
    checksum="3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec"
    ;;
  *)
    echo "unsupported architecture for Go bootstrap: $(uname -m)" >&2
    exit 1
    ;;
esac

install_root="/opt/agent-harness/go-${version}"
if [ ! -x "${install_root}/bin/go" ]; then
  temporary="$(mktemp -d)"
  trap 'rm -rf "$temporary"' EXIT HUP INT TERM
  archive="${temporary}/go.tar.gz"
  staging="${temporary}/go"
  mkdir -p "$staging"
  curl --fail --location --proto '=https' --tlsv1.2 \
    "https://go.dev/dl/go${version}.linux-${arch}.tar.gz" \
    --output "$archive"
  printf '%s  %s\n' "$checksum" "$archive" | sha256sum --check --status
  tar --extract --gzip --file "$archive" --directory "$staging" --strip-components=1
  mkdir -p /opt/agent-harness
  rm -rf /opt/agent-harness/go-1.27.1
  mv "$staging" "$install_root"
fi

ln -sfn "${install_root}/bin/go" /usr/local/bin/go
ln -sfn "${install_root}/bin/gofmt" /usr/local/bin/gofmt
go version
