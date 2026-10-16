<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import ConfirmDialog from '../lib/ConfirmDialog.svelte'
  import { invokeTool, startJob, type Job } from '../lib/api'
  import { backendTitles, machineActions, type Machine, type MachineBackend, type MachineInventory } from '../lib/machines'

  let { allowWrites }: { allowWrites: boolean } = $props()
  let backend = $state<MachineBackend>('lxd')
  let inventory = $state<MachineInventory | null>(null)
  let loading = $state(true), error = $state(''), filter = $state('')
  let selectedName = $state(''), details = $state<Machine | null>(null), detailLoading = $state(false), detailError = $state('')
  let form = $state<'launch' | 'define' | null>(null)
  let instanceName = $state(''), image = $state('ubuntu:24.04'), kind = $state('container'), xmlFile = $state('')
  let autostart = $state(true), submitting = $state(false), activeJob = $state<string | null>(null)
  let feedback = $state(''), failed = $state(false)
  let pending = $state<{ tool: string; args: Record<string, unknown>; title: string; description: string } | null>(null)
  let stopped = false, listController: AbortController | undefined, detailController: AbortController | undefined
  const capability = $derived(inventory?.backends.find(c => c.id === backend))
  const machines = $derived(inventory?.machines.filter(m => m.name.toLowerCase().includes(filter.toLowerCase())) ?? [])
  const selected = $derived(inventory?.machines.find(m => m.name === selectedName))
  const busy = $derived(submitting || activeJob !== null)
  const canWrite = $derived(allowWrites && !!capability?.available && !busy)

  function memory(value?: number) { return value === undefined ? 'Memory unavailable' : `${(value / 1024 ** 2).toLocaleString(undefined, { maximumFractionDigits: 1 })} MiB` }

  async function load() {
    if (stopped) return
    listController?.abort(); listController = new AbortController()
    const controller = listController, target = backend
    loading = true; error = ''
    try {
      const result = await invokeTool<MachineInventory>('machines_list', { backend: target }, controller.signal)
      if (stopped || controller.signal.aborted || backend !== target) return
      inventory = result
      if (!result.machines.some(m => m.name === selectedName)) { selectedName = ''; details = null; detailController?.abort(); detailLoading = false }
    } catch (e) { if (!controller.signal.aborted) error = e instanceof Error ? e.message : 'Could not read machines' }
    finally { if (!controller.signal.aborted) loading = false }
  }

  function switchBackend(value: MachineBackend) { backend = value; inventory = null; selectedName = ''; details = null; detailError = ''; form = null; detailLoading = false; detailController?.abort(); void load() }

  async function inspect(machine: Machine) {
    if (stopped || !machine.manageable) return
    detailController?.abort(); detailController = new AbortController()
    const controller = detailController
    if (selectedName !== machine.name) details = null
    selectedName = machine.name; detailLoading = true; detailError = ''
    try {
      const result = await invokeTool<Machine>('machines_details', { backend: machine.backend, name: machine.uuid || machine.name }, controller.signal)
      if (stopped || controller.signal.aborted || backend !== machine.backend || selectedName !== machine.name) return
      details = result; autostart = result.autostart ?? true
      if (inventory) inventory.machines = inventory.machines.map(m => m.name === machine.name ? result : m)
    } catch (e) { if (!controller.signal.aborted) { details = null; detailError = e instanceof Error ? e.message : 'Could not inspect machine' } }
    finally { if (!controller.signal.aborted) detailLoading = false }
  }

  async function perform(tool: string, args: Record<string, unknown>) {
    submitting = true; error = ''; feedback = ''; failed = false
    try { const job = await startJob(tool, args); activeJob = job.id; feedback = 'Management job started. Follow its output in Activity.'; form = null }
    catch (e) { error = e instanceof Error ? e.message : 'Could not start machine job' }
    finally { submitting = false; pending = null }
  }

  function action(machine: Machine, action: string) {
    const args: Record<string, unknown> = { name: machine.uuid || machine.name }
    const tool = `${machine.backend}_${action}`
    if (action === 'start') { void perform(tool, args); return }
    args.confirm = true
    let description = `${machineActions[action]} ${machine.name}. This interrupts its workload.`
    if (action === 'delete') description = `Permanently delete ${machine.name}, its snapshots and its root storage. The instance must be stopped. This cannot be undone.`
    if (action === 'undefine') description = `Remove ${machine.name}'s persistent domain definition. The domain must be shut down. Disks and NVRAM are retained; existing snapshots or managed-save metadata may prevent removal.`
    if (action === 'shutdown' || action === 'reboot') description += ' This submits a guest request; refresh status to check whether the guest completed it.'
    pending = { tool, args, title: `${machineActions[action]}: ${machine.name}`, description }
  }

  function define() { pending = { tool: 'kvm_define', args: { file: xmlFile, confirm: true }, title: 'Define KVM domain', description: `Register the domain from ${xmlFile}. An existing definition with the same identity may be replaced. The domain is not started. XML files and their host-device references must be trusted.` } }
  function actionDisabled(machine: Machine, action: string) { return !canWrite || !machine.manageable || (action === 'delete' && machine.state !== 'stopped') || (action === 'undefine' && machine.state !== 'shut off') }

  onMount(() => {
    const finished = async (event: Event) => {
      const job = (event as CustomEvent<Job>).detail
      if (job.id !== activeJob) return
      activeJob = null; failed = job.state !== 'succeeded'; feedback = failed ? job.error || `Operation ${job.state}.` : 'Operation completed. Guest shutdown and reboot requests may still be in progress.'
      const machine = selected
      await load()
      if (!stopped && machine && selectedName === machine.name && backend === machine.backend) await inspect(machine)
    }
    window.addEventListener('serverman:job-finished', finished)
    void load()
    return () => { stopped = true; listController?.abort(); detailController?.abort(); window.removeEventListener('serverman:job-finished', finished) }
  })
