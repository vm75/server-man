<script lang="ts">
  import { onMount } from 'svelte'
  import DefinitionEditor from '../lib/DefinitionEditor.svelte'
  import ConfigurationHistory from '../lib/ConfigurationHistory.svelte'
  import BackupInventory from '../lib/BackupInventory.svelte'
  import StorageProvision from '../lib/StorageProvision.svelte'
  import { invokeTool, startJob, type Job } from '../lib/api'
  import type { ResourceKind, ModelSnapshot, ImpactPlan } from '../lib/managed'
  let { allowWrites }: { allowWrites: boolean } = $props()
  let model = $state<ModelSnapshot | null>(null)
  let provisioning = $state<string | null>(null)
  let editor = $state<{kind:ResourceKind;id:string} | null>(null)
  let error = $state('')
  let feedback = $state('')
  let busy = $state(false)
  let activeJob = $state<string | null>(null)
  let rawOpen = $state(false)
  let content = $state('')
  let revision = $state('missing')
  let valid = $state(true)
  let plan = $state<ImpactPlan | null>(null)
  let rawReviewed = $state('')
  let mode = $state('now')
  let applyStage = $state('none')
  let controller: AbortController | undefined
  let stopped = false
  function entries(kind:ResourceKind): [string, string][] {
    if (kind === 'storage') return Object.entries(model?.config.storage ?? {}).map(([id,s]) => [id,`${s.path}${s.external ? ' (external)' : ''}`])
    if (kind === 'binding') return Object.entries(model?.config.bindings ?? {}).map(([id,b]) => [id,`${b.kind}: ${b.name}`])
    return Object.entries(model?.config.secrets ?? {})
  }
  async function load() {
    if (!allowWrites) return
    controller?.abort(); controller = new AbortController(); const signal = controller.signal; busy = true; error = ''
    try {
      const raw = await invokeTool<{content:string;revision:string;valid:boolean;error?:string}>('managed_config_read',{},signal)
      if (stopped || signal.aborted) return
      content = raw.content; revision = raw.revision; valid = raw.valid
      if (raw.valid) model = await invokeTool<ModelSnapshot>('managed_config_model',{},signal)
      else { model = null; rawOpen = true; error = raw.error || 'Repair invalid YAML before using definition forms.' }
      const status = await invokeTool<{stage:string}>('managed_apply_status',{},signal); if (!stopped) applyStage = status.stage
    } catch (e) { if (!signal.aborted) error = e instanceof Error ? e.message : 'Could not read configuration' }
    finally { if (!signal.aborted) busy = false }
  }
  async function review() {
    busy = true; error = ''
    try { const result = await invokeTool<{plan:ImpactPlan}>('managed_config_plan',{content,revision}); plan = result.plan; rawReviewed = content }
    catch(e) { error = e instanceof Error ? e.message : 'Configuration validation failed' }
    finally { busy = false }
  }
  async function rawSave() { busy = true; error = ''; try { const job = await startJob('managed_config_save',{content:rawReviewed,revision,mode,confirm:true}); activeJob = job.id; plan = null; feedback = 'Configuration save started. Follow Activity for publication results.' } catch(e) { error = e instanceof Error ? e.message : 'Could not save configuration' } finally { busy = false } }
  async function retry() { busy = true; error = ''; try { const job = await startJob('managed_apply_retry',{confirm:true}); activeJob = job.id; feedback = 'Recovery inspection and retry started.' } catch(e) { error = e instanceof Error ? e.message : 'Could not retry apply' } finally { busy = false } }
  onMount(() => { const finished = (e:Event) => { const job = (e as CustomEvent<Job>).detail; if (job.id !== activeJob) return; activeJob = null; feedback = job.state === 'succeeded' ? 'Configuration operation completed.' : job.error || `Operation ${job.state}.`; void load() }; window.addEventListener('server-man:job-finished',finished); void load(); return () => { stopped = true; controller?.abort(); window.removeEventListener('server-man:job-finished',finished) } })
