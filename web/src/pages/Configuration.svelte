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
  let applyResources = $state<Record<string,{state:string;message?:string}>>({})
  type ArtifactReview = { resource:string; revision:string; reviewToken:string; canReconcile:boolean; parts:{label:string;files:string;manifest:string}[] }
  let artifactReview = $state<ArtifactReview | null>(null)
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
      artifactReview = null
      content = raw.content; revision = raw.revision; valid = raw.valid
      if (raw.valid) model = await invokeTool<ModelSnapshot>('managed_config_model',{},signal)
      else { model = null; rawOpen = true; error = raw.error || 'Repair invalid YAML before using definition forms.' }
      const status = await invokeTool<{stage:string;resources?:Record<string,{state:string;message?:string}>}>('managed_apply_status',{},signal)
      if (!stopped && !signal.aborted) { applyStage = status.stage; applyResources = status.resources ?? {} }
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
  async function reviewArtifact(resource:string) {
    busy = true; error = ''; artifactReview = null
    try { artifactReview = await invokeTool<ArtifactReview>('managed_artifact_review',{resource}) }
    catch(e) { error = e instanceof Error ? e.message : 'Could not inspect generated files' }
    finally { busy = false }
  }
  async function reconcileArtifact() {
    if (!artifactReview?.canReconcile) return
    busy = true; error = ''
    try {
      const job = await startJob('managed_artifact_reconcile',{resource:artifactReview.resource,revision:artifactReview.revision,reviewToken:artifactReview.reviewToken,confirm:true})
      activeJob = job.id; artifactReview = null
      feedback = 'Metadata-only repair started. Inspect the result before explicitly retrying apply.'
    } catch(e) { error = e instanceof Error ? e.message : 'Could not reconcile artifact metadata' }
    finally { busy = false }
  }
  onMount(() => { const finished = (e:Event) => { const job = (e as CustomEvent<Job>).detail; if (job.id !== activeJob) return; activeJob = null; feedback = job.state === 'succeeded' ? 'Configuration operation completed.' : job.error || `Operation ${job.state}.`; void load() }; window.addEventListener('server-man:job-finished',finished); void load(); return () => { stopped = true; controller?.abort(); window.removeEventListener('server-man:job-finished',finished) } })
</script>

