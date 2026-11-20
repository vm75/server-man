<script lang="ts">
  import { onMount } from 'svelte'
  import DefinitionEditor from '../lib/DefinitionEditor.svelte'
  import ConfirmDialog from '../lib/ConfirmDialog.svelte'
  import type { ConfigurationIssue, ConfigurationHealth } from '../lib/managed'
  import { invokeTool, startJob, type Job } from '../lib/api'
  let { allowWrites }: { allowWrites: boolean } = $props()
  type Issue = ConfigurationIssue
  interface Service { id: string; name: string; kind: string; backend: string; enabled: boolean; state: string; registered: boolean; configuration: string; issues: Issue[]; members?: { name: string; state: string }[] }
  interface Inventory { configured: boolean; valid?: boolean; error?: string; services: Service[]; health?: ConfigurationHealth }
  let inventory = $state<Inventory | null>(null)
  let error = $state('')
  let loading = $state(false)
  let query = $state('')
  type EditorState = {
    id: string
    initialKind?: 'container' | 'vm'
    initialEngine?: 'podman' | 'docker'
    initialBackend?: 'quadlet' | 'compose' | 'libvirt' | 'incus' | 'firecracker'
  }
  let editor = $state<EditorState | null>(null)
  let feedback = $state('')
  let activeJob = $state<string | null>(null)
  let pending = $state<{ service: Service; action: string } | null>(null)
  let logServiceID = $state<string | null>(null)
  let logTail = $state(200)
  let logLoading = $state(false)
  let logError = $state('')
  let logs = $state<{ text: string; truncated: boolean; source: string } | null>(null)
  let logController: AbortController | undefined
  let controller: AbortController | undefined
  let stopped = false
  const filtered = $derived(inventory?.services.filter(s => `${s.id} ${s.name} ${s.backend}`.toLowerCase().includes(query.toLowerCase())) ?? [])
  const issues = $derived(inventory?.health?.issues ?? [])
  const blocked = $derived(issues.filter(i => i.severity !== 'warning'))

  function openEditor(
    kind: 'container' | 'vm',
    engine?: 'podman' | 'docker',
    backend?: 'quadlet' | 'compose' | 'libvirt' | 'incus' | 'firecracker'
  ) {
    editor = {
      id: '',
      initialKind: kind,
      initialEngine: engine,
      initialBackend: backend
    }
  }

  async function load(recheck = false) {
    controller?.abort(); controller = new AbortController(); const signal = controller.signal
    loading = true; error = ''
    try {
      if (recheck && inventory?.configured && inventory.valid) await invokeTool('managed_config_check', {}, signal)
      const result = await invokeTool<Inventory>('managed_services', {}, signal)
      if (!stopped && !signal.aborted) inventory = result
    } catch (e) { if (!signal.aborted) error = e instanceof Error ? e.message : 'Could not read applications' }
    finally { if (!signal.aborted) loading = false }
  }
  async function readLogs(service: Service) {
    logController?.abort(); logController = new AbortController()
    const signal = logController.signal
    logServiceID = service.id; logs = null; logError = ''; logLoading = true
    try {
      const result = await invokeTool<{ text: string; truncated: boolean; source: string }>('managed_service_logs', { service: service.id, tail: logTail }, signal)
      if (!stopped && !signal.aborted && logServiceID === service.id) logs = result
    } catch (e) {
      if (!signal.aborted) logError = e instanceof Error ? e.message : 'Could not read application logs'
    } finally { if (!signal.aborted) logLoading = false }
  }
  async function perform(service: Service, action: string) {
    error = ''; pending = null
    try { const job = await startJob(`managed_service_${action}`, { service: service.id, ...(action === 'start' ? {} : { confirm: true }) }); activeJob = job.id; feedback = 'Operation started. Follow its progress in Activity.' }
    catch (e) { error = e instanceof Error ? e.message : 'Could not start operation' }
  }
  onMount(() => {
    const finished = (event: Event) => { const job = (event as CustomEvent<Job>).detail; if (job.id !== activeJob) return; activeJob = null; feedback = job.state === 'succeeded' ? 'Operation completed.' : job.error || `Operation ${job.state}.`; void load() }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && editor !== null) {
        editor = null
      }
    }
    window.addEventListener('keydown', onKey)
    window.addEventListener('server-man:job-finished', finished)
    void load()
    return () => { stopped = true; controller?.abort(); logController?.abort(); window.removeEventListener('keydown', onKey); window.removeEventListener('server-man:job-finished', finished) }
  })
