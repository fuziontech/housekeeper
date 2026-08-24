# ClickHouse MCP Server Documentation

**Housekeeper runs as an MCP server by default** - no flags needed! It serves MCP over HTTP (streamable HTTP transport); connect a client such as Claude Desktop via [mcp-remote](https://github.com/geelen/mcp-remote). This document covers the complete MCP implementation that exposes tools for:
1. Read‑only queries against configurable ClickHouse databases
2. Querying Prometheus metrics for monitoring and correlation
3. (Optional) Querying a separate Prometheus endpoint for ClickHouse-internal metrics
4. (Optional) An in-account, Bedrock-backed diagnosis agent (`clickhouse_diagnose`)

> **Looking for investigation guidance?** See [INVESTIGATION_PLAYBOOK.md](./INVESTIGATION_PLAYBOOK.md) for a methodology and `system.*` query patterns for diagnosing ClickHouse + ZooKeeper issues using these tools.

## Installation

### Option 1: Install via go install (Recommended)

```bash
# Install the latest version directly from GitHub
go install github.com/PostHog/housekeeper@latest

# The binary will be installed to $GOPATH/bin/housekeeper
# Make sure $GOPATH/bin is in your PATH
```

### Option 2: Build from source

```bash
# Clone the repository
git clone https://github.com/PostHog/housekeeper.git
cd housekeeper

# Build integrated binary
go build -o housekeeper
```

## Configuration

### Option 1: Command-line flags (Recommended for MCP)

You can configure the server entirely via command-line flags, making it easy to use without config files:

```bash
# MCP mode is the default - just run with your connection parameters
housekeeper \
  --ch-host "127.0.0.1" \
  --ch-port 9000 \
  --ch-user "default" \
  --ch-password "your-password" \
  --ch-database "default" \
  --ch-cluster "default" \
  --ch-allowed-databases "system,models" \
  --prom-host "localhost" \
  --prom-port 8481
```

Available flags:
- `--ch-host`: ClickHouse host (default: "127.0.0.1")
- `--ch-allowed-databases`: Comma-separated list of databases to allow (default: ["system"])
- `--ch-port`: ClickHouse port (default: 9000)
- `--ch-user`: ClickHouse user (default: "default")
- `--ch-password`: ClickHouse password (default: "")
- `--ch-database`: ClickHouse database (default: "default")
- `--ch-cluster`: ClickHouse cluster name (default: "default")
- `--prom-host`: Prometheus/Victoria Metrics host (default: "localhost")
- `--prom-port`: Prometheus/Victoria Metrics port (default: 8481)
- `--prom-vm-cluster`: Enable Victoria Metrics cluster mode (default: false)
- `--prom-vm-tenant`: Victoria Metrics tenant ID (default: "0")
- `--prom-vm-prefix`: Victoria Metrics path prefix (default: "")

### Option 2: Configuration file

- Uses `configs/config.yml` (Viper) — copy and edit `configs/config.yml.sample`.
- You can point to a custom path with `-config /path/to/config.yml` or env `HOUSEKEEPER_CONFIG=/path/to/config.yml`.
- Required keys for ClickHouse: `clickhouse.host`, `clickhouse.port`, `clickhouse.user`, `clickhouse.password`, `clickhouse.database`, `clickhouse.cluster`.
  - The DB user should be read‑only; queries are restricted to the configured `clickhouse.allowed_databases` (defaults to `["system"]`).
- Required keys for Prometheus: `prometheus.host`, `prometheus.port`.

### Victoria Metrics from Kubernetes

If you need to expose Victoria Metrics from Kubernetes locally:

```bash
kubectl port-forward --namespace=monitoring svc/vmcluster-victoria-metrics-cluster-vmselect  8481:8481
```


## Running (HTTP)

The server uses the official go-sdk and serves MCP over HTTP using the streamable HTTP transport. It listens on `http.addr` (default `:8080`), exposes the MCP endpoint at `POST /`, and a `GET /health` check for liveness probes (no auth; returns a static `ok`). Connect stdio-only clients (e.g. Claude Desktop) via [mcp-remote](https://github.com/geelen/mcp-remote).

- **Default mode**: `./housekeeper` (runs the HTTP MCP server)
- **Analysis mode**: `./housekeeper --analyze` (runs Gemini AI analysis)
- Optional bearer auth: set `http.auth_token` (or `--http-auth-token`) and clients must present it.
- Methods implemented:
  - `initialize`
  - `tools/list`
  - `tools/call`

## Tools

### Tool: clickhouse_query

- Name: `clickhouse_query`
- Description: Query ClickHouse tables (read‑only) from configured allowed databases.
- Arguments (two modes):
  - Structured: `table` (required, must be from allowed databases), `columns`[], `where`, `order_by`, `limit`.
  - Free-form: `sql` (string) — must be a single SELECT/WITH statement referencing only tables from allowed databases. Semicolons and write/DDL are rejected.
- Allowed databases: Configured via `--ch-allowed-databases` flag or `clickhouse.allowed_databases` in config (defaults to ["system"])

**IMPORTANT Usage Guidelines:**
- **Structured mode auto-wraps `system.*`**: the tool converts `system.query_log` → `clusterAllReplicas(cluster, system.query_log)` for cluster-wide data. Non-system tables are queried directly. Note: on deployments with column REVOKEs on query-log tables, this forced wrap fails (a distributed read demands the whole-table grant) — use free-form `sql` with a plain single-node read for those tables.
- **Free-form mode wraps nothing** — choose per table: replicated tables (same data everywhere) → query directly to avoid duplicate rows; sharded tables and per-node `system.*` → wrap in `clusterAllReplicas('<cluster>', db.table)` to see everything. Check `system.tables.engine` if unsure.
- Only `db.table` and `clusterAllReplicas('cluster', db.table)` references are accepted; `cluster()` and `remote()` are blocked.

### Tool: prometheus_query

- Name: `prometheus_query`
- Description: Execute PromQL queries against Prometheus metrics.
- Arguments:
  - `query` (required): PromQL query string
  - `start` (optional): Start time in RFC3339 format or relative time (e.g. "-1h")
  - `end` (optional): End time in RFC3339 format or relative time (e.g. "-1h")
  - `step` (optional): Step duration (e.g. "15s", "1m", "1h") (default: "1m")

### Tool: clickhouse_diagnose (optional)

- Name: `clickhouse_diagnose`
- Description: Ask a natural-language question about ClickHouse health; a server-side Bedrock agent (credentials from the default AWS chain) investigates with a guarded `run_sql` tool on the elevated `analyst_clickhouse.*` connection and returns only a text summary — raw query text never leaves the account.
- Registration: only exposed when both `bedrock.region` and `bedrock.model_id` are set.
- Arguments: `question` (required), `cluster` (optional focus hint).
- Budgets: `bedrock.max_iterations` run_sql round-trips and `bedrock.max_seconds` wall-clock; on exceeding the time budget the agent stops investigating and summarizes findings so far. The MCP client's tool timeout must exceed `bedrock.max_seconds` (e.g. `MCP_TOOL_TIMEOUT` in Claude Code) or the client gives up while the server keeps working.

## Example tools/call

Request:
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "clickhouse_query",
    "arguments": {
      "table": "system.query_log",
      "columns": ["query", "query_duration_ms", "memory_usage"],
      "where": "event_time > subtractHours(now(), 1) AND query_duration_ms > 1000",
      "order_by": "query_duration_ms DESC",
      "limit": 10
    }
  }
}
```

Response (truncated):
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "content": [
      {"type": "json", "data": {"results": [{"query": "..."}], "count": 10}}
    ]
  }
}
```

