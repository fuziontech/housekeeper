# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**Housekeeper is an MCP-first application**: by default it runs an HTTP MCP server (streamable HTTP transport) that gives AI assistants read-only access to ClickHouse and Prometheus/Victoria Metrics, plus an optional in-account LLM diagnosis tool.

### MCP tools exposed

- **clickhouse_query** — read-only queries (structured fields or free-form single SELECT/WITH), restricted to `clickhouse.allowed_databases`. Free-form SQL goes through the validator in `clickhouse_mcp.go` (allowlist of table refs, forbidden keywords, quote/whitespace normalization).
- **prometheus_query** — PromQL range queries against the main Prometheus/VM endpoint.
- **clickhouse_diagnose** — only registered when `bedrock.region` + `bedrock.model_id` are set. Runs a server-side Bedrock Converse tool-use loop (`bedrock.go`) with a single `run_sql` tool against the elevated `analyst_clickhouse.*` connection, bounded by `bedrock.max_iterations` and `bedrock.max_seconds`, and returns only a text summary. The system prompt lives in `diagnose_mcp.go`; deployment-specific context is appended from `mcp.extra_tool_description`.

### Analysis Mode (legacy, optional)

With `--analyze`, runs a one-shot Gemini-based error analysis (`agent.go`, `slack.go`, `clickhouse.go`) and posts to Slack. Legacy; not used in MCP server deployments.

## Development Commands

```bash
go run .                          # MCP server (default)
go run . --config configs/config.yml
go build -o housekeeper
go test ./...                     # tests exist: validator, query building, prometheus parsing
gofmt -s -w . && go vet ./...
docker-compose up -d              # local ClickHouse
```

## Architecture

### Core files

1. **main.go** — entry point, pflag definitions, mode selection
2. **sdk_mcp.go** — MCP server (go-sdk), tool registration, tool descriptions, HTTP transport + auth/CORS/logging middleware, row summarization
3. **clickhouse_mcp.go** — query args validation, free-form SQL validator, query building/execution, value normalization
4. **prometheus_mcp.go** — Prometheus/VM clients (default + optional clickhouse endpoint), time-range validation
5. **diagnose_mcp.go** — clickhouse_diagnose tool, analyst connection, diagnose system prompt
6. **bedrock.go** — Bedrock client (default AWS credential chain), Converse tool-use loop with iteration + wall-clock budgets
7. **config.go** — Viper config: defaults, `HOUSEKEEPER_*` env vars (dots → underscores), config file search paths
8. **clickhouse.go / agent.go / slack.go** — legacy `--analyze` mode (Gemini + Slack)

### Configuration

Priority: CLI flags > env vars > config file > defaults. Notable keys beyond connection params:

- `mcp.extra_tool_description` — deployment-specific guidance appended to BOTH the clickhouse_query description and the diagnose agent's system prompt
- `mcp.query_extra_description` — appended ONLY to clickhouse_query (restricted-role caveats the elevated diagnose agent must not see)
- `bedrock.*` — enables clickhouse_diagnose (region + model_id), budgets, temperature
- `analyst_clickhouse.*` — elevated connection for the diagnose agent; falls back to `clickhouse.*` when unset

### Deployment

The server is deployment-agnostic: everything operator-facing is set via flags, config file, or `HOUSEKEEPER_*` env vars.

## Important Notes

1. **MCP is the default mode** — `--analyze` is legacy and unused in k8s.
2. **Security boundary is server-side** — the SQL validator is defense-in-depth; the ClickHouse role/profile (grants, column REVOKEs) is the real boundary. Keep both in mind when changing the validator.
3. **Tool descriptions are product surface** — most operator-facing behavior is prompt text supplied via the `mcp.extra_tool_description` / `mcp.query_extra_description` config keys, not Go code. Check deployment config first for instruction changes.
4. **Config security** — `configs/config*.yml` are gitignored; only `*.sample`/`*.example` are tracked.
5. **Tests** — table-driven tests in `clickhouse_mcp_test.go`, `clickhouse_test.go`, `prometheus_mcp_test.go`. Extend them when touching the validator; bypasses have happened (multiline whitespace).