</script>

<div class="compose-toolbar"><div class="engine-tabs">{#each ['lxd', 'lxc', 'kvm'] as value}<button class:active={backend === value} aria-pressed={backend === value} onclick={() => switchBackend(value as MachineBackend)}><Icon name="machines" size={17} />{backendTitles[value as MachineBackend]}</button>{/each}</div><div class="compose-search"><label class="sr-only" for="machine-filter">Filter machines</label><input id="machine-filter" type="search" placeholder="Filter machines…" bind:value={filter} /><button onclick={load} disabled={loading}>{loading ? 'Refreshing…' : 'Refresh'}</button></div></div>
{#if !allowWrites}<div class="policy-note"><span class="connection-dot"></span>Read only. Management writes can be enabled in the server configuration.</div>{/if}
{#if error}<div class="notice error compose-notice" role="alert"><p>{error}</p><button onclick={load}>Retry</button></div>{/if}
{#if feedback}<div class="job-feedback" class:failed role="status">{feedback}</div>{/if}
{#if capability}<div class="compose-root"><span class="mono">{capability.context}</span><span class="capability-state" class:available={capability.available}>{capability.available ? 'Tools installed' : 'Unavailable'}</span></div>{#if capability.reason || capability.notice}<div class="backend-notice" role="status">{capability.reason || capability.notice}</div>{/if}{/if}
{#if backend === 'lxd' || backend === 'kvm'}<div class="machine-create"><p>{backend === 'lxd' ? 'Create an instance using the configured LXD profiles and storage.' : 'Register an existing KVM domain XML file from the configured import root.'}</p><button class="primary" disabled={!canWrite} onclick={() => { form = form ? null : backend === 'lxd' ? 'launch' : 'define' }}>{form ? 'Close form' : backend === 'lxd' ? 'Launch instance' : 'Define from XML'}</button></div>{/if}
{#if form}
  <form class="panel machine-form" onsubmit={event => { event.preventDefault(); if (form === 'launch') void perform('lxd_launch', { name: instanceName, image, kind }); else define() }}>
    <h2>{form === 'launch' ? 'Launch LXD instance' : 'Define KVM domain'}</h2>
    {#if form === 'launch'}<div class="machine-form-fields"><label>Instance name<input required maxlength="63" pattern="[A-Za-z][A-Za-z0-9-]*" bind:value={instanceName} placeholder="web-01" /></label><label>Image alias or fingerprint<input required maxlength="256" bind:value={image} placeholder="ubuntu:24.04" /></label><label>Instance type<select bind:value={kind}><option value="container">System container</option><option value="virtual-machine">Virtual machine</option></select></label></div><p class="resource-hint">Uses this LXD project's default profile, network and storage. Image downloads may take several minutes.</p>{:else}<label>Existing XML file on server<input required maxlength="4096" bind:value={xmlFile} placeholder="/srv/vms/domain.xml" /></label><p class="resource-hint">The file must be inside libvirtXMLRoot, which defaults to vmRoot. Defining a domain can replace an existing persistent definition. No disks are created and the domain is not started.</p>{/if}
    <div class="dialog-actions"><button type="button" onclick={() => { form = null }}>Cancel</button><button class="primary" disabled={!canWrite}>{form === 'launch' ? 'Launch instance' : 'Review definition'}</button></div>
  </form>
{/if}
{#if loading && !inventory}<section class="panel empty-state" aria-busy="true"><span class="loading-ring"></span><h2>Reading machine inventory</h2><p>Connecting to {backendTitles[backend]}…</p></section>{:else if inventory && !machines.length}<section class="panel empty-state"><span class="module-icon"><Icon name="machines" size={28} /></span><h2>{filter ? 'No matching machines' : capability?.notice ? 'Inventory unavailable' : capability?.available ? 'No machines found' : `${backendTitles[backend]} is unavailable`}</h2><p>{filter ? 'Try another machine name.' : 'Inventory follows the server account and configured backend context. Refresh after completing backend setup.'}</p></section>{:else}
  <div class="project-grid">{#each machines as machine}<article class="panel project-card" class:selected={selectedName === machine.name}><div class="project-card-top"><span class="module-icon"><Icon name="machines" size={20} /></span><span class="project-state" class:running={machine.state === 'running'}>{machine.state}</span></div><button class="project-name" disabled={!machine.manageable} onclick={() => inspect(machine)}>{machine.name}</button><div class="project-meta"><span>{machine.kind === 'virtual-machine' ? 'Virtual machine' : 'System container'}</span>{#if machine.cpus !== undefined}<span>{machine.cpus} vCPU</span>{/if}</div><div class="project-card-footer"><span>{memory(machine.memoryBytes)}</span><button class="small-button" disabled={!machine.manageable} onclick={() => inspect(machine)}>Details <Icon name="arrow" size={13} /></button></div>{#if !machine.manageable}<p class="resource-hint">This name is outside the supported identifier format; manage it with its native tools.</p>{/if}</article>{/each}</div>
{/if}
{#if selected}
  <section class="panel project-details" aria-labelledby="machine-heading"><div class="project-detail-heading"><div><div class="eyebrow">{backendTitles[backend]}</div><h2 id="machine-heading">{selected.name}</h2></div><button class="small-button" disabled={detailLoading} onclick={() => inspect(selected)}>Refresh details</button></div><div class="project-actions">{#each capability?.actions.filter(action => !['launch', 'define', 'autostart'].includes(action)) ?? [] as value}<button class:danger-button={value === 'delete' || value === 'undefine'} class:primary={value === 'start'} disabled={actionDisabled(details ?? selected, value)} onclick={() => action(details ?? selected, value)}>{machineActions[value]}</button>{/each}</div>
    {#if detailError}<p class="form-error" role="alert">{detailError}</p>{:else if detailLoading}<p class="resource-hint">Reading machine details…</p>{/if}
    {#if details}<dl class="machine-facts"><div><dt>State</dt><dd>{details.state}</dd></div><div><dt>Memory</dt><dd>{memory(details.memoryBytes)}</dd></div>{#if details.cpus !== undefined}<div><dt>Virtual CPUs</dt><dd>{details.cpus}</dd></div>{/if}{#if details.architecture}<div><dt>Architecture</dt><dd>{details.architecture}</dd></div>{/if}{#if details.location}<div><dt>Location</dt><dd>{details.location}</dd></div>{/if}{#if details.uuid}<div><dt>UUID</dt><dd class="mono">{details.uuid}</dd></div>{/if}{#if details.persistent !== undefined}<div><dt>Persistent</dt><dd>{details.persistent ? 'Yes' : 'No'}</dd></div>{/if}{#if details.autostart !== undefined}<div><dt>Autostart</dt><dd>{details.autostart ? 'Enabled' : 'Disabled'}</dd></div>{/if}<div><dt>IP addresses</dt><dd>{details.ips.join(', ') || 'Not reported'}</dd></div></dl>
      {#if backend === 'kvm'}<form class="machine-autostart" onsubmit={event => { event.preventDefault(); if (details) void perform('kvm_autostart', { name: details.uuid || details.name, enabled: autostart }) }}><label for="machine-autostart">Start with host</label><select id="machine-autostart" bind:value={autostart}><option value={true}>Enabled</option><option value={false}>Disabled</option></select><button disabled={!canWrite || details.persistent === false}>Save autostart</button></form><p class="resource-hint">Shutdown and reboot submit guest requests. Refresh to observe their eventual state. Undefine retains disks and NVRAM.</p>{/if}
    {/if}
  </section>
{/if}
<ConfirmDialog open={pending !== null} title={pending?.title ?? ''} description={pending?.description ?? ''} label="Confirm change" busy={submitting} onConfirm={() => { if (pending) void perform(pending.tool, pending.args) }} onClose={() => { pending = null }} />
