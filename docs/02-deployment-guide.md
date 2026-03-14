# Deployment Guide — arkd on Regtest

## Prerequisites
- Docker + Docker Compose
- Nigiri (Bitcoin regtest environment)

## Setup Steps

### 1. Install Nigiri
```bash
curl -sL -o /tmp/nigiri \
  "https://github.com/vulpemventures/nigiri/releases/download/v0.5.16/nigiri-linux-amd64"
chmod +x /tmp/nigiri && sudo mv /tmp/nigiri /usr/local/bin/
nigiri start
```

### 2. Clone and Deploy arkd
```bash
git clone https://github.com/arkade-os/arkd.git ark
cd ark
docker compose -f docker-compose.regtest.yml up --build -d
```

### 3. Create and Unlock Operator Wallet
```bash
docker exec arkd arkd wallet create --password <password>
# Save the mnemonic!
docker exec arkd arkd wallet unlock --password <password>
```

### 4. Fund Operator Wallet
```bash
ADDR=$(docker exec arkd arkd wallet address)
nigiri faucet $ADDR 1
nigiri rpc generatetoaddress 2 $(nigiri rpc getnewaddress)
```

## Services

| Service | Port | Purpose |
|---------|------|---------|
| arkd | 7070 (public), 7071 (admin) | Ark server |
| arkd-wallet | 6060 | Wallet/signer |
| nbxplorer | 32838 | TX indexer |
| PostgreSQL | 5432 | nbxplorer DB |
| Redis | 6379 | Cache |
| Bitcoin (Nigiri) | 18443 (RPC), 3000 (Chopsticks) | Regtest node |

## Configuration (Regtest Defaults)

| Parameter | Value | Description |
|-----------|-------|-------------|
| `ARKD_SESSION_DURATION` | 10s | Round interval |
| `ARKD_VTXO_TREE_EXPIRY` | 20s | VTXO lifetime (regtest) |
| `ARKD_UNILATERAL_EXIT_DELAY` | 512 blocks | Unilateral exit timeout |
| `ARKD_BOARDING_EXIT_DELAY` | 1024 blocks | Boarding exit timeout |
| `ARKD_ROUND_MIN_PARTICIPANTS_COUNT` | 1 | Min participants per round |
| `ARKD_DB_TYPE` | sqlite | Database (light mode) |
| `ARKD_NO_MACAROONS` | true | Disable auth (regtest) |

## HTTPS Reverse Proxy (Caddy)

Required for browser wallet (Service Workers need HTTPS).

```caddyfile
:443 {
    tls /certs/cert.pem /certs/key.pem

    handle /v1/* {
        reverse_proxy arkd:7070 {
            flush_interval -1
        }
    }

    handle /esplora/* {
        uri strip_prefix /esplora
        reverse_proxy esplora-proxy:3001
    }

    handle {
        root * /srv
        try_files {path} /index.html
        file_server
    }
}
```

### Self-Signed Certificate for Local IP
```bash
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
  -days 3650 -nodes -keyout key.pem -out cert.pem \
  -subj "/CN=192.168.1.125" \
  -addext "subjectAltName=IP:192.168.1.125"
```

Install in browser trust store:
```bash
sudo cp cert.pem /usr/local/share/ca-certificates/arkd-local.crt
sudo update-ca-certificates
# For Chrome/Brave:
certutil -A -n "arkd local" -t "TC,C,T" -i cert.pem -d sql:~/.pki/nssdb
```

## Esplora Address Proxy

The Arkade Wallet SDK generates `bc1p` (mainnet) addresses on regtest. A proxy converts them to `bcrt1p` for the explorer API. See `esplora-proxy.mjs`.
