<script lang="ts">
  import { invokeTool, startJob } from './api'
  import type { DiskSource } from './managed'
  let { storageID, revision, kind, sourceConfigured = false, onClose, onStarted }: {storageID:string;revision:string;kind:string;sourceConfigured?:boolean;onClose:()=>void;onStarted:(jobID:string)=>void} = $props()
  interface Plan { source?:DiskSource;sourceVirtualBytes?:number;storage:string;revision:string;path:string;kind:string;format?:string;sizeGiB:number;availableBytes:number;uid:number;gid:number;mode:string;consumers:string[];reviewToken:string }
  let sizeGiB = $state(20)
  let plan = $state<Plan | null>(null)
  let acknowledge = $state(false)
  let busy = $state(false)
  let error = $state('')
  async function review() {
    busy = true; error = ''; acknowledge = false
    try { plan = await invokeTool<Plan>('managed_storage_plan',{storage:storageID,revision,sizeGiB:kind === 'file' ? sizeGiB : 0}) }
    catch(e) { error = e instanceof Error ? e.message : 'Could not review storage creation' }
    finally { busy = false }
  }
  async function create() {
    if (!plan || !acknowledge) return
    busy = true; error = ''
    try { const job = await startJob('managed_storage_create',{storage:plan.storage,revision:plan.revision,sizeGiB:plan.sizeGiB,reviewToken:plan.reviewToken,purpose:'new_data',confirm:true}); onStarted(job.id) }
    catch(e) { error = e instanceof Error ? e.message : 'Could not create new data' }
    finally { busy = false }
  }
</script>
<section class="panel provision" aria-label="New storage provisioning">
  <header><h2>Create new data · {storageID}</h2><button disabled={busy} onclick={onClose}>Close provisioning</button></header>
  <p>Use this only to provision new data. If this path belongs to a moved or restored workload, restore its original data instead. Existing paths are never replaced, and missing parent directories must be created or restored explicitly first.</p>
  {#if error}<p class="form-error" role="alert">{error}</p>{/if}
  {#if plan}
    <h3>Review new storage</h3>
    <dl><div><dt>Destination</dt><dd>{plan.path}</dd></div><div><dt>Type</dt><dd>{plan.kind}{plan.format ? ` · ${plan.format}` : ''}</dd></div><div><dt>Owner and permissions</dt><dd>UID {plan.uid} · GID {plan.gid} · {plan.mode}</dd></div>{#if plan.kind === 'file'}<div><dt>Virtual capacity</dt><dd>{plan.sizeGiB} GiB · sparse, space is not reserved</dd></div>{/if}<div><dt>Available filesystem space</dt><dd>{(plan.availableBytes / 1073741824).toFixed(1)} GiB</dd></div></dl>
    {#if plan.consumers.length}<p>References: {plan.consumers.join(', ')}. Services must remain stopped and shares unpublished during creation.</p>{/if}
    {#if plan.source}<p>Initial image: {plan.source.path} · {plan.source.format}. Source capacity: {((plan.sourceVirtualBytes || 0)/1073741824).toFixed(2)} GiB.</p><p>Expected SHA-256: {plan.source.sha256}. The creation job verifies the full checksum before and after conversion.</p>{/if}
    {#if plan.kind === 'file' && !plan.source}<p>A blank disk has no operating system. Guest installation or image provisioning is a separate operation. Disk growth can exhaust the filesystem; virtual capacity does not guarantee available space.</p>{/if}
    <label class="ack-label"><input type="checkbox" bind:checked={acknowledge} /> {plan.source ? 'I want a new disk from this image; I am not restoring missing guest data.' : 'I want new empty data; I am not restoring missing data.'}</label>
    <div class="actions"><button disabled={busy} onclick={() => { plan = null; acknowledge = false }}>Back to provisioning</button><button class="primary" disabled={busy || !acknowledge} onclick={create}>Confirm new data creation</button></div>
  {:else}
    {#if kind === 'file'}<label class="size-label">{sourceConfigured ? 'New disk size GiB' : 'Blank disk size GiB'}<input aria-label="Disk size GiB" type="number" min="1" max="4096" bind:value={sizeGiB} /></label>{/if}
    <div class="actions"><button class="primary" disabled={busy} onclick={review}>{busy ? 'Checking…' : 'Review new data creation'}</button></div>
  {/if}
</section>
<style>
  .provision {
    margin-top: 24px;
    padding: 24px;
    min-width: 0;
    overflow-wrap: anywhere;
    border: 1px solid var(--border-light);
    box-shadow: var(--shadow-md);
  }
  header {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: 12px;
    align-items: center;
    margin-bottom: 14px;
    padding-bottom: 12px;
    border-bottom: 1px solid var(--border-subtle);
  }
  header h2 {
    font-size: 17px;
  }
  dl {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px 18px;
    margin: 16px 0;
    padding: 16px;
    background: var(--panel-subtle);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
  }
  dt {
    font-size: 11px;
    color: var(--dim);
    text-transform: uppercase;
    letter-spacing: 0.8px;
    font-weight: 600;
  }
  dd {
    font-size: 13px;
    font-weight: 550;
    color: var(--text);
    margin: 2px 0 0;
  }
  .ack-label {
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 18px 0;
    cursor: pointer;
    font-size: 13px;
    color: var(--text-secondary);
  }
  .size-label {
    display: grid;
    gap: 8px;
    max-width: 260px;
    margin-top: 14px;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-start;
    gap: 10px;
    align-items: center;
    margin-top: 18px;
  }
  @media (max-width: 600px) {
    dl {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
