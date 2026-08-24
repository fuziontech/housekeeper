# Housekeeper - MCP Server for ClickHouse & Prometheus

**An MCP (Model Context Protocol) server that provides AI assistants with direct access to ClickHouse system tables and Prometheus metrics.**

Housekeeper runs as an HTTP MCP server, providing AI assistants like Claude with the ability to query and analyze your ClickHouse clusters and Prometheus metrics in real-time. It exposes read-only access to system tables and metrics, enabling sophisticated analysis, troubleshooting, and monitoring directly through AI conversations.

## 🎯 What it provides

- **ClickHouse Queries**: Read-only access to configurable databases (defaults to `system.*` tables)
- **Prometheus/Victoria Metrics**: Execute PromQL queries for metrics correlation and analysis
- **In-account diagnosis (optional)**: A Bedrock-backed `clickhouse_diagnose` tool that investigates server-side on an elevated connection and returns only a summary
- **Smart Cluster Querying**: Automatic use of `clusterAllReplicas()` for system tables only (non-system tables are queried directly)

---

## 🚀 Quick Start with Claude Desktop

Housekeeper runs as an HTTP server. Connect Claude Desktop to it via [mcp-remote](https://github.com/geelen/mcp-remote).

### 1. Start Housekeeper

```bash
# With a config file (recommended — see configs/config.yml.sample)
docker run -p 8080:8080 \
  -v $(pwd)/configs/config.yml:/etc/housekeeper/config.yml \
  ghcr.io/posthog/housekeeper:latest

# Or with flags
docker run -p 8080:8080 ghcr.io/posthog/housekeeper:latest \
  --ch-host your-clickhouse-host \
  --ch-password your-password
```

### 2. Configure Claude Desktop

Edit `~/Library/Application Support/Claude/claude_desktop_config.json`:

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

> **Note:** mcp-remote requires Node.js v20+. If you manage Node with nvm, hardcode the path to avoid Claude Desktop picking up an older version (e.g. `/Users/you/.nvm/versions/node/v24.x.x/bin`). The `PATH` env override ensures all child processes also use the right version.

### 3. Restart Claude Desktop and start querying!

---

## 📚 MCP Tools Available

### `clickhouse_query`
Query ClickHouse tables from allowed databases with two modes:
- **Structured**: Specify table, columns, filters, ordering, and limits
- **Free-form SQL**: Write custom queries (restricted to allowed databases)

Example questions you can ask Claude:
- "Show me the slowest queries from the last hour"
- "What tables are using the most disk space?"
- "Find all failed queries with their error messages"
- "Show me the current running queries across all nodes"

### `prometheus_query`
Execute PromQL queries for metrics analysis:
- Range queries with customizable time windows
- Support for Victoria Metrics cluster mode

Example requests:
- "What's the current query rate per second?"
- "Show me memory usage trends for the past hour"
- "Find nodes with high CPU usage"

### `clickhouse_diagnose` (optional)
Ask a natural-language question ("why is X slow", "what's driving load") and a server-side Bedrock agent investigates via a guarded `run_sql` tool on a separate, elevated ClickHouse connection, returning only an attributed summary. Registered when `bedrock.region` + `bedrock.model_id` are set; credentials come from the default AWS chain. Bounded by `bedrock.max_iterations` / `bedrock.max_seconds` — make sure your MCP client's tool timeout exceeds the latter. See [MCP.md](./MCP.md) for details.

---

## 🔍 Investigation Playbook

For diagnosing ClickHouse + ZooKeeper issues (replication anomalies, counter spikes, session expirations, readonly replicas) using the MCP tools above, see **[INVESTIGATION_PLAYBOOK.md](./INVESTIGATION_PLAYBOOK.md)**.

It captures a triage methodology and a set of `system.*` query patterns that resolve most common dead ends — particularly the "source code says impossible but data shows otherwise" trap.

---

## 🐳 Running with Docker

```bash
# Build the image
docker build -t housekeeper .

# Run with a config file
docker run -p 8080:8080 \
  -v $(pwd)/configs/config.yml:/etc/housekeeper/config.yml \
  housekeeper --config /etc/housekeeper/config.yml
```

The health check endpoint is available at `GET /health`.

## ⚙️ Configuration

### Command-Line Flags
```bash
housekeeper \
  --http-addr ":8080" \
  --http-auth-token "your-secret-token" \
  --ch-host "127.0.0.1" \
  --ch-port 9000 \
  --ch-user "default" \
  --ch-password "password" \
  --ch-database "default" \
  --ch-cluster "cluster_name" \
  --ch-allowed-databases "system,models" \
  --prom-host "localhost" \
  --prom-port 8481
```

### Configuration File
Copy `configs/config.yml.sample` to `configs/config.yml` and fill in your values:

```yaml
clickhouse:
  host: "127.0.0.1"
  port: 9000
  user: "default"
  password: "password"
  database: "default"
  cluster: "cluster_name"
  allowed_databases:
    - "system"
    - "models"
prometheus:
  host: "localhost"
  port: 8481
  # Victoria Metrics cluster mode (optional)
  vm_cluster_mode: false
  vm_tenant_id: "0"
  vm_path_prefix: ""
http:
  addr: ":8080"
  auth_token: "your-secret-token"
```

See [`configs/config.yml.sample`](configs/config.yml.sample) for the full set of options, including `logging` and the optional `mcp.extra_tool_description`.

Then run:
```bash
docker run -p 8080:8080 \
  -v $(pwd)/configs/config.yml:/etc/housekeeper/config.yml \
  housekeeper --config /etc/housekeeper/config.yml
```

### Victoria Metrics Cluster Mode
```bash
docker run -p 8080:8080 housekeeper \
  --prom-vm-cluster \
  --prom-vm-tenant "0" \
  --prom-vm-prefix "select/0/prometheus"
```

### Kubernetes Port Forwarding
```bash
# Forward Victoria Metrics from K8s
kubectl port-forward --namespace=monitoring \
  svc/vmcluster-victoria-metrics-cluster-vmselect 8481:8481
```

Now configure the Prometheus host and port in your `config.yml`:

```yaml
prometheus:
  host: "localhost"
  port: 8481
```

## 📊 Analysis Mode (Optional)

Housekeeper also includes an AI-powered analysis mode for automated monitoring and alerting:

```bash
docker run housekeeper --analyze
docker run housekeeper --analyze --performance
```

This mode:
- Queries recent errors from ClickHouse
- Analyzes patterns using Google Gemini AI
- Generates Slack-ready summaries
- Requires `gemini_key` in config

```yaml
gemini_key: "your-gemini-api-key"
clickhouse:
  # ... same as above
```

---

## 🔒 Security Notes

- **Read-Only**: SELECT-only at the SQL layer; DDL and writes are blocked. Server-side role/profile is the real boundary.
- **Authentication**: Set `--http-auth-token` (or `HOUSEKEEPER_HTTP_AUTH_TOKEN`) for bearer auth, or leave unset and front housekeeper with a network-level identity gate.
- **Credentials**: `configs/config*.yml` are gitignored. Only `*.example`/`*.sample` templates are tracked.

---

## 📁 Project Structure

```
.
├── main.go                  # Entry point, flag definitions
├── sdk_mcp.go               # HTTP MCP server, tool registration, middlewares
├── clickhouse_mcp.go        # ClickHouse query validation and execution
├── prometheus_mcp.go        # Prometheus/Victoria Metrics client
├── diagnose_mcp.go          # clickhouse_diagnose tool + analyst connection
├── bedrock.go               # Bedrock Converse tool-use loop (IRSA credentials)
├── clickhouse.go            # ClickHouse connection (analysis mode)
├── agent.go                 # Gemini AI integration (analysis mode)
├── slack.go                 # Slack notifications (analysis mode)
├── config.go                # Config loading and logging setup
├── *_test.go                # Table-driven tests (SQL validator, query building, prometheus)
├── Dockerfile               # Multi-stage build → distroless runtime
├── docker-compose.yml       # Local ClickHouse for development
└── configs/
    └── config.yml.sample    # Configuration template
```

## 🤝 Contributing

We welcome contributions! Key areas:
- Additional MCP tools for ClickHouse operations
- Enhanced Prometheus/Victoria Metrics support
- Improved error handling and validation
- Documentation and examples

## 📄 License

MIT License - see [LICENSE.md](LICENSE.md) for details