### Free-form example (ClickHouse)

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "clickhouse_query",
    "arguments": {
      "sql": "WITH slow AS (SELECT event_time, query_duration_ms FROM clusterAllReplicas(default, system.query_log) WHERE event_time > subtractHours(now(),1)) SELECT count() AS cnt, quantileExact(0.95)(query_duration_ms) AS p95 FROM slow"
    }
  }
}
```

### Prometheus example (range query)

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "prometheus_query",
    "arguments": {
      "query": "rate(clickhouse_query_duration_ms_sum[5m])",
      "start": "-1h",
      "end": "-10m",
      "step": "1m"
    }
  }
}
```

## Claude Desktop Integration

Housekeeper serves MCP over HTTP, so Claude Desktop connects to it through [mcp-remote](https://github.com/geelen/mcp-remote) rather than spawning the binary directly.

### 1. Start the housekeeper server

Run it locally with flags:

```bash
housekeeper \
  --ch-host "127.0.0.1" \
  --ch-port 9000 \
  --ch-user "default" \
  --ch-password "your-password" \
  --ch-database "default" \
  --ch-cluster "default" \
  --prom-host "localhost" \
  --prom-port 8481
```

…or with a config file:

```bash
housekeeper --config /path/to/your/config.yml
```

This listens on `http.addr` (default `:8080`). It can also be a remote/deployed instance reachable over your network.

### 2. Point Claude Desktop at it

Find your Claude Desktop config file:
- macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`
- Windows: `%APPDATA%\Claude\claude_desktop_config.json`
- Linux: `~/.config/claude/claude_desktop_config.json`

Add an `mcp-remote` entry pointing at the server's URL:

```json
{
  "mcpServers": {
    "housekeeper": {
      "command": "npx",
      "args": ["mcp-remote", "http://localhost:8080"],
      "env": {
        "PATH": "/path/to/node/v20+/bin:/usr/local/bin:/usr/bin:/bin"
      }
    }
  }
}
```

> **Note:** mcp-remote requires Node.js v20+. If you manage Node with nvm, hardcode the path so Claude Desktop doesn't pick up an older version. If the server has `http.auth_token` set, pass it via mcp-remote's `--header` argument (e.g. `"--header", "Authorization: Bearer <token>"`).

### 3. Restart Claude Desktop for the changes to take effect.

## Notes

- Queries are restricted to the configured `allowed_databases` (defaults to `system`) and reject multi‑statement inputs.
- For `system.*` tables the server uses `clusterAllReplicas(<cluster>, <system.table>)` for cluster‑wide visibility; non-system tables are queried directly.
- If building fails initially, run `go mod tidy` to fetch `github.com/modelcontextprotocol/go-sdk`.
- The DB user should be read‑only for security.
