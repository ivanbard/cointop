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
Historical chart responses are fresh for five minutes and use the same
24-hour stale fallback. They cache the provider's historical response; cointop
does not create a locally sampled time-series database.

## Query with cointop

The `data` commands call the running daemon and always write its JSON response
to stdout. They never silently contact an upstream provider.

```bash
cointop data prices --coins btc,eth --currency USD
cointop data coins --limit 100 --offset 0 --currency USD
cointop data coin bitcoin --currency USD
cointop data global --currency USD
cointop data currencies
cointop data chart coin bitcoin --range 7d --currency USD
cointop data chart global --range ytd --currency USD
```

## Share the daemon with the TUI

Direct-provider mode remains the default. To make one or more TUI processes
share the daemon's credentials, snapshots, and in-memory request coalescing,
start the daemon and pass an explicit loopback endpoint:

```bash
cointop api
cointop --endpoint http://127.0.0.1:7070
```

The TUI does not silently fall back to a provider when the daemon is
unavailable. Full coin lists are paginated through REST, and charts, global
data, exchange rates, coin links, and prices all use the daemon adapter.

## Explicit portfolio access

Portfolio data is undiscoverable by default. It cannot be enabled in TOML or
with an environment variable. Opt in only for the lifetime of a process:

```bash
cointop api --expose-portfolio
cointop data portfolio --currency USD
```

When enabled, `GET /v1/portfolio` returns holdings, quantities, balances,
allocation, cost basis, and profit/loss using the same market-data freshness
metadata. Request logs never include portfolio results or quantities. Without
the flag the route returns `404`.

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
GET /v1/charts/coins/{identifier}?currency=USD&range=7d
GET /v1/charts/global?currency=USD&range=ytd
GET /v1/exchange-rate?from=BTC&to=USD
GET /v1/links/coins/{identifier}
GET /v1/portfolio?currency=USD
```

The complete wire contract is maintained in `docs/openapi.yaml`.

## Current boundaries

- Coin identifiers are provider-specific.
- The API is read-only and accessible only from the same machine.
- The cache stores latest snapshots, not a locally sampled time series.
- Streaming, provider failover, and LAN access are not part of the core
  market-data release.
