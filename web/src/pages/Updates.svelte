<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import ConfirmDialog from '../lib/ConfirmDialog.svelte'
  import { invokeTool, startJob, type Job } from '../lib/api'
  import { updateKinds, type UpdateStatus } from '../lib/updates'

  let { allowWrites }: { allowWrites: boolean } = $props()
  let status = $state<UpdateStatus | null>(null), loading = $state(true), error = $state('')
  let packageNames = $state(''), installPackages = $state(false)
  let filter = $state(''), kind = $state('all'), confirm = $state(false), submitting = $state(false), activeJob = $state<string | null>(null)
  let feedback = $state(''), failed = $state(false), controller: AbortController | undefined, stopped = false
  const packages = $derived(status?.packages.filter(p => (kind === 'all' || p.kind === kind) && `${p.name} ${p.architecture ?? ''}`.toLowerCase().includes(filter.toLowerCase())) ?? [])
  const pendingCount = $derived(status?.packages.filter(p => p.kind !== 'held').length ?? 0)
  const heldCount = $derived(status?.packages.filter(p => p.kind === 'held').length ?? 0)
  const canWrite = $derived(allowWrites && !!status?.manager.available && !submitting && activeJob === null)

  async function load() {
    if (stopped) return
    controller?.abort(); controller = new AbortController()
    const request = controller
    loading = true; error = ''
    try { const result = await invokeTool<UpdateStatus>('updates_status', {}, request.signal); if (!stopped && !request.signal.aborted) status = result }
    catch (e) { if (!request.signal.aborted) error = e instanceof Error ? e.message : 'Could not read pending updates' }
    finally { if (!request.signal.aborted) loading = false }
  }
  async function perform(action: 'refresh' | 'install' | 'packages') {
    submitting = true; error = ''; feedback = ''; failed = false
    try { const job = await startJob(action === 'packages' ? 'packages_install' : `updates_${action}`, action === 'packages' ? { packages: packageNames.trim(), confirm: true } : action === 'install' ? { confirm: true } : {}); activeJob = job.id; feedback = `${action === 'install' ? 'System update' : 'Metadata refresh'} started. Follow its progress in Activity.` }
    catch (e) { error = e instanceof Error ? e.message : 'Could not start update job' }
    finally { submitting = false; confirm = false; installPackages = false }
  }
  onMount(() => {
    const finished = async (event: Event) => {
      const job = (event as CustomEvent<Job>).detail
      if (job.id !== activeJob) return
      activeJob = null; failed = job.state !== 'succeeded'
      const result = job.result as { recheckRequired?: boolean } | undefined
      feedback = failed ? job.error || `Operation ${job.state}. Check Activity and native package state before retrying.` : result?.recheckRequired ? 'The package manager was updated. Review remaining updates and run installation again.' : job.tool === 'updates_refresh' ? 'Metadata refresh completed.' : 'System package update completed. Review the remaining packages and reboot status below.'
      await load()
    }
    window.addEventListener('serverman:job-finished', finished); void load()
    return () => { stopped = true; controller?.abort(); window.removeEventListener('serverman:job-finished', finished) }
  })
</script>

