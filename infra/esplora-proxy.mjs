/**
 * Esplora API Proxy for Regtest
 *
 * Converts bc1p (mainnet) bech32m addresses to bcrt1p (regtest) in API requests.
 * Required because the Arkade Wallet SDK generates mainnet-prefix addresses on regtest.
 *
 * Run: docker run -d --name esplora-proxy --network nigiri \
 *        -v ./esplora-proxy.mjs:/app/proxy.mjs:ro \
 *        node:22-alpine node /app/proxy.mjs
 */
import http from "http";

const UPSTREAM = process.env.ESPLORA_URL || "http://chopsticks:3000";
const PORT = parseInt(process.env.PORT || "3001");
const CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l";

function bech32Polymod(values) {
  const GEN = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3];
  let chk = 1;
  for (const v of values) {
    const b = chk >> 25;
    chk = ((chk & 0x1ffffff) << 5) ^ v;
    for (let i = 0; i < 5; i++) chk ^= (b >> i) & 1 ? GEN[i] : 0;
  }
  return chk;
}

function hrpExpand(hrp) {
  return [...hrp]
    .map((c) => c.charCodeAt(0) >> 5)
    .concat([0])
    .concat([...hrp].map((c) => c.charCodeAt(0) & 31));
}

/**
 * Convert a bc1p... (mainnet bech32m) address to bcrt1p... (regtest).
 * Strips the old checksum, recomputes for the "bcrt" HRP.
 */
function convertAddress(addr) {
  if (!addr.startsWith("bc1p")) return addr;
  const pos = addr.lastIndexOf("1");
  const data = [...addr.slice(pos + 1)]
    .map((c) => CHARSET.indexOf(c))
    .slice(0, -6);
  const vals = hrpExpand("bcrt")
    .concat(data)
    .concat([0, 0, 0, 0, 0, 0]);
  const polymod = bech32Polymod(vals) ^ 0x2bc830a3; // bech32m constant
  const checksum = Array.from(
    { length: 6 },
    (_, i) => CHARSET[(polymod >> (5 * (5 - i))) & 31]
  );
  return "bcrt1" + addr.slice(pos + 1, addr.length - 6) + checksum.join("");
}

const server = http.createServer((req, res) => {
  let url = req.url;
  // Rewrite bc1p addresses in /address/<addr>/ paths
  url = url.replace(
    /\/address\/(bc1[a-z0-9]+)(\/|$)/g,
    (_, addr, trail) => `/address/${convertAddress(addr)}${trail}`
  );

  const proxyReq = http.request(
    UPSTREAM + url,
    { method: req.method, headers: { ...req.headers, host: new URL(UPSTREAM).host } },
    (proxyRes) => {
      res.writeHead(proxyRes.statusCode, proxyRes.headers);
      proxyRes.pipe(res);
    }
  );
  proxyReq.on("error", (e) => {
    res.writeHead(502);
    res.end(e.message);
  });
  req.pipe(proxyReq);
});

server.listen(PORT, "0.0.0.0", () =>
  console.log(`Esplora proxy on :${PORT} → ${UPSTREAM}`)
);
