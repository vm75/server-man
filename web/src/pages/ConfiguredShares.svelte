<script lang="ts">
  import { onMount } from 'svelte'
  import DefinitionEditor from '../lib/DefinitionEditor.svelte'
  import ConfirmDialog from '../lib/ConfirmDialog.svelte'
  import { invokeTool, startJob, type Job } from '../lib/api'
  import type { ShareDefinition } from '../lib/managed'
  let { allowWrites }: { allowWrites: boolean } = $props()
  interface ServerStatus { available: boolean }
  interface ShareItem { id: string; definition: ShareDefinition; configuration: string; issues: { message: string; remedy: string; code?: string }[] }
  let shares = $state<ShareItem[]>([])
  let servers = $state<Record<string, ServerStatus>>({})
  let editor = $state<{ id: string } | null>(null)
  let error = $state('')
  let feedback = $state('')
  let loading = $state(false)
  let activeJob = $state<string | null>(null)
  let pending = $state<ShareItem | null>(null)
  let setupPending = $state(false)
  let protocol = $state('samba')
  let controller: AbortController | undefined
  let stopped = false

  const neededProtocols = $derived(Array.from(new Set(shares.map(s => s.definition.protocol))))
  const unavailableProtocols = $derived(
    neededProtocols.filter(p => {
      if (servers[p] !== undefined) return !servers[p].available
      return shares.some(s => s.definition.protocol === p && s.issues.some(i => i.code === 'share_setup'))
    })
  )

  $effect(() => {
    if (unavailableProtocols.length > 0 && !unavailableProtocols.includes(protocol)) {
      protocol = unavailableProtocols[0]
    }
  })

  async function load() {
    controller?.abort(); controller = new AbortController()
    const signal = controller.signal
    loading = true; error = ''
    try {
      const result = await invokeTool<{ shares: ShareItem[]; servers?: Record<string, ServerStatus> }>('managed_shares', {}, signal)
      if (!stopped && !signal.aborted) {
        shares = result.shares
        if (result.servers) servers = result.servers
      }
    }
    catch (e) { if (!signal.aborted) error = e instanceof Error ? e.message : 'Could not read file shares' }
    finally { if (!signal.aborted) loading = false }
  }
  async function publish() {
    const share = pending
    if (!share) return
    pending = null
    try { const job = await startJob('managed_share_publish', { share: share.id, published: !share.definition.enabled, confirm: true }); activeJob = job.id; feedback = 'Share publication change started.' }
    catch (e) { error = e instanceof Error ? e.message : 'Could not change publication' }
  }
  async function setup() {
    setupPending = false
    try { const job = await startJob('shares_setup', { protocol, confirm: true }); activeJob = job.id; feedback = 'Host share-server setup started.' }
    catch (e) { error = e instanceof Error ? e.message : 'Could not start host setup' }
  }
  onMount(() => {
    const finished = (e: Event) => {
      const job = (e as CustomEvent<Job>).detail
      if (job.id !== activeJob) return
      activeJob = null
      feedback = job.state === 'succeeded' ? 'Share configuration applied. Existing clients may retain earlier access until reconnecting.' : job.error || `Operation ${job.state}.`
      void load()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && editor !== null) {
        editor = null
      }
    }
    window.addEventListener('keydown', onKey)
    window.addEventListener('server-man:job-finished', finished)
    void load()
    return () => { stopped = true; controller?.abort(); window.removeEventListener('keydown', onKey); window.removeEventListener('server-man:job-finished', finished) }
  })
</script>

<div class="shares-toolbar">
  <button class="primary" disabled={!allowWrites || activeJob !== null} onclick={() => { editor = { id: '' } }}>Define file share</button>
  <button disabled={loading} onclick={load}>{loading ? 'Reading…' : 'Refresh shares'}</button>
</div>