</script>

<div class="toolbar">
  <div class="define-actions" role="group" aria-label="Define applications">
    <button class="small-button primary" disabled={!allowWrites || activeJob !== null} onclick={() => openEditor('container', 'podman', 'compose')}>Podman Compose</button>
    <button class="small-button primary" disabled={!allowWrites || activeJob !== null} onclick={() => openEditor('container', 'podman', 'quadlet')}>Podman Quadlet</button>
    <button class="small-button primary" disabled={!allowWrites || activeJob !== null} onclick={() => openEditor('container', 'docker', 'compose')}>Docker Compose</button>
    <button class="small-button primary" disabled={!allowWrites || activeJob !== null} onclick={() => openEditor('vm', undefined, 'incus')}>Incus VM</button>
    <button class="small-button primary" disabled={!allowWrites || activeJob !== null} onclick={() => openEditor('vm', undefined, 'libvirt')}>KVM/Libvirt VM</button>
    <button class="small-button" disabled={!allowWrites || activeJob !== null} onclick={() => openEditor('vm', undefined, 'firecracker')}>Firecracker VM (experimental)</button>
  </div>
  <div class="search-wrap">
    <label class="sr-only" for="app-search">Find an application</label>
    <input id="app-search" type="search" bind:value={query} placeholder="Find an application by name or backend…" />
  </div>
  <button disabled={loading} onclick={() => load(true)}>{loading ? 'Checking…' : 'Check configuration'}</button>
</div>

