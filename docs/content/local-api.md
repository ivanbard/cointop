---
title: "Local API"
date: 2026-08-21T00:00:00-00:00
draft: false
---

# Local market data API

The local API gives development tools and agents structured access to the same
normalized CoinGecko or CoinMarketCap provider layer used by cointop. Version 1
selects one provider at a time; it does not combine or reconcile providers.

## Start the daemon

```bash
cointop api
```

The default address is `127.0.0.1:7070`. Version 1 rejects non-loopback listen
addresses and client endpoints. API credentials are read from the existing
cointop config and then overridden by `COINGECKO_API_KEY`,
`COINGECKO_PRO_API_KEY`, or `CMC_PRO_API_KEY` when present.

Useful options:

```bash
cointop api --api coingecko --cache-ttl 1m --max-stale 24h
cointop api --config /path/to/config.toml
```

Prices, coins, and global data are fresh for 60 seconds by default. Supported
currencies are fresh for 24 hours. When an upstream refresh fails, cointop may
return the last snapshot for up to 24 additional hours with `stale: true` and
`cacheStatus: "stale"` in the response metadata.

## Query with cointop

The `data` commands call the running daemon and always write its JSON response
to stdout. They never silently contact an upstream provider.

```bash
cointop data prices --coins btc,eth --currency USD
cointop data coins --limit 100 --offset 0 --currency USD
cointop data coin bitcoin --currency USD
cointop data global --currency USD
cointop data currencies
```

## Query with other tools

curl:

```bash
curl "http://127.0.0.1:7070/v1/prices?coins=btc,eth&currency=USD"
```

PowerShell:

```powershell
Invoke-RestMethod "http://127.0.0.1:7070/v1/global?currency=USD"
```

JavaScript:

```javascript
const response = await fetch("http://127.0.0.1:7070/v1/coins/bitcoin?currency=USD");
if (!response.ok) throw new Error(`cointop returned ${response.status}`);
const result = await response.json();
console.log(result.data.price, result.meta);
```

Python:

```python
import json
import urllib.request

with urllib.request.urlopen(
    "http://127.0.0.1:7070/v1/prices?coins=btc,eth&currency=USD"
) as response:
    result = json.load(response)
print(result["data"], result["meta"])
```

Local agents can use the same endpoints through their HTTP tool or execute a
`cointop data` command and parse stdout as JSON. Consumers should check
`meta.stale` and `meta.fetchedAt` before making freshness-sensitive decisions.

## Endpoint summary

```text
GET /v1/health
GET /v1/prices?coins=btc,eth&currency=USD
GET /v1/coins?currency=USD&limit=100&offset=0
GET /v1/coins/{identifier}?currency=USD
GET /v1/global?currency=USD
GET /v1/currencies
```

The complete wire contract is maintained in `docs/openapi.yaml`.

## Current boundaries

- Coin identifiers are provider-specific.
- The API is read-only and accessible only from the same machine.
- The cache stores latest snapshots, not a locally sampled time series.
- Charts, portfolios, streaming, MCP, provider failover, and LAN access are not
  part of version 1.
