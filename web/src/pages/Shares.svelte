<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import ConfirmDialog from '../lib/ConfirmDialog.svelte'
  import { invokeTool, startJob, type Job } from '../lib/api'
  import { runtimeLabels, type Share, type ShareInventory, type ShareProtocol } from '../lib/shares'

  let { allowWrites }: { allowWrites: boolean } = $props()
  let protocol = $state<ShareProtocol>('samba'), inventory = $state<ShareInventory | null>(null)
  let loading = $state(true), error = $state(''), filter = $state('')
  let formOpen = $state(false), editing = $state(false), name = $state(''), path = $state(''), readOnly = $state(true)
  let guest = $state(false), browseable = $state(true), users = $state(''), clients = $state(''), rootSquash = $state(true), security = $state('sys'), formRevision = $state('')
  let pending = $state<{ tool: string; args: Record<string, unknown>; title: string; description: string; label: string } | null>(null)
  let submitting = $state(false), activeJob = $state<string | null>(null), feedback = $state(''), failed = $state(false)
  let controller: AbortController | undefined, stopped = false
  const capability = $derived(inventory?.backends.find(b => b.protocol === protocol))
  const shares = $derived(inventory?.shares.filter(s => `${s.name} ${s.path} ${s.clients?.join(' ') ?? ''}`.toLowerCase().includes(filter.toLowerCase())) ?? [])
  const canWrite = $derived(allowWrites && !!capability?.canManage && !submitting && activeJob === null)

  async function load() {
    if (stopped) return
    controller?.abort(); controller = new AbortController()
    const request = controller, target = protocol
    loading = true; error = ''
    try { const result = await invokeTool<ShareInventory>('shares_list', { protocol: target }, request.signal); if (!stopped && !request.signal.aborted && protocol === target) inventory = result }
    catch (e) { if (!request.signal.aborted) error = e instanceof Error ? e.message : 'Could not read shares' }
    finally { if (!request.signal.aborted) loading = false }
  }
  function switchProtocol(value: ShareProtocol) { protocol = value; inventory = null; formOpen = false; filter = ''; void load() }
  function edit(share?: Share) {
    editing = !!share; name = share?.name ?? ''; path = share?.path ?? ''; readOnly = share?.readOnly ?? true
    guest = share?.guest ?? false; browseable = share?.browseable ?? true; users = share?.users?.join(', ') ?? ''
    clients = share?.clients?.join(', ') ?? ''; rootSquash = share?.rootSquash ?? true; security = share?.security ?? 'sys'
    formRevision = capability?.revision ?? ''; formOpen = true; error = ''
  }
  function review() {
    const args: Record<string, unknown> = { name, path, readOnly, revision: formRevision, confirm: true }
    if (protocol === 'samba') Object.assign(args, { guest, browseable, users: guest ? '' : users })
    else Object.assign(args, { clients, rootSquash, security })
    const access = protocol === 'samba' ? guest ? 'guest access' : `users/groups: ${users}` : `clients: ${clients}; root squashing ${rootSquash ? 'enabled' : 'disabled'}; security ${security}`
    pending = { tool: `${protocol}_share_save`, args, title: `${editing ? 'Update' : 'Create'} ${protocol === 'samba' ? 'Samba share' : 'NFS export'}: ${name}`, description: `Publish ${path} with ${readOnly ? 'read-only' : 'read/write'} access for ${access}. This applies the share configuration and can affect connected clients. The directory must already exist; filesystem permissions and accounts are not changed.`, label: 'Save and apply' }
  }
  function remove(share: Share) { pending = { tool: `${protocol}_share_remove`, args: { name: share.name, revision: capability?.revision, confirm: true }, title: `Remove share: ${share.name}`, description: `Remove the managed ${protocol === 'samba' ? 'Samba share' : 'NFS export'} for ${share.path} and apply the change. Connected clients may lose access. The directory and its data are retained.`, label: 'Remove share' } }
  function setup() { pending = { tool: 'shares_setup', args: { protocol, confirm: true }, title: `Set up ${protocol === 'samba' ? 'Samba' : 'NFS'}`, description: 'Install the native server packages, prepare the managed configuration, and enable and start the server. Existing configuration and shared data are preserved. On Arch this also upgrades the system. Configure network access and Samba passwords separately as needed.', label: 'Set up server' } }
  async function perform() {
    if (!pending) return
    const action = pending
    submitting = true; error = ''; feedback = ''; failed = false
    try { const job = await startJob(action.tool, action.args); activeJob = job.id; feedback = 'Share job started. Follow validation, apply and any rollback in Activity.'; formOpen = false }
    catch (e) { error = e instanceof Error ? e.message : 'Could not start share job' }
    finally { submitting = false; pending = null }
  }
  onMount(() => {
    const finished = async (event: Event) => { const job = (event as CustomEvent<Job>).detail; if (job.id !== activeJob) return; activeJob = null; failed = job.state !== 'succeeded'; feedback = failed ? job.error || `Operation ${job.state}.` : 'Share configuration applied. Refresh client connections to use updated Samba settings.'; await load() }
    window.addEventListener('serverman:job-finished', finished); void load()
    return () => { stopped = true; controller?.abort(); window.removeEventListener('serverman:job-finished', finished) }
  })
