<script lang="ts">
  import { onMount } from 'svelte'
  import DefinitionEditor from '../lib/DefinitionEditor.svelte'
  import ConfirmDialog from '../lib/ConfirmDialog.svelte'
  import { invokeTool,startJob,type Job } from '../lib/api'
  import type { ShareDefinition } from '../lib/managed'
  let { allowWrites }: {allowWrites:boolean} = $props()
  interface ShareItem {id:string;definition:ShareDefinition;configuration:string;issues:{message:string;remedy:string}[]}
  let shares = $state<ShareItem[]>([])
  let editor = $state<{id:string} | null>(null)
  let error = $state('')
  let feedback = $state('')
  let loading = $state(false)
  let activeJob = $state<string | null>(null)
  let pending = $state<ShareItem | null>(null)
  let setupPending = $state(false)
  let protocol = $state('samba')
  let controller: AbortController | undefined
  let stopped = false
  async function load() { controller?.abort(); controller = new AbortController(); const signal = controller.signal; loading = true; error = ''; try { const result = await invokeTool<{shares:ShareItem[]}>('managed_shares',{},signal); if (!stopped && !signal.aborted) shares = result.shares } catch(e) { if (!signal.aborted) error = e instanceof Error ? e.message : 'Could not read file shares' } finally { if (!signal.aborted) loading = false } }
  async function publish() { const share = pending; if (!share) return; pending = null; try { const job = await startJob('managed_share_publish',{share:share.id,published:!share.definition.enabled,confirm:true}); activeJob = job.id; feedback = 'Share publication change started.' } catch(e) { error = e instanceof Error ? e.message : 'Could not change publication' } }
  async function setup() { setupPending = false; try { const job = await startJob('shares_setup',{protocol,confirm:true}); activeJob = job.id; feedback = 'Host share-server setup started.' } catch(e) { error = e instanceof Error ? e.message : 'Could not start host setup' } }
  onMount(() => { const finished = (e:Event) => { const job = (e as CustomEvent<Job>).detail; if (job.id !== activeJob) return; activeJob = null; feedback = job.state === 'succeeded' ? 'Share configuration applied. Existing clients may retain earlier access until reconnecting.' : job.error || `Operation ${job.state}.`; void load() }; window.addEventListener('server-man:job-finished',finished); void load(); return () => { stopped = true; controller?.abort(); window.removeEventListener('server-man:job-finished',finished) } })
</script>
<div class="actions"><button disabled={!allowWrites || activeJob !== null} onclick={() => { editor = {id:''} }}>Define file share</button><button disabled={loading} onclick={load}>{loading ? 'Reading…' : 'Refresh shares'}</button></div>
{#if error}<p role="alert" class="form-error">{error}</p>{/if}{#if feedback}<p role="status">{feedback}</p>{/if}
<section class="panel"><h2>Host share server</h2><p>Install/configure the native server as an explicit host operation, separate from share definitions.</p><div class="actions"><label>Server protocol <select bind:value={protocol}><option value="samba">Samba</option><option value="nfs">NFS</option></select></label><button disabled={!allowWrites || activeJob !== null} onclick={() => { setupPending = true }}>Set up server…</button></div></section>
<p class="resource-hint">Definitions come from server-man configuration. Host packages, daemon setup, accounts and directory permissions are managed separately.</p>
{#if editor}{#key editor.id}<DefinitionEditor kind="share" resourceID={editor.id} onClose={() => { editor = null }} onSaved={(id) => { editor = null; activeJob = id; feedback = 'Share definition save started. Follow Activity for validation and publication.' }} />{/key}{/if}
{#if !shares.length && !loading}<section class="panel"><h2>No configured file shares</h2><p>Add directory storage in Configuration, then define a Samba or NFS share.</p></section>{/if}
{#each shares as share}<section class="panel"><header><div><h2>{share.id}</h2><p>{share.definition.protocol} · storage: {share.definition.storage}</p></div><strong>{share.configuration}</strong></header><p>{share.definition.enabled ? 'Publication enabled' : 'Publication disabled'} · {share.definition.readOnly ? 'Read only' : 'Read and write'}</p>
{#if share.issues.length}<ul>{#each share.issues as issue}<li><strong>{issue.message}</strong><p>{issue.remedy}</p></li>{/each}</ul>{/if}
<div class="actions"><button disabled={!allowWrites || activeJob !== null} onclick={() => { editor = {id:share.id} }}>Edit share</button><button disabled={!allowWrites || activeJob !== null} onclick={() => { pending = share }}>{share.definition.enabled ? 'Unpublish share' : 'Publish share'}</button></div></section>{/each}
<ConfirmDialog open={pending !== null} title={pending ? `${pending.definition.enabled ? 'Unpublish' : 'Publish'}: ${pending.id}` : ''} description="Update the authoritative share publication setting. Shared data and host daemon enablement are preserved. Existing clients may keep prior settings until reconnecting." label="Apply publication" busy={activeJob !== null} onConfirm={publish} onClose={() => { pending = null }} />
<ConfirmDialog open={setupPending} title="Set up server" description="Install native server packages, prepare owned include/export setup, and enable/start the host server. Existing configuration and shared data are preserved. Credentials, firewall and directory permissions remain explicit administrator setup." label="Set up server" busy={activeJob !== null} onConfirm={setup} onClose={() => { setupPending = false }} />
<style>.panel { margin-top:1rem; min-width:0; } header,.actions { display:flex; flex-wrap:wrap; gap:1rem; justify-content:space-between; align-items:center; } p,h2,strong { overflow-wrap:anywhere; }</style>
