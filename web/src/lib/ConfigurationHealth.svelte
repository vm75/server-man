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
  const uncertain = $derived(issues.some(issue => issue.severity === 'unknown'))

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
  <div role="status">
    <strong>{!readiness ? 'Readiness not checked' : readiness.error ? 'Workload configuration needs repair' : error || uncertain ? 'Configuration readiness is uncertain' : 'Workload configuration alert'}</strong>
    {#if checking}<p>Checking configuration requirements…</p>{/if}
    {#if error}<p class="form-error">{error} Previous issues remain visible until checks succeed.</p>{/if}
    {#if readiness?.error}<p>{readiness.error}</p>{/if}
    {#if issues.length}<p>{issues.length} configuration {issues.length === 1 ? 'issue requires' : 'issues require'} attention. Repair the affected requirements before starting workloads.</p>{/if}
    {#if readiness?.health}<p class="resource-hint">Files checked {new Date(readiness.health.checkedAt).toLocaleString()}. {#if readiness.health.runtimeCheckedAt}Native requirements checked {new Date(readiness.health.runtimeCheckedAt).toLocaleString()}.{/if}</p>{/if}
  </div>
  <div class="health-actions">
    <button disabled={checking} onclick={() => load(true)}>Recheck requirements</button>
    <button onclick={() => navigate('configuration')}>Open Configuration</button>
    {#if issues.length}<button onclick={() => navigate('applications')}>View affected applications</button><button onclick={() => navigate('updates')}>Manage host dependencies</button>{/if}
  </div>
  {#if issues.length}<details><summary>View all configuration issues ({issues.length})</summary><ul>{#each issues as issue}<li><strong>{issue.resource} · {issue.severity}</strong><p>{issue.message}</p><p>{issue.remedy}</p><small>Field: {issue.field}</small></li>{/each}</ul></details>{/if}
</section>
{/if}

<style>
  .configuration-health { border-left: 3px solid #e0a25b; margin-bottom: 1rem; min-width: 0; overflow-wrap: anywhere; }
  .health-actions { display: flex; flex-wrap: wrap; gap: .5rem; margin: 1rem 0; }
  details { margin-top: .75rem; }
  summary { cursor: pointer; }
  ul { padding-left: 1.25rem; }
  li { margin-top: .75rem; }
  p { margin: .4rem 0; }
</style>
