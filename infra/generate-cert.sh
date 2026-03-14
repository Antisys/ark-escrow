#!/bin/bash
# Generate self-signed TLS certificate for a local IP
# Usage: ./generate-cert.sh [IP]

IP="${1:-192.168.1.125}"
DIR="$(dirname "$0")/certs"
mkdir -p "$DIR"

openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
  -days 3650 -nodes \
  -keyout "$DIR/key.pem" -out "$DIR/cert.pem" \
  -subj "/CN=$IP" \
  -addext "subjectAltName=IP:$IP"

echo "Certificate generated for $IP in $DIR/"
echo "Install in browser trust store:"
echo "  sudo cp $DIR/cert.pem /usr/local/share/ca-certificates/arkd-local.crt"
echo "  sudo update-ca-certificates"
echo "  certutil -A -n 'arkd local' -t 'TC,C,T' -i $DIR/cert.pem -d sql:~/.pki/nssdb"
