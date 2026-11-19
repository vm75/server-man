<script lang="ts">
  import { onMount } from 'svelte'
  import { invokeTool } from './api'
  import type { ConfigurationReadiness } from './managed'

  let { navigate }: { navigate: (page: string) => void } = $props()
  let readiness = $state<ConfigurationReadiness | null>(null)
  let checking = $state(true)
  let error = $state('')
  let controller: AbortController | undefined
  let stopped = false
  let queued = false
  const issues = $derived(readiness?.health?.issues ?? [])
  const blockers = $derived(issues.filter(issue => issue.severity === 'error'))
  const unknown = $derived(issues.filter(issue => issue.severity === 'unknown'))
  const warnings = $derived(issues.filter(issue => issue.severity === 'warning'))

  async function load(refresh = false) {
    if (controller) { queued = true; return }
    controller = new AbortController()
    const signal = controller.signal
    checking = true
    try {
      if (refresh && readiness?.configured && readiness.valid) await invokeTool('managed_config_check', {}, signal)
      const next = await invokeTool<ConfigurationReadiness>('managed_config_readiness', {}, signal)
      if (!stopped) { readiness = next; error = '' }
    } catch (e) {
      if (!stopped && !signal.aborted) error = e instanceof Error ? e.message : 'Configuration checks could not complete.'
    } finally {
      controller = undefined
      if (!stopped) {
        checking = false
        if (queued) { queued = false; void load() }
      }
    }
  }

  onMount(() => {
    const changed = () => { void load() }
    const visible = () => { if (!document.hidden) void load() }
    window.addEventListener('server-man:job-finished', changed)
    document.addEventListener('visibilitychange', visible)
    const timer = window.setInterval(visible, 60_000)
    void load()
    return () => {
      stopped = true; controller?.abort(); window.clearInterval(timer)
      window.removeEventListener('server-man:job-finished', changed)
      document.removeEventListener('visibilitychange', visible)
    }
  })
</script>

{#if !readiness || error || readiness.error || issues.length}
<section class="panel configuration-health" aria-label="Configuration readiness" aria-busy={checking}>
  <div role="status" class="health-status">
    <strong class="health-title">{!readiness ? 'Readiness not checked' : readiness.error ? 'Workload configuration needs repair' : error ? 'Configuration readiness is uncertain' : blockers.length ? 'Workload configuration alert' : unknown.length ? 'Configuration readiness is uncertain' : 'Configuration warnings'}</strong>
    {#if checking}<p class="health-msg">Checking configuration requirements…</p>{/if}
    {#if error}<p class="form-error">{error} Previous issues remain visible until checks succeed.</p>{/if}
    {#if readiness?.error}<p class="health-msg">{readiness.error}</p>{/if}
    {#if blockers.length}<p class="health-msg">{blockers.length} startup {blockers.length === 1 ? 'blocker' : 'blockers'} found. Repair affected requirements before starting those workloads.</p>{/if}
    {#if unknown.length}<p class="health-msg">{unknown.length} {unknown.length === 1 ? 'readiness check is' : 'readiness checks are'} inconclusive. Recheck when the provider is available.</p>{/if}
    {#if !blockers.length && !unknown.length && warnings.length}<p class="health-msg">{warnings.length} configuration {warnings.length === 1 ? 'warning' : 'warnings'} to review. Warnings alone do not prevent workloads from starting.</p>{/if}
    {#if readiness?.health}<p class="resource-hint">Files checked {new Date(readiness.health.checkedAt).toLocaleString()}. {#if readiness.health.runtimeCheckedAt}Native requirements checked {new Date(readiness.health.runtimeCheckedAt).toLocaleString()}.{/if}</p>{/if}
  </div>

  <div class="health-actions">
    <button disabled={checking} onclick={() => load(true)}>Recheck requirements</button>
    <button onclick={() => navigate('configuration')}>Open Configuration</button>
    {#if issues.length}
      <button onclick={() => navigate('applications')}>View affected applications</button>
      <button onclick={() => navigate('updates')}>Manage host dependencies</button>
    {/if}
  </div>

  {#if issues.length}
    <details class="health-details">
      <summary>View all configuration issues ({issues.length})</summary>
      <ul class="health-issues-list">
        {#each issues as issue}
          <li class="health-issue-card">
            <strong>{issue.resource} · {issue.severity}</strong>
            <p>{issue.message}</p>
            <p>{issue.remedy}</p>
            <small>Field: {issue.field}</small>
          </li>
        {/each}
      </ul>
    </details>
  {/if}
</section>
{/if}

<style>
  .configuration-health {
    border-left: 4px solid var(--amber);
    background: var(--amber-dim);
    border-color: var(--amber-border);
    margin-bottom: 24px;
    padding: 20px 24px;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .health-title {
    font-size: 15px;
    color: var(--amber-text);
    display: block;
    margin-bottom: 6px;
  }
  .health-msg {
    color: var(--amber-text-secondary);
    margin: 4px 0;
    font-size: 13px;
  }
  .health-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 16px;
  }
  .health-details {
    margin-top: 16px;
    padding-top: 14px;
    border-top: 1px solid var(--amber-border);
  }
  .health-details summary {
    cursor: pointer;
    font-weight: 600;
    font-size: 13px;
    color: var(--amber-text);
    outline: none;
  }
  .health-issues-list {
    list-style: none;
    padding: 0;
    margin: 12px 0 0;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .health-issue-card {
    background: var(--panel);
    border: 1px solid var(--amber-border);
    border-radius: var(--radius-sm);
    padding: 10px 14px;
  }
  .health-issue-card strong {
    color: var(--amber-text);
    font-size: 12px;
  }
  .health-issue-card p {
    color: var(--text-secondary);
    font-size: 11.5px;
    margin: 3px 0;
  }
  .health-issue-card small {
    color: var(--dim);
    font-size: 10px;
    font-family: var(--mono);
  }
</style>