{#if error}<div class="notice error" role="alert"><p>{error}</p><button onclick={load}>Retry</button></div>{/if}
{#if feedback}<div class="job-feedback" role="status">{feedback}</div>{/if}

{#if unavailableProtocols.length > 0}
  <section class="panel host-server-card">
    <h2>Host share server</h2>
    <p>Install and configure the native server as an explicit host operation, separate from share definitions.</p>
    <div class="host-server-controls">
      <label class="protocol-select">
        <span>Server protocol</span>
        <select bind:value={protocol}>
          {#each unavailableProtocols as p}
            <option value={p}>{p === 'samba' ? 'Samba' : 'NFS'}</option>
          {/each}
        </select>
      </label>
      <button disabled={!allowWrites || activeJob !== null} onclick={() => { setupPending = true }}>Set up server…</button>
    </div>
  </section>
{/if}

<p class="resource-hint intro-hint">Definitions come from server-man configuration. Host packages, daemon setup, accounts and directory permissions are managed separately.</p>

{#if editor}
  <div class="modal-backdrop" role="presentation" onclick={(e) => { if (e.target === e.currentTarget) editor = null }}>
    <div class="modal-card">
      {#key editor.id}
        <DefinitionEditor kind="share" resourceID={editor.id} onClose={() => { editor = null }} onSaved={(id) => { editor = null; activeJob = id; feedback = 'Share definition save started. Follow Activity for validation and publication.' }} />
      {/key}
    </div>
  </div>
{/if}

{#if !shares.length && !loading}
  <section class="panel empty-state">
    <h2>No configured file shares</h2>
    <p>Add directory storage in Configuration, then define a Samba or NFS share.</p>
    <button class="primary" disabled={!allowWrites} onclick={() => { editor = { id: '' } }}>Define file share</button>
  </section>
{/if}

{#if shares.length}
  <div class="share-grid">
    {#each shares as share (share.id)}
      <section class="panel share-card">
        <header class="share-header">
          <div>
            <h2>{share.id}</h2>
            <span class="share-meta-line">{share.definition.protocol.toUpperCase()} · storage: {share.definition.storage}</span>
          </div>
          <span class="project-state" class:running={share.definition.enabled}>{share.configuration}</span>
        </header>

        <div class="share-status-chips">
          <span class="status-chip" class:enabled={share.definition.enabled}>
            {share.definition.enabled ? 'Publication enabled' : 'Publication disabled'}
          </span>
          <span class="status-chip">
            {share.definition.readOnly ? 'Read only' : 'Read and write'}
          </span>
        </div>

        {#if share.issues.length}
          <ul class="issues">
            {#each share.issues as issue}
              <li class="issue-item">
                <strong>{issue.message}</strong>
                <p>{issue.remedy}</p>
              </li>
            {/each}
          </ul>
        {/if}

        <div class="share-card-actions">
          <button disabled={!allowWrites || activeJob !== null} onclick={() => { editor = { id: share.id } }}>Edit share</button>
          <button disabled={!allowWrites || activeJob !== null} onclick={() => { pending = share }}>
            {share.definition.enabled ? 'Unpublish share' : 'Publish share'}
          </button>
        </div>
      </section>
    {/each}
  </div>
{/if}

<ConfirmDialog open={pending !== null} title={pending ? `${pending.definition.enabled ? 'Unpublish' : 'Publish'}: ${pending.id}` : ''} description="Update the authoritative share publication setting. Shared data and host daemon enablement are preserved. Existing clients may keep prior settings until reconnecting." label="Apply publication" busy={activeJob !== null} onConfirm={publish} onClose={() => { pending = null }} />
<ConfirmDialog open={setupPending} title="Set up server" description="Install native server packages, prepare owned include/export setup, and enable/start the host server. Existing configuration and shared data are preserved. Credentials, firewall and directory permissions remain explicit administrator setup." label="Set up server" busy={activeJob !== null} onConfirm={setup} onClose={() => { setupPending = false }} />

<style>
  .shares-toolbar {
    display: flex;
    gap: 12px;
    align-items: center;
    margin-bottom: 24px;
    flex-wrap: wrap;
  }
  .host-server-card {
    margin-bottom: 16px;
  }
  .host-server-card h2 {
    font-size: 17px;
    margin-bottom: 6px;
  }
  .host-server-controls {
    display: flex;
    align-items: flex-end;
    gap: 14px;
    margin-top: 16px;
    flex-wrap: wrap;
  }
  .protocol-select {
    display: grid;
    gap: 6px;
    min-width: 160px;
    margin-bottom: 0;
  }
  .intro-hint {
    margin: 8px 0 24px;
  }
  .share-header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 16px;
    margin-bottom: 14px;
  }
  .share-header h2 {
    font-size: 18px;
    overflow-wrap: anywhere;
  }
  .share-meta-line {
    font-size: 12px;
    color: var(--muted);
    display: block;
    margin-top: 3px;
  }
  .share-status-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin: 14px 0 16px;
  }
  .status-chip {
    font-size: 11px;
    font-weight: 550;
    padding: 3px 8px;
    border-radius: 4px;
    background: var(--panel-subtle);
    border: 1px solid var(--border-subtle);
    color: var(--muted);
  }
  .status-chip.enabled {
    background: var(--accent-dim);
    border-color: var(--accent-border);
    color: var(--accent);
  }
  .issues {
    list-style: none;
    padding: 0;
    margin: 14px 0;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .issue-item {
    background: var(--amber-dim);
    border: 1px solid var(--amber-border);
    border-radius: var(--radius-sm);
    padding: 10px 14px;
  }
  .issue-item strong {
    color: #fde68a;
    font-size: 12px;
  }
  .issue-item p {
    color: #fef3c7;
    font-size: 11.5px;
    margin-top: 3px;
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
    width: min(680px, calc(100vw - 32px));
    max-height: calc(100vh - 48px);
    overflow-y: auto;
  }
</style>