<div class="compose-toolbar"><div class="service-summary"><strong>{status?.manager.title || 'System packages'}</strong><span>{status?.distribution}</span></div><div class="update-actions"><button onclick={load} disabled={loading}>{loading ? 'Reading…' : 'Reload list'}</button><button onclick={() => { void perform('refresh') }} disabled={!canWrite || !status?.manager.canRefresh}>Refresh metadata</button><button class="primary" onclick={() => { confirm = true }} disabled={!canWrite}>Install system updates</button></div></div>
<form class="compose-toolbar" onsubmit={(event) => { event.preventDefault(); installPackages = true }}><label for="package-names">Install repository packages</label><input id="package-names" placeholder="Package names separated by spaces" bind:value={packageNames} disabled={!canWrite} required maxlength={4096} /><button class="primary" disabled={!canWrite || !packageNames.trim()}>Install packages…</button></form>
{#if !allowWrites}<div class="policy-note"><span class="connection-dot"></span>Read only. Management writes can be enabled in the server configuration.</div>{/if}
{#if error}<div class="notice error compose-notice" role="alert"><p>{error}</p><button onclick={load}>Retry</button></div>{/if}
{#if feedback}<div class="job-feedback" class:failed role="status">{feedback}</div>{/if}
{#if loading && !status}<section class="panel empty-state" aria-busy="true"><span class="loading-ring"></span><h2>Reading package updates</h2><p>Inspecting cached repository metadata…</p></section>{/if}
{#if status}
  {#if !status.manager.available}<section class="panel empty-state"><span class="module-icon"><Icon name="updates" size={28} /></span><h2>System updates unavailable</h2><p>{status.manager.reason}</p></section>{:else}
    <div class="update-summary-grid"><section class="panel update-summary"><span class="eyebrow">PENDING PACKAGES</span><strong>{status.valid ? pendingCount : '—'}</strong><p>{status.valid ? 'Candidates from cached metadata' : 'Update inventory unavailable'}</p></section><section class="panel update-summary"><span class="eyebrow">DEFERRED PACKAGES</span><strong>{status.valid ? heldCount : '—'}</strong><p>Held, phased or ignored entries reported by the manager</p></section><section class="panel update-summary"><span class="eyebrow">REBOOT STATUS</span><strong class="reboot-value" class:service-failed={status.reboot.required === true}>{status.reboot.required === true ? 'Declared' : status.reboot.required === false ? 'No marker' : 'Unknown'}</strong><p>{status.reboot.reason}</p></section></div>
    <div class="backend-notice" role="status">{status.notice}</div>{#if status.manager.refreshReason}<p class="resource-hint update-cache-note">{status.manager.refreshReason}</p>{/if}
    <div class="update-list-heading"><div><h2>Package candidates</h2><p>{status.cacheSource} · read {new Date(status.readAt).toLocaleTimeString()}</p></div><div class="compose-search"><label class="sr-only" for="update-filter">Filter package candidates</label><input id="update-filter" type="search" placeholder="Find a package…" bind:value={filter} /><label class="sr-only" for="update-kind">Update type</label><select id="update-kind" bind:value={kind}><option value="all">All packages</option><option value="upgrade">Upgrades</option><option value="install">Dependencies</option><option value="held">Deferred</option></select></div></div>
    {#if !status.valid}<section class="panel empty-state"><h2>Pending packages are unknown</h2><p>Refresh metadata when available, then reload the list. Native command output and access errors appear in Activity.</p></section>{:else if !packages.length}<section class="panel empty-state"><span class="module-icon"><Icon name="updates" size={28} /></span><h2>{filter || kind !== 'all' ? 'No matching packages' : 'No pending packages in cached metadata'}</h2><p>{filter || kind !== 'all' ? 'Try another package name or type.' : 'Refresh repositories to check for newer packages. This view does not check third-party application stores or manually installed software.'}</p></section>{:else}<section class="panel update-package-list" aria-label="Package updates">{#each packages as pkg}<article class="update-package-row"><div class="update-package-name"><strong>{pkg.name}</strong><span>{pkg.architecture || 'Native architecture'} · {updateKinds[pkg.kind]}</span></div><div class="update-package-versions"><span class="mono">{pkg.installed || (pkg.kind === 'install' ? 'Not installed' : 'Installed version not reported')}</span><Icon name="arrow" size={14} /><strong class="mono">{pkg.candidate || 'Deferred by package policy'}</strong></div></article>{/each}</section>{/if}
  {/if}
{/if}
<ConfirmDialog open={confirm} title="Install system package updates" description="Refresh the configured repositories and install normal system updates. Packages may add dependencies, replace obsolete packages or restart services, and the resolved plan may differ from this cached list. No distribution migration or reboot is requested. Cancelling during installation may require recovery with the native package tools." label="Install updates" busy={submitting} onConfirm={() => { void perform('install') }} onClose={() => { confirm = false; installPackages = false }} />

<ConfirmDialog open={installPackages} title="Install repository packages" description={`Install ${packageNames.trim()} and required dependencies. On Arch this also upgrades the system to avoid partial upgrades. Packages may restart services. Cancellation may require native package recovery.`} label="Install packages" busy={submitting} onConfirm={() => { void perform('packages') }} onClose={() => { installPackages = false }} />
