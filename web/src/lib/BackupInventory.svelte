<script lang="ts">
  import { onMount } from 'svelte'
  import { invokeTool } from './api'
  import type { ConfigurationIssue } from './managed'
  interface Input {native?:{backend:string;name:string;project?:string;pool?:string;contentType?:string};runtimeState?:string;id:string;kind:string;strategy:string;path?:string;bundlePath?:string;resources:string[];required:boolean;sensitive:boolean;includedBy?:string;inspection:string;logicalBytes?:number;allocatedBytes?:number}
  interface Inventory {stage:string;revision:string;checkedAt:string;inputs:Input[];requirements:{resource:string;kind:string;description:string}[];exclusions:{kind:string;reason:string}[];issues:ConfigurationIssue[]}
  interface BackupSummary {stage:string;destination:string;revision:string;preparedAt:string;totalFiles:number;totalBytes:number;manifestChecksum:string;quiesced:boolean;resumedServices?:string[];failedResume?:string[]}
  let {disabled}:{disabled:boolean}=$props()
  let inventory=$state<Inventory|null>(null)
  let busy=$state(false)
  let error=$state('')
  let destination=$state('')
  let quiesce=$state(true)
  let confirm=$state(false)
  let preparing=$state(false)
  let prepareError=$state('')
  let summary=$state<BackupSummary|null>(null)
  let controller:AbortController|undefined
  let stopped=false
  async function review(){
    busy=true;error='';controller?.abort();controller=new AbortController()
    try{const result=await invokeTool<Inventory>('managed_backup_inventory',{},controller.signal);if(!stopped)inventory=result}
    catch(e){if(!stopped)error=e instanceof Error?e.message:'Could not inventory backup inputs'}
    finally{if(!stopped)busy=false}
  }
  async function prepareBundle(){
    if(!destination.trim()||!confirm)return
    preparing=true;prepareError=''
    try{
      const result=await invokeTool<BackupSummary>('managed_backup_prepare',{destination:destination.trim(),quiesce,confirm:true})
      if(!stopped)summary=result
    }catch(e){
      if(!stopped)prepareError=e instanceof Error?e.message:'Backup preparation failed'
    }finally{
      if(!stopped)preparing=false
    }
  }
  onMount(()=>()=>{stopped=true;controller?.abort()})