</script>
{#if !allowWrites}<section class="panel"><h2>Configuration editing is disabled</h2><p>Enable management writes in server settings to edit private definitions. Applications still shows redacted status and startup issues.</p></section>
{:else}
  <div class="actions"><button disabled={busy || activeJob !== null} onclick={load}>Refresh configuration</button><button disabled={busy || activeJob !== null} onclick={() => { rawOpen = !rawOpen }}>YAML / recovery editor</button></div>
  {#if error}<p role="alert" class="form-error">{error}</p>{/if}{#if feedback}<p role="status">{feedback}</p>{/if}
  {#if !['none','complete','aborted'].includes(applyStage)}<section class="panel"><h2>Apply needs attention: {applyStage}</h2><p>The saved configuration may differ from registered or running resources. Retry inspects native state before repeating work; uncertain destructive operations are refused.</p><button disabled={busy || activeJob !== null} onclick={retry}>Inspect and retry apply</button></section>{/if}
  {#if provisioning}<StorageProvision storageID={provisioning} {revision} kind={model?.config.storage?.[provisioning]?.kind || 'directory'} sourceConfigured={!!model?.config.storage?.[provisioning]?.source} onClose={() => { provisioning = null }} onStarted={(id) => { activeJob = id; provisioning = null; feedback = 'New data creation started. Follow Activity for results; workloads remain stopped.' }} />{/if}
  {#if editor}{#key `${editor.kind}/${editor.id}`}<DefinitionEditor kind={editor.kind} resourceID={editor.id} onClose={() => { editor = null }} onSaved={(id) => { editor = null; activeJob = id; feedback = 'Definition save started. Follow Activity for apply results.' }} />{/key}{/if}
  {#if valid}{#each [{kind:'storage',title:'Storage references'},{kind:'binding',title:'Host bindings'},{kind:'secret',title:'Private file references'}] as group}
    <section class="panel"><header><h2>{group.title}</h2><button disabled={busy || activeJob !== null} onclick={() => { editor = {kind:group.kind as ResourceKind,id:''} }}>Add {group.kind === 'secret' ? 'private file reference' : group.kind}</button></header>
    {#if !entries(group.kind as ResourceKind).length}<p>No references defined.</p>{/if}
    <ul>{#each entries(group.kind as ResourceKind) as [id,description]}<li><div><strong>{id}</strong><p>{description}</p></div><div class="actions"><button disabled={busy || activeJob !== null} onclick={() => { editor = {kind:group.kind as ResourceKind,id} }}>Edit {id}</button>{#if group.kind === 'storage' && !model?.config.storage?.[id]?.external}<button disabled={busy || activeJob !== null} onclick={() => { provisioning = id }}>{model?.config.storage?.[id]?.source ? 'Provision image' : 'Create empty data'} for {id}</button>{/if}</div></li>{/each}</ul>
    </section>
  {/each}{/if}
  {#key revision}<BackupInventory disabled={busy || activeJob !== null} />{/key}
  {#key revision}<ConfigurationHistory {revision} disabled={busy || activeJob !== null} onStarted={(id) => { activeJob = id; feedback = 'Configuration revert started. Follow Activity for apply results.' }} onInspect={(historical) => { content = historical; rawOpen = true; plan = null }} />{/key}
  {#if rawOpen}<section class="panel"><h2>Authoritative YAML</h2><p>One file contains server settings and all managed resources. Server setting changes require restarting server-man; save them separately from resource changes. Saves validate the full configuration and reject stale revisions. Referenced private files are never read here.</p>
    {#if plan}<h3>Review YAML changes</h3>{#if plan.restartRequired}<p role="status">Restart required after saving. Restart server-man to activate server settings before further managed operations.</p>{/if}<ul>{#each plan.changes as change}<li>{change.operation}: {change.resource} ({change.backend})</li>{/each}</ul>{#if plan.files.issues.length}<ul>{#each plan.files.issues as issue}<li><strong>{issue.resource}: {issue.message}</strong><p>{issue.remedy}</p></li>{/each}</ul>{/if}<label>Apply mode <select bind:value={mode}><option value="now">Apply now</option><option value="next_start">Save for next start</option></select></label><div class="actions"><button disabled={busy} onclick={() => { plan = null }}>Back to YAML</button><button disabled={busy || activeJob !== null} onclick={rawSave}>Confirm YAML save</button></div>
    {:else}<label>Server-man YAML <textarea rows="20" spellcheck="false" bind:value={content}></textarea></label><button disabled={busy || activeJob !== null} onclick={review}>Validate and review YAML</button>{/if}
  </section>{/if}
{/if}
<style>
  .panel { margin-top:1rem; min-width:0; } header,.actions,li { display:flex; flex-wrap:wrap; gap:1rem; justify-content:space-between; align-items:center; } ul { list-style:none; padding:0; } li { padding:.5rem 0; } p,strong { overflow-wrap:anywhere; } label { display:grid; gap:.5rem; } textarea { width:100%; max-width:100%; font-family:monospace; } .actions { margin:.8rem 0; }
</style>
