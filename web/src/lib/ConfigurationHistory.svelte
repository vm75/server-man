<script lang="ts">
  import { onMount } from 'svelte'
  import { invokeTool, startJob } from './api'
  import type { ImpactPlan } from './managed'
  interface Entry { revision: string; modifiedAt: string; valid: boolean; error?: string }
  let { revision, disabled, onStarted, onInspect }: { revision: string; disabled: boolean; onStarted: (jobID:string) => void; onInspect:(content:string) => void } = $props()
  let entries = $state<Entry[]>([])
  let error = $state('')
  let busy = $state(false)
  let selected = $state<{revision:string;content:string;valid:boolean} | null>(null)
  let plan = $state<ImpactPlan | null>(null)
  let mode = $state('next_start')
  let controller: AbortController | undefined
  let stopped = false
  async function load() {
    busy = true; error = ''; controller?.abort(); controller = new AbortController()
    try { const result = await invokeTool<{entries:Entry[]}>('managed_config_history',{},controller.signal); if (!stopped) entries = result.entries }
    catch(e) { if (!stopped) error = e instanceof Error ? e.message : 'Could not read history' }
    finally { if (!stopped) busy = false }
  }
  async function inspect(target:string) {
    busy = true; error = ''; plan = null
    try { selected = await invokeTool('managed_config_history_read',{target},controller?.signal) }
    catch(e) { error = e instanceof Error ? e.message : 'Could not inspect revision' }
    finally { busy = false }
  }
  async function review() {
    if (!selected) return
    busy = true; error = ''
    try { const result = await invokeTool<{plan:ImpactPlan}>('managed_config_revert_plan',{target:selected.revision,revision},controller?.signal); plan = result.plan }
    catch(e) { error = e instanceof Error ? e.message : 'Could not review revert' }
    finally { busy = false }
  }
  async function revert() {
    if (!selected || !plan) return
    busy = true; error = ''
    try { const job = await startJob('managed_config_revert',{target:selected.revision,revision,mode,confirm:true}); plan = null; onStarted(job.id) }
    catch(e) { error = e instanceof Error ? e.message : 'Could not revert configuration' }
    finally { busy = false }
  }
  onMount(() => { void load(); return () => { stopped = true; controller?.abort() } })
</script>

<section class="panel history" aria-label="Configuration history">
  <header><h2>Configuration history</h2><button disabled={disabled || busy} onclick={load}>Refresh history</button></header>
  <p>Up to eight previous definitions are retained privately. Reverting saves the selected definition as current desired configuration and reconciles native definitions. It does not restore data, undo client activity or start stopped workloads.</p>
  {#if error}<p class="form-error" role="alert">{error}</p>{/if}
  {#if !entries.length && !busy}<p>No previous revisions retained.</p>{/if}
  <ul>{#each entries as entry}<li><div><code>{entry.revision.slice(0,12)}</code> · {new Date(entry.modifiedAt).toLocaleString()}{#if entry.error}<p>{entry.error}</p>{/if}</div><button disabled={busy || disabled} onclick={() => inspect(entry.revision)}>Inspect {entry.revision.slice(0,12)}</button></li>{/each}</ul>
  {#if selected}
    <h3>Historical YAML · {selected.revision.slice(0,12)}</h3>
    <label>Historical configuration<textarea rows="10" readonly value={selected.content}></textarea></label>
    <div class="actions"><button disabled={busy || disabled} onclick={() => onInspect(selected!.content)}>Copy into recovery editor</button><button disabled={busy || disabled || !selected.valid} onclick={review}>Review revert</button></div>
    {#if !selected.valid}<p>This revision contains invalid YAML. Inspect and repair it in the recovery editor before saving.</p>{/if}
    {#if plan}<h3>Review configuration revert</h3><p>Applying now may interrupt running workloads. A failed apply retains the selected desired definition and reports pending recovery.</p>
      <ul>{#each plan.changes as change}<li>{change.operation}: {change.resource} ({change.backend})</li>{/each}</ul>
      {#if plan.files.issues.length}<ul>{#each plan.files.issues as issue}<li><strong>{issue.resource}: {issue.message}</strong><p>{issue.remedy}</p></li>{/each}</ul>{/if}
      <label>Revert apply mode<select aria-label="Revert apply mode" bind:value={mode}><option value="next_start">Save for next start</option><option value="now">Apply now</option></select></label>
      <div class="actions"><button disabled={busy} onclick={() => { plan = null }}>Cancel revert</button><button disabled={busy || disabled} onclick={revert}>Confirm configuration revert</button></div>
    {/if}
  {/if}
</section>
<style>
  .history {
    margin-top: 24px;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  header {
    display: flex;
    flex-wrap: wrap;
    gap: 12px;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 12px;
  }
  header h2 {
    font-size: 17px;
  }
  ul {
    list-style: none;
    padding: 0;
    margin: 16px 0;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  li {
    display: flex;
    flex-wrap: wrap;
    gap: 12px;
    justify-content: space-between;
    align-items: center;
    padding: 10px 14px;
    background: var(--panel-subtle);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    font-size: 12.5px;
  }
  li code {
    font-family: var(--mono);
    color: var(--accent);
    font-weight: 600;
  }
  h3 {
    margin: 20px 0 10px;
    font-size: 15px;
  }
  label {
    display: grid;
    gap: 8px;
    margin-top: 12px;
  }
  textarea {
    max-width: 100%;
    width: 100%;
    font-family: var(--mono);
    font-size: 12px;
    line-height: 1.6;
    padding: 14px;
    background: var(--panel-inset);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    margin: 16px 0;
  }
</style>
