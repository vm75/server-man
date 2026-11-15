<script lang="ts">
  import { onMount } from 'svelte'
  import { invokeTool } from './api'
  import type { ConfigurationIssue } from './managed'
  interface Input {id:string;kind:string;strategy:string;path?:string;bundlePath?:string;resources:string[];required:boolean;sensitive:boolean;includedBy?:string;inspection:string;logicalBytes?:number;allocatedBytes?:number}
  interface Inventory {stage:string;revision:string;checkedAt:string;inputs:Input[];requirements:{resource:string;kind:string;description:string}[];exclusions:{kind:string;reason:string}[];issues:ConfigurationIssue[]}
  let {disabled}:{disabled:boolean}=$props()
  let inventory=$state<Inventory|null>(null)
  let busy=$state(false)
  let error=$state('')
  let controller:AbortController|undefined
  let stopped=false
  async function review(){
    busy=true;error='';controller?.abort();controller=new AbortController()
    try{const result=await invokeTool<Inventory>('managed_backup_inventory',{},controller.signal);if(!stopped)inventory=result}
    catch(e){if(!stopped)error=e instanceof Error?e.message:'Could not inventory backup inputs'}
    finally{if(!stopped)busy=false}
  }
  onMount(()=>()=>{stopped=true;controller?.abort()})
</script>
<section class="panel backup-inventory" aria-label="Backup inventory">
  <header><h2>Portable backup inventory</h2><button disabled={disabled||busy} onclick={review}>{busy?'Inspecting…':'Review backup inventory'}</button></header>
  <p>Review required inputs and host dependencies before preparing a portable backup. This inspection creates no bundle, reads no secret contents and changes no workloads. Directory trees and native VM state need further collection; this is not a completed backup.</p>
  {#if error}<p class="form-error" role="alert">{error}</p>{/if}
  {#if inventory}
    <p>Stage: {inventory.stage} · Configuration {inventory.revision.slice(0,12)} · checked {new Date(inventory.checkedAt).toLocaleString()}.</p>
    {#if inventory.issues.length}<details open><summary>Unresolved backup inputs ({inventory.issues.length})</summary><ul>{#each inventory.issues as issue}<li><strong>{issue.resource} · {issue.severity}</strong><p>{issue.message}</p><p>{issue.remedy}</p></li>{/each}</ul></details>{/if}
    <details open><summary>Required inputs ({inventory.inputs.length})</summary><ul>{#each inventory.inputs as input}<li><strong>{input.id}</strong><p>{input.kind} · {input.strategy.replaceAll('_',' ')} · {input.inspection.replaceAll('_',' ')}</p>{#if input.path}<p>{input.path}</p>{/if}{#if input.bundlePath}<p>Bundle path: {input.bundlePath}</p>{/if}{#if input.includedBy}<p>Covered by: {input.includedBy}</p>{/if}<p>References: {input.resources.join(', ')}</p>{#if input.logicalBytes !== undefined}<p>Observed file size: {input.logicalBytes} bytes{input.allocatedBytes !== undefined ? ` · allocated ${input.allocatedBytes} bytes` : ''}. Directory contents are not included in these figures.</p>{/if}</li>{/each}</ul></details>
    <details><summary>Collection and restore requirements ({inventory.requirements.length})</summary><ul>{#each inventory.requirements as requirement}<li><strong>{requirement.resource} · {requirement.kind.replaceAll('_',' ')}</strong><p>{requirement.description}</p></li>{/each}</ul></details>
    <details><summary>Explicit exclusions ({inventory.exclusions.length})</summary><ul>{#each inventory.exclusions as exclusion}<li><strong>{exclusion.kind.replaceAll('_',' ')}</strong><p>{exclusion.reason}</p></li>{/each}</ul></details>
  {/if}
</section>
<style>
 .backup-inventory{margin-top:1rem;min-width:0;overflow-wrap:anywhere}header{display:flex;flex-wrap:wrap;gap:1rem;justify-content:space-between;align-items:center}details{margin-top:1rem}summary{cursor:pointer}ul{padding-left:1.25rem}li{padding:.5rem 0}p{margin:.4rem 0}
</style>
