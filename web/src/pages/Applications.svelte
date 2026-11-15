<script lang="ts">
  import { onMount } from 'svelte'
  import DefinitionEditor from '../lib/DefinitionEditor.svelte'
  import ConfirmDialog from '../lib/ConfirmDialog.svelte'
  import type { ConfigurationIssue, ConfigurationHealth } from '../lib/managed'
  import { invokeTool, startJob, type Job } from '../lib/api'
  let { allowWrites }: { allowWrites: boolean } = $props()
  type Issue = ConfigurationIssue
  interface Service { id: string; name: string; kind: string; backend: string; enabled: boolean; state: string; registered: boolean; configuration: string; issues: Issue[] }
  interface Inventory { configured: boolean; valid?: boolean; error?: string; services: Service[]; health?: ConfigurationHealth }
  let inventory = $state<Inventory | null>(null)
  let error = $state('')
  let loading = $state(false)
  let query = $state('')
  let editor = $state<{id:string} | null>(null)
  let feedback = $state('')
  let activeJob = $state<string | null>(null)
  let pending = $state<{ service: Service; action: string } | null>(null)
  let controller: AbortController | undefined
  let stopped = false
  const filtered = $derived(inventory?.services.filter(s => `${s.id} ${s.name} ${s.backend}`.toLowerCase().includes(query.toLowerCase())) ?? [])
  const issues = $derived(inventory?.health?.issues ?? [])
  const blocked = $derived(issues.filter(i => i.severity !== 'warning'))
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
  async function perform(service: Service, action: string) {
    error = ''; pending = null
    try { const job = await startJob(`managed_service_${action}`, { service: service.id, ...(action === 'start' ? {} : { confirm: true }) }); activeJob = job.id; feedback = 'Operation started. Follow its progress in Activity.' }
    catch (e) { error = e instanceof Error ? e.message : 'Could not start operation' }
  }
  onMount(() => {
    const finished = (event: Event) => { const job = (event as CustomEvent<Job>).detail; if (job.id !== activeJob) return; activeJob = null; feedback = job.state === 'succeeded' ? 'Operation completed.' : job.error || `Operation ${job.state}.`; void load() }
    window.addEventListener('server-man:job-finished', finished); void load()
    return () => { stopped = true; controller?.abort(); window.removeEventListener('server-man:job-finished', finished) }
  })
</script>

<div class="toolbar"><button disabled={!allowWrites || activeJob !== null} onclick={() => { editor = {id: ''} }}>Define application</button><label>Find an application <input type="search" bind:value={query} placeholder="Name or backend" /></label><button disabled={loading} onclick={() => load(true)}>{loading ? 'Checking…' : 'Check configuration'}</button></div>
{#if editor}{#key editor.id}<DefinitionEditor kind="service" resourceID={editor.id} onClose={() => { editor = null }} onSaved={(id) => { activeJob = id; editor = null; feedback = 'Definition save started. Follow its validation and apply progress in Activity.' }} />{/key}{/if}
{#if error}<p class="form-error" role="alert">{error}</p>{/if}
{#if feedback}<p role="status">{feedback}</p>{/if}
{#if inventory?.error}<section class="panel" role="alert"><h2>Configuration needs attention</h2><p>{inventory.error}</p></section>{/if}
{#if blocked.length}<section class="panel health-alert" role="alert"><h2>Configuration needs attention</h2><p>Some applications have startup requirements to resolve. Restore missing data and repair host dependencies before starting them.</p></section>{/if}
{#if inventory?.health}<p class="resource-hint">Files checked {new Date(inventory.health.checkedAt).toLocaleString()}. {#if inventory.health.runtimeCheckedAt}Native requirements checked {new Date(inventory.health.runtimeCheckedAt).toLocaleString()}.{/if}</p>{/if}
{#if !allowWrites}<p class="resource-hint">Management writes are disabled. Status and configuration checks remain available.</p>{/if}
{#if inventory && !inventory.error && !inventory.services.length}<section class="panel empty-state"><h2>No managed applications yet</h2><p>No applications have been defined yet. Use Define application to create a stopped container application or virtual machine. Configure storage and host bindings in Configuration first.</p></section>
{:else if inventory && !inventory.error && inventory.services.length && !filtered.length}<section class="panel empty-state"><h2>No matching applications</h2><p>Try a different search.</p></section>{/if}
<div class="applications">
{#each filtered as service (service.id)}
  <section class="panel application">
    <header><div><h2>{service.name || service.id}</h2><p>{service.kind} · {service.backend}</p></div><strong>{service.state}</strong></header>
    <dl><div><dt>Start at boot</dt><dd>{service.enabled ? 'Enabled' : 'Disabled'}</dd></div><div><dt>Configuration</dt><dd>{service.configuration.replaceAll('_', ' ')}</dd></div></dl>
    {#if service.issues.length}<ul class="issues">{#each service.issues as issue}<li><strong>{issue.message}</strong><p>{issue.remedy}</p></li>{/each}</ul>{/if}
    <div class="actions">
      <button disabled={!allowWrites || activeJob !== null} onclick={() => { editor = {id:service.id} }}>Edit definition</button>
      {#each ['start', 'stop', 'restart', service.enabled ? 'disable' : 'enable'] as action}
        <button disabled={!allowWrites || activeJob !== null || loading || ((action === 'start' || action === 'restart' || action === 'enable') && service.issues.some(i => i.severity !== 'warning' && (action === 'enable' || i.field !== 'autostart')))} onclick={() => { if (action === 'start') void perform(service, action); else pending = { service, action } }}>{action === 'enable' ? 'Enable boot startup' : action === 'disable' ? 'Disable boot startup' : action.charAt(0).toUpperCase() + action.slice(1)}</button>
      {/each}
    </div>
  </section>
{/each}
</div>
{#if issues.length}<details class="panel"><summary>All configuration issues ({issues.length})</summary><ul class="issues">{#each issues as issue}<li><strong>{issue.resource}: {issue.message}</strong><p>{issue.remedy}</p></li>{/each}</ul></details>{/if}
<ConfirmDialog open={pending !== null} title={pending ? `${pending.action}: ${pending.service.name || pending.service.id}` : ''} description={pending?.action === 'enable' || pending?.action === 'disable' ? 'Change automatic startup at boot. The current running state stays unchanged.' : 'This interrupts the selected application.'} label="Confirm" busy={activeJob !== null} onConfirm={() => { if (pending) void perform(pending.service, pending.action) }} onClose={() => { pending = null }} />

<style>
  .toolbar, header, .actions { display: flex; gap: 1rem; justify-content: space-between; flex-wrap: wrap; align-items: center; }
  .toolbar { margin-bottom: 1rem; }
  label { display: grid; gap: .5rem; }
  input { max-width: 100%; }
  .applications { display: grid; gap: 1rem; }
  .application { min-width: 0; }
  h2, p, dd, strong { overflow-wrap: anywhere; }
  header p { margin-top: .4rem; }
  dl { display: flex; flex-wrap: wrap; gap: 2rem; }
  dd { margin: .4rem 0; }
  .issues { padding-left: 1.3rem; }
  .issues p { margin-top: .3rem; }
  .health-alert { border-left: 3px solid #e0a25b; margin-bottom: 1rem; }
  details { margin-top: 1rem; }
</style>
