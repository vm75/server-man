<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import ConfirmDialog from '../lib/ConfirmDialog.svelte'
  import { invokeTool, startJob, type Job } from '../lib/api'
  import { serviceActions, type Service, type ServiceInventory, type ServiceDetails, type ServiceLogs } from '../lib/services'

  let { allowWrites }: { allowWrites: boolean } = $props()
  let inventory = $state<ServiceInventory | null>(null), loading = $state(true), error = $state('')
  let scope = $state('system')
  let filter = $state(''), stateFilter = $state('all'), selectedName = $state('')
  let details = $state<ServiceDetails | null>(null), detailLoading = $state(false), detailError = $state('')
  let logs = $state<ServiceLogs | null>(null), logLoading = $state(false), logError = $state(''), tail = $state(200)
  let activeJob = $state<string | null>(null), submitting = $state(false), feedback = $state(''), failed = $state(false)
  let pending = $state<{ unit: string; action: string } | null>(null)
  let stopped = false, listController: AbortController | undefined, detailController: AbortController | undefined, logController: AbortController | undefined
  const services = $derived(inventory?.services.filter(s => (stateFilter === 'all' || s.active === stateFilter) && `${s.name} ${s.description}`.toLowerCase().includes(filter.toLowerCase())) ?? [])
  const selected = $derived(inventory?.services.find(s => s.name === selectedName))
  const canWrite = $derived(allowWrites && !!inventory?.available && !submitting && activeJob === null)
  const activeCount = $derived(inventory?.services.filter(s => s.active === 'active').length ?? 0)
  const failedCount = $derived(inventory?.services.filter(s => s.active === 'failed').length ?? 0)

  async function load() {
    if (stopped) return
    listController?.abort(); listController = new AbortController()
    const controller = listController
    loading = true; error = ''
    try { const result = await invokeTool<ServiceInventory>('services_list', { scope }, controller.signal); if (stopped || controller.signal.aborted) return; inventory = result; if (selectedName && !result.services.some(s => s.name === selectedName) && !result.warnings.length) { selectedName = ''; details = null } }
    catch (e) { if (!controller.signal.aborted) error = e instanceof Error ? e.message : 'Could not list services' }
    finally { if (!controller.signal.aborted) loading = false }
  }

  function changeScope() {
    detailController?.abort(); logController?.abort()
    selectedName = ''; details = null; logs = null; pending = null
    detailError = ''; logError = ''; feedback = ''; inventory = null
    detailLoading = false; logLoading = false
    void load()
  }

  async function inspect(service: Service) {
    if (stopped) return
    detailController?.abort(); detailController = new AbortController()
    const controller = detailController
    if (selectedName !== service.name) { details = null; logs = null; logError = ''; logController?.abort(); logLoading = false }
    selectedName = service.name; detailLoading = true; detailError = ''
    try {
      const result = await invokeTool<ServiceDetails>('services_details', { unit: service.name, scope }, controller.signal)
      if (stopped || controller.signal.aborted || selectedName !== service.name) return
      details = result
      // Keep the selected alias as the request identity; the backend can return its canonical Id.
      if (inventory) inventory.services = inventory.services.map(s => s.name === service.name ? { ...s, ...result, name: s.name } : s)
    } catch (e) { if (!controller.signal.aborted) { details = null; detailError = e instanceof Error ? e.message : 'Could not read service details' } }
    finally { if (!controller.signal.aborted) detailLoading = false }
  }

  async function readLogs() {
    if (!selected || stopped) return
    const name = selected.name
    logController?.abort(); logController = new AbortController()
    const controller = logController
    logLoading = true; logError = ''; logs = null
    try { const result = await invokeTool<ServiceLogs>('services_logs', { unit: name, tail, scope }, controller.signal); if (!stopped && !controller.signal.aborted && selectedName === name) logs = result }
    catch (e) { if (!controller.signal.aborted) logError = e instanceof Error ? e.message : 'Could not read journal' }
    finally { if (!controller.signal.aborted) logLoading = false }
  }

  async function perform(unit: string, action: string) {
    submitting = true; error = ''; feedback = ''; failed = false
    try { const args: Record<string, unknown> = { unit, scope }; if (!['start', 'enable'].includes(action)) args.confirm = true; const job = await startJob(`services_${action}`, args); activeJob = job.id; feedback = 'Service job started. Follow its output in Activity.' }
    catch (e) { error = e instanceof Error ? e.message : 'Could not start service job' }
    finally { submitting = false; pending = null }
  }
  function action(unit: string, action: string) { if (['start', 'enable'].includes(action)) void perform(unit, action); else pending = { unit, action } }
  function confirmation() { if (!pending) return ''; if (pending.action === 'disable') return `Disable startup links for ${pending.unit}. The current service is not stopped. Other units, sockets or timers may still activate it.`; return `${serviceActions[pending.action]} ${pending.unit} in the ${inventory?.scope ?? 'configured'} manager. This can interrupt its workload.` }
  function blocked(action: string) { return !canWrite || (details !== null && ((action === 'start' && !details.canStart) || (action === 'stop' && !details.canStop) || (action === 'reload' && !details.canReload))) }

  onMount(() => {
    const finished = async (event: Event) => {
      const job = (event as CustomEvent<Job>).detail
      if (job.id !== activeJob) return
      activeJob = null; failed = job.state !== 'succeeded'; feedback = failed ? job.error || `Operation ${job.state}.` : 'Service operation completed.'
      const service = selected
      await load()
      if (!stopped && service && selectedName === service.name) await inspect(service)
    }
    window.addEventListener('server-man:job-finished', finished); void load()
    return () => { stopped = true; listController?.abort(); detailController?.abort(); logController?.abort(); window.removeEventListener('server-man:job-finished', finished) }
  })
