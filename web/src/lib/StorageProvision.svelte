<script lang="ts">
  import { invokeTool, startJob } from './api'
  let { storageID, revision, kind, onClose, onStarted }: {storageID:string;revision:string;kind:string;onClose:()=>void;onStarted:(jobID:string)=>void} = $props()
  interface Plan { storage:string;revision:string;path:string;kind:string;format?:string;sizeGiB:number;availableBytes:number;uid:number;gid:number;mode:string;consumers:string[];reviewToken:string }
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
  <p>Use this only for new empty data. If this path belongs to a moved or restored workload, restore its original data instead. Existing paths are never replaced, and missing parent directories must be created or restored explicitly first.</p>
  {#if error}<p class="form-error" role="alert">{error}</p>{/if}
  {#if plan}
    <h3>Review new storage</h3>
    <dl><div><dt>Destination</dt><dd>{plan.path}</dd></div><div><dt>Type</dt><dd>{plan.kind}{plan.format ? ` · ${plan.format}` : ''}</dd></div><div><dt>Owner and permissions</dt><dd>UID {plan.uid} · GID {plan.gid} · {plan.mode}</dd></div>{#if plan.kind === 'file'}<div><dt>Virtual capacity</dt><dd>{plan.sizeGiB} GiB · sparse, space is not reserved</dd></div>{/if}<div><dt>Available filesystem space</dt><dd>{(plan.availableBytes / 1073741824).toFixed(1)} GiB</dd></div></dl>
    {#if plan.consumers.length}<p>References: {plan.consumers.join(', ')}. Services must remain stopped and shares unpublished during creation.</p>{/if}
    {#if plan.kind === 'file'}<p>A blank disk has no operating system. Guest installation or image provisioning is a separate operation. Disk growth can exhaust the filesystem; virtual capacity does not guarantee available space.</p>{/if}
    <label><input type="checkbox" bind:checked={acknowledge} /> I want new empty data; I am not restoring missing data.</label>
    <div class="actions"><button disabled={busy} onclick={() => { plan = null; acknowledge = false }}>Back to provisioning</button><button disabled={busy || !acknowledge} onclick={create}>Confirm new data creation</button></div>
  {:else}
    {#if kind === 'file'}<label>Blank disk size GiB<input aria-label="Blank disk size GiB" type="number" min="1" max="4096" bind:value={sizeGiB} /></label>{/if}
    <button disabled={busy} onclick={review}>{busy ? 'Checking…' : 'Review new data creation'}</button>
  {/if}
</section>
<style>
  .provision { margin-top:1rem; min-width:0; overflow-wrap:anywhere; } header,.actions { display:flex; flex-wrap:wrap; justify-content:space-between; gap:.75rem; align-items:center; } dd { margin:.3rem 0 .8rem; } .actions { margin-top:1rem; }
</style>