{#if !allowWrites}
  <section class="panel">
    <h2>Configuration editing is disabled</h2>
    <p>Enable management writes in server settings to edit private definitions. Applications still shows redacted status and startup issues.</p>
  </section>
{:else}
  <div class="config-toolbar">
    <button disabled={busy || activeJob !== null} onclick={load}>Refresh configuration</button>
    <button disabled={busy || activeJob !== null} onclick={() => { rawOpen = !rawOpen }}>{rawOpen ? 'Close YAML editor' : 'YAML / recovery editor'}</button>
  </div>

  {#if error}<div class="notice error" role="alert"><p>{error}</p><button onclick={load}>Retry</button></div>{/if}
  {#if feedback}<div class="job-feedback" role="status">{feedback}</div>{/if}

  {#if !['none','complete','aborted'].includes(applyStage)}
    <section class="panel apply-alert" aria-label="Configuration apply recovery">
      <h2>Apply needs attention: {applyStage}</h2>
      <p>The saved configuration may differ from registered or running resources. Retry inspects native state before repeating work; uncertain destructive operations are refused.</p>
      <ul class="recovery-list">
        {#each Object.entries(applyResources).sort(([a],[b]) => a.localeCompare(b)) as [resource,result]}
          <li>
            <div>
              <strong>{resource}</strong>: {result.state.replaceAll('_', ' ')}
              {#if result.message}<p>{result.message}</p>{/if}
            </div>
            {#if resource.startsWith('service/') && result.state === 'failed' && result.message?.startsWith('Generated')}
              <button disabled={busy || activeJob !== null} onclick={() => reviewArtifact(resource)}>Review generated artifact recovery for {resource}</button>
            {/if}
          </li>
        {/each}
      </ul>
      {#if artifactReview}
        <section class="panel" aria-label="Generated artifact metadata review">
          <h3>Generated artifact recovery: {artifactReview.resource}</h3>
          <p>This operation only reconciles private checksum sidecars when every native generated file matches a known definition. It preserves prior sidecar bytes, never replaces native definitions or starts workloads. Native file drift requires manual inspection.</p>
          <ul>{#each artifactReview.parts as part}<li><strong>{part.label}</strong><span>Native files: {part.files}; publication manifest: {part.manifest}</span></li>{/each}</ul>
          {#if artifactReview.canReconcile}
            <button disabled={busy || activeJob !== null} onclick={reconcileArtifact}>Confirm metadata-only reconciliation</button>
          {:else}
            <p>These files cannot be reconciled safely. Inspect and preserve the conflicting native files before manual repair.</p>
          {/if}
          <button disabled={busy || activeJob !== null} onclick={() => { artifactReview = null }}>Close artifact review</button>
        </section>
      {/if}
      <button class="primary" disabled={busy || activeJob !== null} onclick={retry}>Inspect and retry apply</button>
    </section>
  {/if}

  {#if provisioning}
    <StorageProvision storageID={provisioning} {revision} kind={model?.config.storage?.[provisioning]?.kind || 'directory'} sourceConfigured={!!model?.config.storage?.[provisioning]?.source} onClose={() => { provisioning = null }} onStarted={(id) => { activeJob = id; provisioning = null; feedback = 'New data creation started. Follow Activity for results; workloads remain stopped.' }} />
  {/if}

  {#if editor}
    {#key `${editor.kind}/${editor.id}`}
      <DefinitionEditor kind={editor.kind} resourceID={editor.id} onClose={() => { editor = null }} onSaved={(id) => { editor = null; activeJob = id; feedback = 'Definition save started. Follow Activity for apply results.' }} />
    {/key}
  {/if}

  {#if valid}
    {#each [{kind:'storage',title:'Storage references'},{kind:'binding',title:'Host bindings'},{kind:'secret',title:'Private file references'}] as group}
      <section class="panel config-group">
        <header class="group-header">
          <h2>{group.title}</h2>
          <button class="small-button" disabled={busy || activeJob !== null} onclick={() => { editor = {kind:group.kind as ResourceKind,id:''} }}>
            Add {group.kind === 'secret' ? 'private file reference' : group.kind}
          </button>
        </header>
        {#if !entries(group.kind as ResourceKind).length}
          <p class="empty-group">No references defined.</p>
        {:else}
          <ul class="entry-list">
            {#each entries(group.kind as ResourceKind) as [id,description]}
              <li class="entry-item">
                <div class="entry-info">
                  <strong class="entry-id mono">{id}</strong>
                  <p class="entry-desc">{description}</p>
                </div>
                <div class="entry-actions">
                  <button class="small-button" disabled={busy || activeJob !== null} onclick={() => { editor = {kind:group.kind as ResourceKind,id} }}>Edit {id}</button>
                  {#if group.kind === 'storage' && !model?.config.storage?.[id]?.external}
                    <button class="small-button" disabled={busy || activeJob !== null} onclick={() => { provisioning = id }}>
                      {model?.config.storage?.[id]?.source ? 'Provision image' : 'Create empty data'} for {id}
                    </button>
                  {/if}
                </div>
              </li>
            {/each}
          </ul>
        {/if}
      </section>
    {/each}
  {/if}

  {#key revision}<BackupInventory disabled={busy || activeJob !== null} />{/key}
  {#key revision}<ConfigurationHistory {revision} disabled={busy || activeJob !== null} onStarted={(id) => { activeJob = id; feedback = 'Configuration revert started. Follow Activity for apply results.' }} onInspect={(historical) => { content = historical; rawOpen = true; plan = null }} />{/key}

  {#if rawOpen}
    <section class="panel yaml-panel">
      <h2>Authoritative YAML</h2>
      <p>One file contains server settings and all managed resources. Server setting changes require restarting server-man; save them separately from resource changes. Saves validate the full configuration and reject stale revisions. Referenced private files are never read here.</p>
      {#if plan}
        <div class="plan-review">
          <h3>Review YAML changes</h3>
          {#if plan.restartRequired}<p role="status" class="restart-note">Restart required after saving. Restart server-man to activate server settings before further managed operations.</p>{/if}
          <ul class="plan-changes">
            {#each plan.changes as change}
              <li><strong>{change.operation}</strong>: {change.resource} <span class="muted">({change.backend})</span></li>
            {/each}
          </ul>
          {#if plan.files.issues.length}
            <ul class="plan-issues">
              {#each plan.files.issues as issue}
                <li><strong>{issue.resource}: {issue.message}</strong><p>{issue.remedy}</p></li>
              {/each}
            </ul>
          {/if}
          <label class="apply-mode-label">
            Apply mode
            <select bind:value={mode}>
              <option value="now">Apply now</option>
              <option value="next_start">Save for next start</option>
            </select>
          </label>
          <div class="actions">
            <button disabled={busy} onclick={() => { plan = null }}>Back to YAML</button>
            <button class="primary" disabled={busy || activeJob !== null} onclick={rawSave}>Confirm YAML save</button>
          </div>
        </div>
      {:else}
        <div class="yaml-editor-wrap">
          <label for="server-man-yaml">Server-man YAML</label>
          <textarea id="server-man-yaml" rows="22" spellcheck="false" bind:value={content}></textarea>
        </div>
        <div class="actions">
          <button class="primary" disabled={busy || activeJob !== null} onclick={review}>Validate and review YAML</button>
        </div>
      {/if}
    </section>
  {/if}
{/if}

<style>
  .config-toolbar {
    display: flex;
    gap: 12px;
    align-items: center;
    margin-bottom: 24px;
    flex-wrap: wrap;
  }
  .group-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 16px;
    margin-bottom: 16px;
  }
  .group-header h2 {
    font-size: 17px;
  }
  .empty-group {
    font-size: 13px;
    color: var(--dim);
    font-style: italic;
  }
  .entry-list {
    list-style: none;
    padding: 0;
    margin: 0;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .entry-item {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 16px;
    padding: 12px 16px;
    background: var(--panel-subtle);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    flex-wrap: wrap;
    transition: border-color 0.15s ease;
  }
  .entry-item:hover {
    border-color: var(--border-light);
  }
  .entry-info {
    min-width: 0;
    flex: 1 1 180px;
  }
  .entry-id {
    font-size: 13px;
    color: var(--text);
  }
  .entry-desc {
    font-size: 12px;
    color: var(--muted);
    margin-top: 2px;
    overflow-wrap: anywhere;
  }
  .entry-actions {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    max-width: 100%;
  }
  .entry-actions button {
    white-space: normal;
  }
  @media (max-width: 600px) {
    .entry-item {
      flex-direction: column;
      align-items: stretch;
      gap: 10px;
    }
    .entry-actions {
      flex-direction: column;
      width: 100%;
    }
    .entry-actions button {
      width: 100%;
      text-align: center;
    }
  }
  .apply-alert {
    border-left: 4px solid var(--amber);
    background: var(--amber-dim);
    border-color: var(--amber-border);
    margin-bottom: 24px;
  }
  .apply-alert h2 {
    color: var(--amber-text);
    font-size: 16px;
    margin-bottom: 6px;
  }
  .recovery-list {
    list-style: none;
    padding: 0;
    margin: 14px 0;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .recovery-list li {
    padding: 8px 12px;
    background: var(--panel-subtle);
    border-radius: var(--radius-sm);
    font-size: 12px;
  }
  .yaml-panel h2 {
    margin-bottom: 8px;
  }
  .yaml-panel > p {
    margin-bottom: 20px;
  }
  .yaml-editor-wrap {
    margin: 16px 0;
  }
  .yaml-editor-wrap label {
    margin-bottom: 8px;
  }
  .yaml-editor-wrap textarea {
    font-family: var(--mono);
    font-size: 12px;
    line-height: 1.6;
    padding: 16px;
    background: var(--panel-inset);
    border-radius: var(--radius-sm);
  }
  .plan-review {
    margin-top: 18px;
    padding: 18px;
    background: var(--panel-subtle);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-md);
  }
  .restart-note {
    color: var(--amber);
    font-weight: 550;
    margin: 8px 0;
  }
  .plan-changes, .plan-issues {
    list-style: none;
    padding: 0;
    margin: 12px 0;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .plan-changes li, .plan-issues li {
    padding: 8px 12px;
    background: var(--panel);
    border-radius: var(--radius-sm);
    font-size: 12.5px;
  }
  .apply-mode-label {
    margin: 16px 0;
    max-width: 280px;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    margin-top: 16px;
  }
</style>