</script>

<div class="compose-toolbar"><div class="engine-tabs">{#each ['samba', 'nfs'] as value}<button class:active={protocol === value} aria-pressed={protocol === value} onclick={() => switchProtocol(value as ShareProtocol)}><Icon name="shares" size={17} />{value === 'samba' ? 'Samba / SMB' : 'NFS'}</button>{/each}</div><div class="compose-search"><label class="sr-only" for="share-filter">Filter shares</label><input id="share-filter" type="search" placeholder="Filter shares…" bind:value={filter} /><button onclick={load} disabled={loading}>{loading ? 'Refreshing…' : 'Refresh'}</button><button onclick={setup} disabled={!allowWrites || submitting || activeJob !== null}>Set up server…</button><button class="primary" onclick={() => edit()} disabled={!canWrite}>New share</button></div></div>
{#if !allowWrites}<div class="policy-note"><span class="connection-dot"></span>Read only. Management writes can be enabled in the server configuration.</div>{/if}
{#if error}<div class="notice error compose-notice" role="alert"><p>{error}</p><button onclick={load}>Refresh inventory</button></div>{/if}
{#if feedback}<div class="job-feedback" class:failed role="status">{feedback}</div>{/if}
{#if capability}<div class="compose-root"><span class="mono">{capability.managedFile}</span><span class="capability-state" class:available={capability.canManage}>{capability.canManage ? 'Management ready' : capability.available ? 'Setup or access required' : 'Tools unavailable'}</span></div>{#if capability.reason}<div class="backend-notice" role="status">{capability.reason}</div>{/if}
  {#if capability.setupRequired}<section class="panel share-setup"><h2>Complete host setup</h2>{#if protocol === 'samba'}<p>Add this literal include once at the end of the Samba configuration selected by server-man. Use a new, unused managed filename; server-man creates its owned fragment on the first save.</p><pre>{`include = ${capability.managedFile}`}</pre>{:else}<p>Create the exports.d directory and configure the NFS server using your distribution's normal setup. server-man owns only serverman.exports; use Set up server to initialize it.</p>{/if}<p>Set up server can install the native packages and prepare this configuration. Refresh after setup.</p></section>{/if}
  {#if capability.notice}<p class="resource-hint share-runtime-note">{capability.notice}</p>{/if}
{/if}
{#if inventory?.warnings.length}<details class="monitor-warnings"><summary>Share notices ({inventory.warnings.length})</summary><ul>{#each inventory.warnings as warning}<li>{warning}</li>{/each}</ul></details>{/if}
{#if formOpen}<form class="panel machine-form share-form" onsubmit={event => { event.preventDefault(); review() }}><h2>{editing ? 'Edit managed share' : 'Create managed share'}</h2><div class="share-form-fields"><label>Share name<input required maxlength="64" pattern="[A-Za-z0-9][A-Za-z0-9_.\-]*" bind:value={name} disabled={editing} placeholder="documents" /></label><label>Existing directory on server<input required maxlength="4096" bind:value={path} placeholder="/srv/shares/documents" /></label></div><div class="share-checkboxes"><label><input type="checkbox" bind:checked={readOnly} /> Read-only access</label>{#if protocol === 'samba'}<label><input type="checkbox" bind:checked={guest} /> Allow guest access</label><label><input type="checkbox" bind:checked={browseable} /> Visible when browsing</label>{:else}<label><input type="checkbox" bind:checked={rootSquash} /> Map remote root to anonymous user</label>{/if}</div>
  {#if protocol === 'samba'}{#if !guest}<label>Allowed users or groups<input required maxlength="4096" bind:value={users} placeholder="alice, @staff" /></label>{/if}<p class="resource-hint">Use existing Samba accounts and local users/groups. server-man does not create users or change directory ownership, modes, ACLs or SELinux labels. Guest access also depends on the host's Samba settings.</p>{:else}<div class="share-form-fields"><label>Allowed clients or networks<input required maxlength="2048" bind:value={clients} placeholder="192.168.1.0/24, 2001:db8::/64" /></label><label>Authentication<select bind:value={security}><option value="sys">AUTH_SYS</option><option value="krb5">Kerberos</option><option value="krb5i">Kerberos with integrity</option><option value="krb5p">Kerberos with privacy</option></select></label></div><p class="resource-hint">Use IP addresses/CIDR networks, or an explicit * for all clients. Exports use synchronous writes, reserved client ports and no subtree checking. Kerberos requires existing host setup.</p>{/if}<p class="resource-hint">Changes use the revision captured when this form opened. If configuration changed elsewhere, refresh and reopen the form.</p><div class="dialog-actions"><button type="button" onclick={() => { formOpen = false }}>Cancel</button><button class="primary" disabled={!canWrite}>Review and apply</button></div></form>{/if}
{#if loading && !inventory}<section class="panel empty-state" aria-busy="true"><span class="loading-ring"></span><h2>Reading file shares</h2><p>Inspecting configured shares and runtime metadata…</p></section>{:else if inventory && !shares.length}<section class="panel empty-state"><span class="module-icon"><Icon name="shares" size={28} /></span><h2>{filter ? 'No matching shares' : inventory.warnings.length ? 'No share metadata available' : 'No shares configured'}</h2><p>{filter ? 'Try a share name or directory path.' : 'Existing host shares appear here for inspection. New shares use the application-owned fragment.'}</p></section>{:else}<div class="share-grid">{#each shares as share}<article class="panel share-card"><div class="project-card-top"><span class="module-icon"><Icon name="shares" size={20} /></span><span class="share-owner" class:managed={share.managed}>{share.managed ? 'Managed by server-man' : 'Host configuration'}</span></div><h2>{share.name}</h2><p class="mono share-path">{share.path || 'Dynamic share path'}</p><div class="share-status"><span class="project-state" class:running={share.configured}>{share.configured ? 'Configured' : share.managed ? 'Configuration differs' : 'Runtime only'}</span><span class:service-failed={share.runtime === 'mismatch'}>{runtimeLabels[share.runtime] ?? share.runtime}</span></div><dl class="share-facts"><div><dt>Access</dt><dd>{protocol === 'samba' && !share.configured && !share.managed ? 'Not reported' : share.readOnly ? 'Read only' : 'Read/write'}</dd></div>{#if protocol === 'samba'}<div><dt>Allowed users</dt><dd>{share.guest ? 'Guest access enabled' : share.users?.join(', ') || 'Inherited host settings'}</dd></div>{#if share.connections !== undefined}<div><dt>Connections</dt><dd>{share.connections}</dd></div>{/if}{:else}<div><dt>Clients</dt><dd>{share.clients?.join(', ') || 'Not reported'}</dd></div><div><dt>Security</dt><dd>{share.security ?? 'sys'} · root squashing {share.rootSquash ? 'on' : 'off'}</dd></div>{/if}</dl>{#if share.managed}<div class="share-card-actions"><button onclick={() => edit(share)} disabled={!canWrite}>Edit / apply</button><button class="danger-button" onclick={() => remove(share)} disabled={!canWrite}>Remove</button></div>{:else}<p class="resource-hint">This share remains under host configuration. Use its native configuration to change it.</p>{/if}</article>{/each}</div>{/if}
<ConfirmDialog open={pending !== null} title={pending?.title ?? ''} description={pending?.description ?? ''} label={pending?.label ?? 'Confirm'} busy={submitting} onConfirm={() => { void perform() }} onClose={() => { pending = null }} />
