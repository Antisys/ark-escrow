# Known Issues & Workarounds

## 1. Arkade Wallet SDK generates mainnet addresses on regtest

**Problem:** The SDK's boarding address uses `bc1p` prefix (mainnet) instead of `bcrt1p` (regtest), even when connected to a regtest server.

**Root Cause:** Unknown — the SDK correctly defines `bech32: "bcrt"` for regtest in its network config, but the built bundle produces mainnet addresses.

**Workaround:** An `esplora-proxy` (Node.js) intercepts Esplora API requests and converts `bc1p` addresses to `bcrt1p` using proper bech32m re-encoding with correct checksums.

See: `infra/esplora-proxy.mjs`

## 2. Mixed Content blocks Service Worker fetches

**Problem:** The Arkade Wallet runs on HTTPS (required for Service Workers), but the Esplora API and arkd run on HTTP. Browsers block mixed content from Service Workers.

**Workaround:** All services are proxied through Caddy on the same HTTPS origin:
- `/v1/*` → arkd:7070
- `/esplora/*` → esplora-proxy:3001 → chopsticks:3000

## 3. Service Worker caching in Brave

**Problem:** Brave caches Service Workers and IndexedDB aggressively. Even "Clear site data" doesn't always clean everything.

**Workaround:** Run in browser console:
```js
(await navigator.serviceWorker.getRegistrations()).forEach(r => r.unregister());
(await indexedDB.databases()).forEach(db => indexedDB.deleteDatabase(db.name));
(await caches.keys()).forEach(k => caches.delete(k));
localStorage.clear();
location.reload();
```

## 4. Delegator service not available for regtest

**Problem:** The Arkade Wallet defaults to `delegate: true` and tries to reach a delegator service at `localhost:7002` (regtest), which doesn't exist.

**Workaround:** Set `delegate: false` in `src/providers/config.tsx` and set regtest delegator URL to `null` in `src/lib/constants.ts` before building.

## 5. Self-signed cert not trusted by Service Workers

**Problem:** Even after accepting a self-signed cert in the browser tab, Service Workers reject it with `ERR_CERT_AUTHORITY_INVALID`.

**Workaround:** Install the cert in the browser's NSS database:
```bash
certutil -A -n "arkd local" -t "TC,C,T" -i cert.pem -d sql:~/.pki/nssdb
```

## 6. Caddy on-demand TLS generates cert for Docker IP

**Problem:** When using `tls internal { on_demand }`, Caddy generates certs for the Docker-internal IP (e.g., 172.24.0.11) instead of the external IP.

**Workaround:** Use a manually generated certificate with the correct IP in SAN:
```bash
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
  -days 3650 -nodes -keyout key.pem -out cert.pem \
  -subj "/CN=192.168.1.125" \
  -addext "subjectAltName=IP:192.168.1.125"
```
Then reference in Caddyfile: `tls /certs/cert.pem /certs/key.pem`