{#if editor}
  <div class="modal-backdrop" role="presentation" onclick={(e) => { if (e.target === e.currentTarget) editor = null }}>
    <div class="modal-card">
      {#key `${editor.id}-${editor.initialKind}-${editor.initialEngine}-${editor.initialBackend}`}
        <DefinitionEditor
          kind="service"
          resourceID={editor.id}
          initialKind={editor.initialKind}
          initialEngine={editor.initialEngine}
          initialBackend={editor.initialBackend}
          onClose={() => { editor = null }}
          onSaved={(id) => { activeJob = id; editor = null; feedback = 'Definition save started. Follow its validation and apply progress in Activity.' }}
        />
      {/key}
    </div>
  </div>
{/if}
{#if error}<div class="notice error" role="alert"><p>{error}</p><button onclick={() => load(true)}>Retry</button></div>{/if}
{#if feedback}<div class="job-feedback" role="status">{feedback}</div>{/if}
{#if inventory?.error}<section class="panel health-alert" role="alert"><h2>Configuration needs attention</h2><p>{inventory.error}</p></section>{/if}
{#if blocked.length}<section class="panel health-alert" role="alert"><h2>Configuration needs attention</h2><p>Some applications have startup requirements to resolve. Restore missing data and repair host dependencies before starting them.</p></section>{/if}
{#if inventory?.health}<p class="resource-hint">Files checked {new Date(inventory.health.checkedAt).toLocaleString()}. {#if inventory.health.runtimeCheckedAt}Native requirements checked {new Date(inventory.health.runtimeCheckedAt).toLocaleString()}.{/if}</p>{/if}
{#if !allowWrites}<p class="resource-hint">Management writes are disabled. Status and configuration checks remain available.</p>{/if}

{#if inventory && !inventory.error && !inventory.services.length}
  <section class="panel empty-state">
    <h2>No managed applications yet</h2>
    <p>No applications have been defined yet. Choose an application or virtual machine type to configure and create workloads. Configure storage and host bindings in Configuration first.</p>
    <div class="empty-define-actions">
      <button class="primary" disabled={!allowWrites} onclick={() => openEditor('container', 'podman', 'compose')}>Podman Compose</button>
      <button class="primary" disabled={!allowWrites} onclick={() => openEditor('container', 'podman', 'quadlet')}>Podman Quadlet</button>
      <button class="primary" disabled={!allowWrites} onclick={() => openEditor('container', 'docker', 'compose')}>Docker Compose</button>
      <button class="primary" disabled={!allowWrites} onclick={() => openEditor('vm', undefined, 'incus')}>Incus VM</button>
      <button class="primary" disabled={!allowWrites} onclick={() => openEditor('vm', undefined, 'libvirt')}>KVM/Libvirt VM</button>
      <button disabled={!allowWrites} onclick={() => openEditor('vm', undefined, 'firecracker')}>Firecracker VM (experimental)</button>
    </div>
  </section>
{:else if inventory && !inventory.error && inventory.services.length && !filtered.length}
  <section class="panel empty-state">
    <h2>No matching applications</h2>
    <p>Try a different search term or clear the filter.</p>
    <button onclick={() => { query = '' }}>Clear search</button>
  </section>
{/if}

<div class="applications">
{#each filtered as service (service.id)}
  <section class="panel application">
    <header class="app-header">
      <div class="app-title-group">
        <h2>{service.name || service.id}</h2>
        <span class="app-subtitle">{service.kind} · {service.backend}</span>
      </div>
      <span class="project-state" class:running={service.state === 'running'} class:attention={service.state.includes('partially') || service.state === 'failed'}>{service.state.replaceAll('_', ' ')}</span>
    </header>
    <dl class="app-meta">
      <div>
        <dt>Start at boot</dt>
        <dd>{service.enabled ? 'Enabled' : 'Disabled'}</dd>
      </div>
      <div>
        <dt>Configuration</dt>
        <dd>{service.configuration.replaceAll('_', ' ')}</dd>
      </div>
      <div>
        <dt>Registration</dt>
        <dd>{service.registered ? 'Registered' : 'Not registered'}</dd>
      </div>
    </dl>
    {#if service.members?.length}
      <details class="application-members">
        <summary>Containers ({service.members.length})</summary>
        <ul class="member-list">
          {#each service.members as member (member.name)}
            <li>
              <strong>{member.name}</strong>
              <span class="project-state" class:running={member.state === 'running'}>{member.state.replaceAll('_', ' ')}</span>
            </li>
          {/each}
        </ul>
      </details>
    {/if}
    {#if service.issues.length}
      <ul class="issues">
        {#each service.issues as issue}
          <li class="issue-item">
            <strong>{issue.message}</strong>
            <p>{issue.remedy}</p>
          </li>
        {/each}
      </ul>
    {/if}
    {#if service.kind === 'container' || service.backend === 'incus' || service.backend === 'firecracker'}
      <div class="project-log-controls">
        <h3>{service.backend === 'incus' || service.backend === 'firecracker' ? 'VM instance diagnostics' : 'Application logs'}</h3>
        <div>
          <label class="sr-only" for="application-log-tail-{service.id}">Log entries for {service.name || service.id}</label>
          <select id="application-log-tail-{service.id}" bind:value={logTail}>
            {#each [100, 200, 500, 1000, 2000] as count}
              <option value={count}>{count} entries</option>
            {/each}
          </select>
          <button disabled={logLoading && logServiceID === service.id} onclick={() => void readLogs(service)}>
            {logLoading && logServiceID === service.id ? 'Reading…' : logs && logServiceID === service.id ? 'Refresh logs' : 'Read logs'}
          </button>
        </div>
      </div>
      {#if logServiceID === service.id}
        {#if logError}<p class="form-error" role="alert">{logError}</p>{/if}
        {#if logs}<pre class="project-log-output">{logs.text || 'No visible log entries. The workload may not have emitted output or the account may lack access.'}</pre>{#if logs.truncated}<p class="resource-hint">Showing only the most recent 128 KiB of output.</p>{/if}{/if}
        <p class="resource-hint">Logs can contain private application data and are requested only when you click Read logs.{service.backend === 'incus' ? ' Incus reports instance diagnostics, not guest application logs.' : service.backend === 'firecracker' ? ' Firecracker reports VMM and host service diagnostics, not guest web application logs.' : ''}</p>
      {/if}
    {:else if service.kind === 'vm'}
      <p class="resource-hint">Guest application logs are not exposed by this VM backend; inspect them inside the guest.</p>
    {/if}
    <div class="actions">
      <button disabled={!allowWrites || activeJob !== null} onclick={() => { editor = {id:service.id} }}>Edit definition</button>
      {#each ['start', 'stop', 'restart', ...(service.backend === 'firecracker' ? ['force_stop'] : []), service.enabled ? 'disable' : 'enable'] as action}
        <button class:primary={action === 'start'} disabled={!allowWrites || activeJob !== null || loading || ((action === 'start' || action === 'restart' || action === 'enable') && service.issues.some(i => i.severity !== 'warning' && (action === 'enable' || i.field !== 'autostart')))} onclick={() => { if (action === 'start') void perform(service, action); else pending = { service, action } }}>{action === 'enable' ? 'Enable boot startup' : action === 'disable' ? 'Disable boot startup' : action === 'force_stop' ? 'Force stop VM' : action.charAt(0).toUpperCase() + action.slice(1)}</button>
      {/each}
    </div>
  </section>
{/each}
</div>

{#if issues.length}
  <details class="panel all-issues">
    <summary>All configuration issues ({issues.length})</summary>
    <ul class="issues">
      {#each issues as issue}
        <li class="issue-item">
          <strong>{issue.resource}: {issue.message}</strong>
          <p>{issue.remedy}</p>
        </li>
      {/each}
    </ul>
  </details>
{/if}

<ConfirmDialog open={pending !== null} title={pending ? `${pending.action}: ${pending.service.name || pending.service.id}` : ''} description={pending?.action === 'enable' || pending?.action === 'disable' ? 'Change automatic startup at boot. The current running state stays unchanged.' : pending?.action === 'force_stop' ? 'Forcefully terminate this Firecracker VM without waiting for guest shutdown. Unsaved guest data may be lost. Only use this if graceful Stop fails.' : 'This interrupts the selected application.'} label="Confirm" busy={activeJob !== null} onConfirm={() => { if (pending) void perform(pending.service, pending.action) }} onClose={() => { pending = null }} />

<style>
  .toolbar {
    display: flex;
    gap: 12px;
    align-items: center;
    margin-bottom: 24px;
    flex-wrap: wrap;
  }
  .define-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    align-items: center;
  }
  .empty-define-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 16px;
  }
  .search-wrap {
    flex: 1;
    min-width: 180px;
  }
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(5, 9, 15, 0.75);
    backdrop-filter: blur(8px);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
    padding: 16px;
    overflow-y: auto;
  }
  .modal-card {
    width: min(860px, calc(100vw - 32px));
    max-height: calc(100vh - 48px);
    overflow-y: auto;
  }
  .applications {
    display: grid;
    gap: 20px;
  }
  .application {
    min-width: 0;
    padding: 24px;
  }
  .app-header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 16px;
    margin-bottom: 16px;
  }
  .app-title-group h2 {
    font-size: 18px;
    font-weight: 650;
    overflow-wrap: anywhere;
  }
  .app-subtitle {
    font-size: 12px;
    color: var(--muted);
    display: block;
    margin-top: 4px;
  }
  .app-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 24px;
    margin: 16px 0;
    padding: 14px 18px;
    background: var(--panel-subtle);
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-subtle);
  }
  .app-meta dt {
    font-size: 11px;
    color: var(--dim);
    text-transform: uppercase;
    letter-spacing: 0.8px;
    font-weight: 600;
  }
  .app-meta dd {
    margin: 4px 0 0;
    font-size: 13px;
    color: var(--text);
    font-weight: 500;
  }
  .application-members {
    margin: 16px 0;
    background: var(--panel-subtle);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    padding: 12px 16px;
  }
  .application-members summary {
    cursor: pointer;
    font-weight: 600;
    font-size: 12.5px;
    color: var(--text-secondary);
  }
  .member-list {
    list-style: none;
    padding: 0;
    margin: 12px 0 0;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .member-list li {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 6px 10px;
    background: var(--panel);
    border-radius: 4px;
    font-size: 12px;
  }
  .issues {
    list-style: none;
    padding: 0;
    margin: 16px 0;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .issue-item {
    background: var(--amber-dim);
    border: 1px solid var(--amber-border);
    border-radius: var(--radius-sm);
    padding: 12px 16px;
  }
  .issue-item strong {
    color: var(--amber-text);
    font-size: 12.5px;
  }
  .issue-item p {
    color: var(--amber-text-secondary);
    font-size: 12px;
    margin-top: 4px;
  }
  .health-alert {
    border-left: 4px solid var(--amber);
    background: var(--amber-dim);
    border-color: var(--amber-border);
    margin-bottom: 20px;
  }
  .health-alert h2 {
    color: var(--amber-text);
    font-size: 16px;
    margin-bottom: 6px;
  }
  .health-alert p {
    color: var(--amber-text-secondary);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    margin-top: 20px;
    padding-top: 16px;
    border-top: 1px solid var(--border);
  }
  .all-issues {
    margin-top: 24px;
  }
</style>
