#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" != "1" ]]; then
  echo "usage: $0 <output-directory>" >&2
  exit 2
fi

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

require_cmd openssl

OUTPUT_DIR="$1"
EXISTING=""
if [[ -d "$OUTPUT_DIR" ]]; then
  EXISTING="$(find "$OUTPUT_DIR" -mindepth 1 -print -quit)"
fi
if [[ -n "$EXISTING" && "${ZCOURIER_EDGE_CERT_FORCE:-0}" != "1" ]]; then
  echo "output directory is not empty: $OUTPUT_DIR" >&2
  echo "set ZCOURIER_EDGE_CERT_FORCE=1 only for a disposable test directory" >&2
  exit 1
fi

mkdir -p "$OUTPUT_DIR/issuer" "$OUTPUT_DIR/server" "$OUTPUT_DIR/client"
chmod 700 "$OUTPUT_DIR/issuer" "$OUTPUT_DIR/server" "$OUTPUT_DIR/client"

openssl req -x509 -new -newkey rsa:2048 -nodes -sha256 -days 7 \
  -subj "/CN=Z-Courier Edge Test Server CA" \
  -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" \
  -keyout "$OUTPUT_DIR/issuer/server-ca.key" \
  -out "$OUTPUT_DIR/issuer/server-ca.crt"

openssl req -new -newkey rsa:2048 -nodes -sha256 \
  -subj "/CN=edge-proxy.test" \
  -keyout "$OUTPUT_DIR/server/tls.key" \
  -out "$OUTPUT_DIR/server/tls.csr"
openssl x509 -req -sha256 -days 7 \
  -in "$OUTPUT_DIR/server/tls.csr" \
  -CA "$OUTPUT_DIR/issuer/server-ca.crt" \
  -CAkey "$OUTPUT_DIR/issuer/server-ca.key" \
  -CAcreateserial \
  -extfile <(printf '%s\n' \
    'basicConstraints=critical,CA:FALSE' \
    'keyUsage=critical,digitalSignature,keyEncipherment' \
    'extendedKeyUsage=serverAuth' \
    'subjectAltName=DNS:edge-proxy.test,DNS:console.example.test,DNS:localhost,IP:127.0.0.1') \
  -out "$OUTPUT_DIR/server/tls.crt"

openssl req -x509 -new -newkey rsa:2048 -nodes -sha256 -days 7 \
  -subj "/CN=Z-Courier Edge Test Client CA" \
  -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" \
  -keyout "$OUTPUT_DIR/issuer/client-ca.key" \
  -out "$OUTPUT_DIR/issuer/client-ca.crt"

openssl req -new -newkey rsa:2048 -nodes -sha256 \
  -subj "/CN=z-courier-edge-smoke-client" \
  -keyout "$OUTPUT_DIR/client/tls.key" \
  -out "$OUTPUT_DIR/client/tls.csr"
openssl x509 -req -sha256 -days 7 \
  -in "$OUTPUT_DIR/client/tls.csr" \
  -CA "$OUTPUT_DIR/issuer/client-ca.crt" \
  -CAkey "$OUTPUT_DIR/issuer/client-ca.key" \
  -CAcreateserial \
  -extfile <(printf '%s\n' \
    'basicConstraints=critical,CA:FALSE' \
    'keyUsage=critical,digitalSignature,keyEncipherment' \
    'extendedKeyUsage=clientAuth') \
  -out "$OUTPUT_DIR/client/tls.crt"

cp "$OUTPUT_DIR/issuer/server-ca.crt" "$OUTPUT_DIR/client/ca.crt"
cp "$OUTPUT_DIR/issuer/client-ca.crt" "$OUTPUT_DIR/server/client-ca.crt"
rm -f \
  "$OUTPUT_DIR/server/tls.csr" \
  "$OUTPUT_DIR/client/tls.csr" \
  "$OUTPUT_DIR/issuer/server-ca.srl" \
  "$OUTPUT_DIR/issuer/client-ca.srl"
chmod 600 \
  "$OUTPUT_DIR/issuer/server-ca.key" \
  "$OUTPUT_DIR/issuer/client-ca.key" \
  "$OUTPUT_DIR/server/tls.key" \
  "$OUTPUT_DIR/client/tls.key"
chmod 644 \
  "$OUTPUT_DIR/issuer/server-ca.crt" \
  "$OUTPUT_DIR/issuer/client-ca.crt" \
  "$OUTPUT_DIR/server/tls.crt" \
  "$OUTPUT_DIR/server/client-ca.crt" \
  "$OUTPUT_DIR/client/ca.crt" \
  "$OUTPUT_DIR/client/tls.crt"

echo "generated disposable edge test certificates in $OUTPUT_DIR"
echo "mount only $OUTPUT_DIR/server into the edge proxy"