</script>

<div class="compose-toolbar">
  <div class="service-summary">
    <strong>{inventory?.services.length ?? '—'} services</strong>
    <span class="service-running">{activeCount} active</span>
    <span class:service-failed={failedCount > 0}>{failedCount} failed</span>
  </div>
  <div class="compose-search">
    <label class="sr-only" for="service-scope">Service manager</label>
    <select id="service-scope" bind:value={scope} onchange={changeScope} disabled={submitting || activeJob !== null}>
      <option value="system">System services</option>
      <option value="user">My services</option>
    </select>
    <label class="sr-only" for="service-filter">Filter services</label>
    <input id="service-filter" type="search" placeholder="Find a service…" bind:value={filter} />
    <label class="sr-only" for="service-state">Service state</label>
    <select id="service-state" bind:value={stateFilter}>
      <option value="all">All states</option>
      <option value="active">Active</option>
      <option value="inactive">Inactive</option>
      <option value="failed">Failed</option>
      <option value="activating">Activating</option>
      <option value="unknown">Unknown</option>
    </select>
    <button onclick={load} disabled={loading}>{loading ? 'Refreshing…' : 'Refresh'}</button>
  </div>
</div>

{#if !allowWrites}<div class="policy-note"><span class="connection-dot"></span>Read only. Management writes can be enabled in the server configuration.</div>{/if}
{#if error}<div class="notice error compose-notice" role="alert"><p>{error}</p><button onclick={load}>Retry</button></div>{/if}
{#if feedback}<div class="job-feedback" class:failed role="status">{feedback}</div>{/if}

{#if inventory}
  <div class="compose-root">
    <span>{inventory.scope === 'user' ? 'Current account’s user manager' : 'System manager'}</span>
    <span class="capability-state" class:available={inventory.available}>{inventory.available ? 'systemctl installed' : 'Unavailable'}</span>
  </div>
  {#if inventory.reason}<div class="backend-notice" role="status">{inventory.reason}</div>{/if}
  {#each inventory.warnings as warning}<div class="backend-notice" role="status">{warning}</div>{/each}
{/if}

{#if loading && !inventory}
  <section class="panel empty-state" aria-busy="true"><span class="loading-ring"></span><h2>Reading systemd services</h2><p>Combining installed units and runtime state…</p></section>
{:else if inventory && !services.length}
  <section class="panel empty-state">
    <span class="module-icon"><Icon name="services" size={28} /></span>
    <h2>{filter || stateFilter !== 'all' ? 'No matching services' : inventory.warnings.length || !inventory.available ? 'Service inventory unavailable' : 'No services found'}</h2>
    <p>{filter || stateFilter !== 'all' ? 'Try another name or state filter.' : 'Inventory follows the configured system or user manager and server account permissions.'}</p>
  </section>
{:else}
  <div class="service-layout" class:has-selection={!!selected}>
    <section class="panel service-list" aria-label="Services">
      {#each services as service}
        <button class="service-list-row" class:selected={selectedName === service.name} onclick={() => inspect(service)}>
          <span class="service-list-identity">
            <strong>{service.name}</strong>
            <span>{service.description || 'Open details to inspect this unit'}</span>
          </span>
          <span class="service-list-state">
            <span class="project-state" class:running={service.active === 'active'} class:attention={service.active === 'failed'}>{service.active}</span>
            <span>{service.enabled || 'unknown'}</span>
          </span>
        </button>
      {/each}
    </section>

    {#if selected}
      <section class="panel project-details service-details" aria-labelledby="service-heading">
        <div class="project-detail-heading">
          <div>
            <div class="eyebrow">SYSTEMD SERVICE</div>
            <h2 id="service-heading">{selected.name}</h2>
          </div>
          <button class="small-button" disabled={detailLoading} onclick={() => inspect(selected)}>Refresh</button>
        </div>
        <p class="service-description">{details?.description || selected.description}</p>
        <div class="project-actions">
          {#each ['start', 'stop', 'restart', 'reload', 'enable', 'disable'] as value}
            <button class:primary={value === 'start'} class:danger-button={value === 'stop'} disabled={blocked(value)} onclick={() => action(selected.name, value)}>{serviceActions[value]}</button>
          {/each}
        </div>
        <p class="resource-hint">Enable/disable change startup links only. They do not start or stop the running service.</p>

        {#if detailError}
          <p class="form-error" role="alert">{detailError}</p>
        {:else if detailLoading}
          <p class="resource-hint">Reading service properties…</p>
        {/if}

        {#if details?.managedApplication}
          <p class="resource-hint">Managed by application {details.managedApplication}. Lifecycle and startup changes apply to that application; edit its definition in Applications.</p>
        {/if}

        {#if details}
          <dl class="machine-facts">
            <div>
              <dt>Runtime</dt>
              <dd>{details.active} / {details.sub}</dd>
            </div>
            <div>
              <dt>Loaded</dt>
              <dd>{details.load}</dd>
            </div>
            <div>
              <dt>Startup</dt>
              <dd>{details.enabled || 'Not reported'}</dd>
            </div>
            {#if details.name !== selected.name}
              <div>
                <dt>Canonical unit</dt>
                <dd>{details.name}</dd>
              </div>
            {/if}
            {#if details.mainPID !== undefined}
              <div>
                <dt>Main PID</dt>
                <dd>{details.mainPID || 'Not running'}</dd>
              </div>
            {/if}
            {#if details.result}
              <div>
                <dt>Result</dt>
                <dd>{details.result}{details.execMainStatus ? ` · exit ${details.execMainStatus}` : ''}</dd>
              </div>
            {/if}
            {#if details.activeSince}
              <div>
                <dt>Active since</dt>
                <dd>{details.activeSince}</dd>
              </div>
            {/if}
            {#if details.fragmentPath}
              <div>
                <dt>Unit path</dt>
                <dd class="mono">{details.fragmentPath}</dd>
              </div>
            {/if}
          </dl>
        {/if}

        <div class="project-log-controls">
          <h3>Service journal</h3>
          <div>
            <label class="sr-only" for="service-log-tail">Journal entries</label>
            <select id="service-log-tail" bind:value={tail}>
              {#each [100, 200, 500, 1000, 2000] as count}
                <option value={count}>{count} entries</option>
              {/each}
            </select>
            <button onclick={readLogs} disabled={logLoading || !inventory?.journalAvailable}>{logLoading ? 'Reading…' : logs ? 'Refresh logs' : 'Read logs'}</button>
          </div>
        </div>

        {#if inventory && !inventory.journalAvailable}<p class="resource-hint">journalctl is not installed.</p>{/if}
        {#if logError}<p class="form-error" role="alert">{logError}</p>{/if}
        {#if logs}
          <pre class="project-log-output">{logs.text || 'No visible journal entries. The service may have no recent output, or this account may lack journal access.'}</pre>
          {#if logs.truncated}<p class="resource-hint">Showing the most recent 128 KiB of journal output.</p>{/if}
        {/if}
      </section>
    {/if}
  </div>
{/if}

<ConfirmDialog open={pending !== null} title={pending ? `${serviceActions[pending.action]}: ${pending.unit}` : ''} description={confirmation()} label={pending ? serviceActions[pending.action] : 'Confirm'} busy={submitting} onConfirm={() => { if (pending) void perform(pending.unit, pending.action) }} onClose={() => { pending = null }} />

<style>
  .service-description {
    font-size: 13.5px;
    margin: 8px 0 16px;
    line-height: 1.5;
  }
</style>
