# MCP access

server-man exposes the shared tool registry over stdio and Streamable HTTP. Initialization advertises `serverInfo.name` and `title` as `server-man`; executable paths and existing tool identifiers remain unchanged. The **Agent access** page shows the endpoint, available backends, tools and input schemas. Agent calls use the same validation and write policy as the web API.

## Local process

Run the binary as the workload owner:

```sh
/absolute/path/to/server-man -transport stdio -config /absolute/path/to/server-man.yaml
```

The client launches this command and exchanges newline-delimited JSON on stdin/stdout. Diagnostics go to stderr. No HTTP listener or browser token is needed in this mode; the operating-system account supplies local access. Configuration defaults still apply, including `allowWrites: false`.

A standalone stdio process owns its sampler and in-memory job history. Keep the connection open and poll each submitted job to a terminal state before disconnecting. Disconnecting terminates the process and cancels its unfinished work. To share one job history with the web UI, connect to that running server's HTTP endpoint.

Instances using the same state directory acquire an operating-system file lock for a management job. Use the same `stateDir` for instances managing the same workloads. Read operations remain independent. An active write in another process yields a `busy` error; no new change starts.

## Streamable HTTP

The normal HTTP server exposes `/mcp`. Supply the server access token in `Authorization: Bearer <token>`. Use the configured HTTPS reverse proxy for remote connections; `publicOrigin` must match the external origin and the proxy must preserve Host. Origin checks and authentication wrap the MCP handler before protocol processing.

Clients POST one JSON-RPC message with `Content-Type: application/json` and Accept types `application/json` and `text/event-stream`. Responses use JSON. Accepted notifications receive an empty 202 response. GET and DELETE receive 405 because this endpoint is stateless and does not maintain standalone SSE streams or HTTP session IDs. A standard Streamable HTTP MCP client handles these details.

## Agent examples

Replace executable/configuration paths with your actual installation. These are server entries for the agent's configuration; they do not change any existing agent settings automatically.

For [Hermes](https://hermes-agent.nousresearch.com/docs/reference/mcp-config-reference), add a stdio entry under `mcp_servers` in `~/.hermes/config.yaml`:

```yaml
mcp_servers:
  server-man:
    command: /absolute/path/to/server-man
    args: ["-transport", "stdio", "-config", "/absolute/path/to/server-man.yaml"]
```

Or connect Hermes to the existing HTTP instance. Put the access token in the agent's secret/environment scope as `SERVER_MAN_TOKEN` and reference it in the header:

```yaml
mcp_servers:
  server-man:
    url: https://your-server.example/mcp
    headers:
      Authorization: "Bearer ${SERVER_MAN_TOKEN}"
```

For [OpenClaw](https://docs.openclaw.ai/tools/mcp), configure a stdio server under `mcp.servers`:

```json
{
  "mcp": {
    "servers": {
      "server-man": {
        "transport": "stdio",
        "command": "/absolute/path/to/server-man",
        "args": ["-transport", "stdio", "-config", "/absolute/path/to/server-man.yaml"],
        "enabled": true
      }
    }
  }
}
```

OpenClaw's Settings → MCP also accepts a Streamable HTTP URL. Configure its sensitive Authorization header through the agent's secret settings. Its documented `openclaw mcp doctor server-man --probe` command checks discovery. These examples follow the agents' documented configuration surfaces; this repository's interoperability verification uses the official MCP SDK, rather than running either agent.

## Tool results and jobs

Start with `system_capabilities` to inspect backend availability and the write policy. Tools carry read-only/destructive hints and the registry's input JSON Schemas. Writes require server configuration `allowWrites: true`; destructive tools additionally require `confirm: true`.

Normal object results appear directly in `structuredContent`, and the serialized object is also returned as text. Array results use an `items` wrapper, and scalar results use `value`. An operation or argument validation failure returns `isError: true` with an `error` object containing a code and message. Invalid protocol requests and unknown tools use JSON-RPC errors.

Management writes return a `job` object. Read its ID using `jobs_get` until `succeeded`, `failed` or `cancelled`. `jobs_cancel` requests cancellation; it cannot undo completed changes. HTTP jobs continue independently of the requesting connection, and remain visible in the web UI's Activity panel.

## Protocol and resource boundaries

The adapter supports MCP revisions `2025-11-25`, `2025-06-18` and `2025-03-26`. Initialization negotiates a supported version; HTTP rejects unsupported protocol-version headers. The server advertises tools only. Its registry is fixed at startup, so it does not advertise list-change notifications.

Stdio requires initialization before ordinary operations, bounds pending tool calls to four, and processes cancellation notifications while a tool runs. Read operations also retain the core registry's deadlines/concurrency bound. Mutation jobs use their own bounded lifetime; use the job cancellation tool after a job has been returned. Messages are capped at 1 MiB plus 16 KiB for the envelope; tool arguments remain capped at 1 MiB.

The protocol implementation uses Go's standard library and is isolated in `internal/mcp/`. The preferred Go SDK could not be downloaded in this workspace because the sandbox denies DNS socket creation; no copied SDK source or unverified dependency was added. Compatibility is covered with the official TypeScript SDK in the verification tooling, which is not part of the runtime binary.

References: [MCP transports](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports), [lifecycle](https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle), [tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools), and [cancellation](https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/cancellation).

## Verification

`make mcp-test` builds the binary and a test-only HTTP bridge, then uses the official TypeScript SDK to initialize, discover tools, read live Linux metrics, and verify validation/write errors. It compares the full tool list and input schemas across stdio, MCP HTTP and the JSON API. Authentication/origin rejection and absence of protocol errors are checked too.

The SDK HTTP client's custom fetch passes requests through the actual authenticated Go HTTP handler in process. This verifies protocol serialization and handler behavior without a TCP socket. It does not prove listening, reverse-proxy/TLS deployment or browser rendering, which remain explicit acceptance checks in the [plan](../plans/implementation.md). Go race tests additionally cover malformed/oversized messages, negotiation, lifecycle, cancellation, destructive acknowledgement and shared job execution.
