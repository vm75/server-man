<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { request, type Discovery } from '../lib/api'

  let { allowWrites }: { allowWrites: boolean } = $props()
  let discovery = $state<Discovery | null>(null)
  let endpoint = $state('/mcp')
  let connection = $state('http')
  let error = $state('')
  let busy = $state(true)
  let copied = $state('')
  const controller = new AbortController()
  const localExample = JSON.stringify({ command: '/absolute/path/to/server-man', args: ['-transport', 'stdio', '-config', '/absolute/path/to/config.json'] }, null, 2)

  async function load() {
    busy = true
    error = ''
    try { discovery = await request<Discovery>('/api/tools', { signal: controller.signal }) }
    catch (e) { if (!controller.signal.aborted) error = e instanceof Error ? e.message : 'Could not discover tools' }
    finally { busy = false }
  }
  async function copyEndpoint() {
    try { await navigator.clipboard.writeText(endpoint); copied = 'Endpoint copied' }
    catch { copied = 'Copy the endpoint from the text above.' }
  }
  onMount(() => { endpoint = `${window.location.origin}/mcp`; void load(); return () => controller.abort() })
</script>

<section class="panel agent-intro">
  <span class="module-icon"><Icon name="agent" size={25} /></span>
  <div><h2>Connect your AI agent</h2><p>MCP gives your agent the same validated operations as this workspace. {allowWrites ? 'Management writes are enabled.' : 'Your server currently allows read operations only.'}</p></div>
</section>

<div class="agent-grid">
  <section class="panel agent-connection" aria-labelledby="connection-heading">
    <div class="resource-heading"><h2 id="connection-heading">Connection</h2><span>MCP tools</span></div>
    <div class="connection-tabs"><button class:active={connection === 'http'} aria-pressed={connection === 'http'} onclick={() => { connection = 'http' }}>This server</button><button class:active={connection === 'stdio'} aria-pressed={connection === 'stdio'} onclick={() => { connection = 'stdio' }}>Local process</button></div>
    {#if connection === 'http'}
      <label for="mcp-endpoint">Streamable HTTP endpoint</label><div class="endpoint-field"><input id="mcp-endpoint" value={endpoint} readonly /><button class="small-button" onclick={copyEndpoint}>Copy</button></div>
      <p class="agent-help">Add this URL to your agent with an Authorization header: <code>Bearer &lt;server access token&gt;</code>. Use your HTTPS address when connecting remotely.</p>
      <dl class="agent-facts"><div><dt>Authentication</dt><dd>Server access token</dd></div><div><dt>Protocol</dt><dd>2025-11-25</dd></div><div><dt>Transport</dt><dd>Streamable HTTP · JSON responses</dd></div></dl>
    {:else}
      <p class="agent-help">Launch the binary as a stdio MCP server on the Linux host. Replace the paths with your binary and configuration file.</p><pre class="connection-code">{localExample}</pre><p class="agent-help">Run it as the account that owns the workloads. Each local process has its own recent job history; keep the connection open until its jobs finish. Instances using the same state directory serialize changes.</p>
    {/if}
    <p class="copy-status" aria-live="polite">{copied}</p>
  </section>
  <section class="panel agent-notes" aria-labelledby="agent-workflow-heading">
    <div class="resource-heading"><h2 id="agent-workflow-heading">Using the tools</h2></div>
    <ol><li>Read <code>system_capabilities</code> for available backends and write permissions.</li><li>Inspect resources before making a change.</li><li>For a change, use the returned job ID with <code>jobs_get</code> until it finishes.</li><li>Use <code>jobs_cancel</code> to stop remaining work. Completed changes remain in place.</li></ol>
    <div class="agent-policy"><strong>{allowWrites ? 'Writes enabled' : 'Read only'}</strong><p>Destructive tools require an explicit <code>confirm: true</code>. Your agent's own tool permissions also apply.</p></div>
    <p class="agent-help">In OpenClaw, configure the server under <code>mcp.servers</code>. In Hermes, use <code>mcp_servers</code>. Both can connect by command or URL.</p>
  </section>
</div>

<div class="section-heading"><h2>Available to your agent</h2><span class="muted">{discovery?.tools.length ?? '—'} tools</span></div>
{#if error}<div class="notice error" role="alert"><p>{error}</p><button onclick={load} disabled={busy}>Retry</button></div>{:else if busy}<section class="panel empty-state" aria-busy="true"><span class="loading-ring"></span><p>Reading tool capabilities…</p></section>{:else if discovery}
  <div class="capability-grid">{#each discovery.modules as module}<div class="panel capability-card"><div><strong>{module.title}</strong><span class="capability-state" class:available={module.available}>{module.available ? 'Available' : 'Unavailable'}</span></div>{#if module.reason}<p>{module.reason}</p>{/if}</div>{/each}</div>
  <section class="panel tool-directory" aria-label="MCP tool directory">{#each discovery.tools as tool}<details class="tool-entry"><summary><div><strong>{tool.title}</strong><code>{tool.name}</code></div><span class="tool-access" class:write={!tool.readOnly}>{tool.destructive ? 'Destructive' : tool.readOnly ? 'Read' : 'Write'}</span></summary><p>{tool.description}</p><pre>{JSON.stringify(tool.inputSchema, null, 2)}</pre></details>{/each}</section>
{/if}
