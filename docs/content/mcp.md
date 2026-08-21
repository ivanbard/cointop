---
title: "MCP"
date: 2026-08-21T00:00:00Z
---

# Model Context Protocol (MCP)

Cointop exposes its normalized, cached market data as a read-only MCP server.
It provides current prices, ranked coins, coin details, global market totals,
and supported currencies. It does not provide trading actions or advice.

## Stdio (recommended for one local agent)

Build or install cointop, find the absolute path to the executable, and configure
your MCP host to run it with the `mcp` argument. Provider credentials use the
same config file and environment variables as the TUI and local API.

Codex `config.toml`:

```toml
[mcp_servers.cointop]
command = "C:\\absolute\\path\\to\\cointop.exe"
args = ["mcp"]
```

Claude Desktop `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "cointop": {
      "command": "C:\\absolute\\path\\to\\cointop.exe",
      "args": ["mcp"]
    }
  }
}
```

Cursor `.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "cointop": {
      "command": "C:\\absolute\\path\\to\\cointop.exe",
      "args": ["mcp"]
    }
  }
}
```

Use an absolute path on every platform. On macOS and Linux it will typically
look like `/usr/local/bin/cointop`. MCP protocol messages are the only output
written to stdout; command failures and diagnostics are written to stderr.

## Shared daemon

Start the loopback daemon when REST, CLI, and MCP clients should share one
in-memory cache and one set of provider requests:

```bash
cointop api --listen 127.0.0.1:7070
```

The stateless Streamable HTTP endpoint is `http://127.0.0.1:7070/mcp`.
It is intentionally unavailable on non-loopback listeners.

## Tools and resources

The tools are `get_prices`, `list_coins`, `get_coin`, `get_global_market`,
`list_currencies`, `get_coin_history`, and `get_global_history`. Every tool is annotated read-only,
idempotent, non-destructive, and open-world. Results contain typed structured
content plus serialized JSON text for compatibility.

Resources use `application/json`:

```text
cointop://currencies
cointop://prices/{coins}{?currency}
cointop://coins/{identifier}{?currency}
cointop://market/global{?currency}
cointop://charts/coins/{identifier}{?currency,range}
cointop://charts/global{?currency,range}
```

Stale snapshots remain successful results with `meta.stale=true`. Invalid
input, missing coins, and provider outages return stable messages without
including credentials or upstream response details.

## Private portfolio opt-in

Run `cointop mcp --expose-portfolio` to register `get_portfolio` and
`cointop://portfolio` for that process only. There is no persistent or
environment-variable opt-in. Without the flag, MCP discovery does not reveal
that the portfolio capability exists. Portfolio arguments, quantities, and
results are never logged.

Use `--fallback-api` with either `cointop mcp` or `cointop api` to configure one
ordered secondary provider. Fallback results carry explicit provider provenance
in `meta`; cointop never combines provider values.