</script>
<section class="panel backup-inventory" aria-label="Backup inventory">
  <header><h2>Portable backup inventory</h2><button disabled={disabled||busy} onclick={review}>{busy?'Inspecting…':'Review backup inventory'}</button></header>
  <p>Review required inputs and host dependencies before preparing a portable backup. This inspection creates no bundle, reads no referenced secret files and changes no workloads. Directory trees and native VM state need further collection; this is not a completed backup.</p>
  {#if error}<p class="form-error" role="alert">{error}</p>{/if}
  {#if inventory}
    <p>Stage: {inventory.stage} · Configuration {inventory.revision.slice(0,12)} · checked {new Date(inventory.checkedAt).toLocaleString()}.</p>
    {#if inventory.issues.length}<details open><summary>Unresolved backup inputs ({inventory.issues.length})</summary><ul>{#each inventory.issues as issue}<li><strong>{issue.resource} · {issue.severity}</strong><p>{issue.message}</p><p>{issue.remedy}</p></li>{/each}</ul></details>{/if}
    <details open><summary>Required inputs ({inventory.inputs.length})</summary><ul>{#each inventory.inputs as input}<li><strong>{input.id}</strong><p>{input.kind} · {input.strategy.replaceAll('_',' ')} · {input.inspection.replaceAll('_',' ')}</p>{#if input.native}<p>Native object: {input.native.backend} · {input.native.name}{input.native.project ? ` · project ${input.native.project}` : ''}{input.native.pool ? ` · pool ${input.native.pool}` : ''}{input.native.contentType ? ` · ${input.native.contentType}` : ''}</p>{/if}{#if input.runtimeState}<p>Observed state: {input.runtimeState}</p>{/if}{#if input.path}<p>{input.path}</p>{/if}{#if input.bundlePath}<p>Bundle path: {input.bundlePath}</p>{/if}{#if input.includedBy}<p>Covered by: {input.includedBy}</p>{/if}<p>References: {input.resources.join(', ')}</p>{#if input.logicalBytes !== undefined}<p>Observed file size: {input.logicalBytes} bytes{input.allocatedBytes !== undefined ? ` · allocated ${input.allocatedBytes} bytes` : ''}. Directory contents are not included in these figures.</p>{/if}</li>{/each}</ul></details>
    <details><summary>Collection and restore requirements ({inventory.requirements.length})</summary><ul>{#each inventory.requirements as requirement}<li><strong>{requirement.resource} · {requirement.kind.replaceAll('_',' ')}</strong><p>{requirement.description}</p></li>{/each}</ul></details>
    <details><summary>Explicit exclusions ({inventory.exclusions.length})</summary><ul>{#each inventory.exclusions as exclusion}<li><strong>{exclusion.kind.replaceAll('_',' ')}</strong><p>{exclusion.reason}</p></li>{/each}</ul></details>
    <div class="prepare-section">
      <h3>Prepare backup bundle</h3>
      <p>Collect authoritative configuration, declared storage data, referenced secret/asset files and native VM exports into a verified, checksummed bundle.</p>
      {#if prepareError}<p class="form-error" role="alert">{prepareError}</p>{/if}
      {#if summary}
        <div class="summary-box" role="status">
          <p><strong>Backup bundle prepared successfully.</strong></p>
          <p>Destination: {summary.destination}</p>
          <p>{summary.totalFiles} files · {summary.totalBytes} bytes · manifest checksum {summary.manifestChecksum.slice(0, 16)}…</p>
          {#if summary.resumedServices && summary.resumedServices.length > 0}
            <p>Resumed services: {summary.resumedServices.join(', ')}</p>
          {/if}
          {#if summary.failedResume && summary.failedResume.length > 0}
            <p class="form-error">Warning: failed to resume services: {summary.failedResume.join(', ')}</p>
          {/if}
        </div>
      {:else}
        <div class="prepare-form">
          <label>
            Destination directory (absolute path)
            <input type="text" bind:value={destination} placeholder="/var/backups/server-man-bundle" disabled={disabled || preparing} />
          </label>
          <label class="checkbox-label">
            <input type="checkbox" bind:checked={quiesce} disabled={disabled || preparing} />
            Gracefully stop running workloads during backup and resume afterward
          </label>
          <label class="checkbox-label">
            <input type="checkbox" bind:checked={confirm} disabled={disabled || preparing} />
            I acknowledge this creates a private backup bundle on disk
          </label>
          <button disabled={disabled || preparing || !destination.trim() || !confirm} onclick={prepareBundle}>
            {preparing ? 'Preparing bundle…' : 'Prepare backup bundle'}
          </button>
        </div>
      {/if}
    </div>
  {/if}
</section>
<style>
 .backup-inventory{margin-top:1rem;min-width:0;overflow-wrap:anywhere}header{display:flex;flex-wrap:wrap;gap:1rem;justify-content:space-between;align-items:center}details{margin-top:1rem}summary{cursor:pointer}ul{padding-left:1.25rem}li{padding:.5rem 0}p{margin:.4rem 0}
 .prepare-section{margin-top:1.5rem;padding-top:1rem;border-top:1px solid var(--border-color,#333)}.prepare-form{display:flex;flex-direction:column;gap:.75rem;margin-top:.75rem}
 .prepare-form input[type="text"]{width:100%;box-sizing:border-box;padding:.4rem .6rem}
 .checkbox-label{display:flex;align-items:center;gap:.5rem;cursor:pointer}
 .summary-box{padding:.75rem;margin-top:.75rem;border-radius:4px;background:rgba(46,160,67,0.15);border:1px solid rgba(46,160,67,0.4)}
</style>
