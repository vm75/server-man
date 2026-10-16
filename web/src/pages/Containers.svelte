<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import ConfirmDialog from '../lib/ConfirmDialog.svelte'
  import { invokeTool, startJob, type Job } from '../lib/api'
  import { actionLabels, destructive, key, stateLabels, type ComposeAction, type ComposeInventory, type Project, type ProjectDetails, type ProjectLogs } from '../lib/compose'

  let { allowWrites }: { allowWrites: boolean } = $props()
  let inventory = $state<ComposeInventory | null>(null)
  let engine = $state('podman')
  let filter = $state('')
  let loading = $state(true)
  let error = $state('')
  let selectedKey = $state('')
  let details = $state<ProjectDetails | null>(null)
  let detailLoading = $state(false)
  let detailError = $state('')
  let logs = $state<ProjectLogs | null>(null)
  let logLoading = $state(false)
  let logError = $state('')
  let showLogs = $state(false)
  let service = $state('')
  let tail = $state(200)
  let pending = $state<{ project: Project; action: ComposeAction } | null>(null)
  let submitting = $state(false)
  let activeJob = $state<string | null>(null)
  let jobMessage = $state('')
  let jobFailed = $state(false)
  let editorOpen = $state(false)
  let editorKind = $state<'compose' | 'env' | 'new-project'>('compose')
  let editorProject = $state<Project | null>(null)
  let editorFilename = $state('')
  let editorNewEnv = $state(false)
  let editorRevision = $state('')
  let editorContent = $state('')
  let editorError = $state('')
  let editorBusy = $state(false)
  let editorDialog = $state<HTMLDialogElement>()
  let dataDirectory = $state('')
  let pendingSelection = ''
  let stopped = false
  let inventoryController: AbortController | undefined, detailController: AbortController | undefined, logController: AbortController | undefined
  const currentEngine = $derived(inventory?.engines.find(e => e.name === engine))
  const projects = $derived(inventory?.projects.filter(p => p.engine === engine && p.name.toLowerCase().includes(filter.toLowerCase())) ?? [])
  const selected = $derived(inventory?.projects.find(p => key(p) === selectedKey))
  const services = $derived([...new Set(details?.containers.map(c => c.service).filter(Boolean) ?? [])].sort())

  $effect(() => { if (!editorDialog) return; if (editorOpen && !editorDialog.open) editorDialog.showModal(); else if (!editorOpen && editorDialog.open) editorDialog.close() })

  async function load() {
    if (stopped) return
    inventoryController?.abort()
    inventoryController = new AbortController()
    const controller = inventoryController
    loading = true; error = ''
    try { const response = await invokeTool<ComposeInventory>('compose_projects', {}, controller.signal); if (stopped || controller.signal.aborted) return; inventory = response; if (!selected) { selectedKey = ''; details = null; showLogs = false } }
    catch (e) { if (!controller.signal.aborted) error = e instanceof Error ? e.message : 'Could not discover Compose projects' }
    finally { if (!controller.signal.aborted) loading = false }
  }

  async function inspect(project: Project) {
    if (stopped) return
    detailController?.abort(); logController?.abort()
    detailController = new AbortController()
    const controller = detailController
    if (selectedKey !== key(project)) { details = null; logs = null; showLogs = false; service = '' }
    selectedKey = key(project); detailLoading = true; detailError = ''
    try {
      const response = await invokeTool<ProjectDetails>('compose_status', { engine: project.engine, project: project.name }, controller.signal)
      if (!controller.signal.aborted && selectedKey === key(project)) { details = response; if (inventory) inventory.projects = inventory.projects.map(p => key(p) === key(project) ? response.project : p) }
    } catch (e) { if (!controller.signal.aborted) { details = null; detailError = e instanceof Error ? e.message : 'Could not read project status' } }
    finally { if (!controller.signal.aborted) detailLoading = false }
  }

  async function readLogs() {
    if (!selected || stopped) return
    const project = selected
    logController?.abort(); logController = new AbortController()
    const controller = logController
    showLogs = true; logLoading = true; logError = ''; logs = null
    try { const response = await invokeTool<ProjectLogs>('compose_logs', { engine: project.engine, project: project.name, service, tail }, controller.signal); if (!stopped && !controller.signal.aborted && selectedKey === key(project)) logs = response }
    catch (e) { if (!controller.signal.aborted) logError = e instanceof Error ? e.message : 'Could not read logs' }
    finally { if (!controller.signal.aborted) logLoading = false }
  }

  function requestAction(project: Project, action: ComposeAction) {
    if (destructive.has(action)) pending = { project, action }
    else void perform(project, action)
  }

  function beginEditor(kind: 'compose' | 'env' | 'new-project', project: Project | null, filename = '') {
    editorKind = kind; editorProject = project; editorFilename = filename; editorNewEnv = kind === 'env' && !filename; editorRevision = editorNewEnv ? 'missing' : ''; editorContent = kind === 'new-project' ? 'services:\n  app:\n    image: your-image:tag\n' : ''; editorError = ''; editorOpen = true
    if (kind === 'compose' && project) void loadEditorFile('compose_file_read', { engine: project.engine, project: project.name })
    if (kind === 'env' && project && filename) void loadEditorFile('compose_env_read', { engine: project.engine, project: project.name, filename })
  }

  async function loadEditorFile(tool: string, args: Record<string, unknown>) {
    editorBusy = true
    try { const file = await invokeTool<{ content: string; revision: string }>(tool, args); editorContent = file.content; editorRevision = file.revision }
    catch (e) { editorError = e instanceof Error ? e.message : 'Could not read file' }
    finally { editorBusy = false }
  }

  async function saveEditor() {
    if (editorBusy || !allowWrites) return
    editorBusy = true; editorError = ''
    try {
      let tool: string, args: Record<string, unknown>
      if (editorKind === 'new-project') {
        const project = editorFilename.trim()
        tool = 'compose_project_create'; args = { engine, project, content: editorContent }
        pendingSelection = `${engine}/${project}`
      } else if (editorProject && editorKind === 'compose') {
        tool = 'compose_file_save'; args = { engine: editorProject.engine, project: editorProject.name, revision: editorRevision, content: editorContent, confirm: true }
        pendingSelection = key(editorProject)
      } else if (editorProject) {
        tool = 'compose_env_save'; args = { engine: editorProject.engine, project: editorProject.name, filename: editorFilename.trim(), revision: editorRevision, content: editorContent, confirm: true }
        pendingSelection = key(editorProject)
      } else return
      const job = await startJob(tool, args)
      activeJob = job.id; jobMessage = `${editorKind === 'new-project' ? 'Project creation' : 'File save'} started. Follow its validation in Activity.`
      editorOpen = false; editorContent = ''
    } catch (e) { pendingSelection = ''; editorError = e instanceof Error ? e.message : 'Could not save file' }
    finally { editorBusy = false }
  }

  async function createDataDirectory() {
    if (!selected || !dataDirectory.trim() || submitting || !allowWrites) return
    submitting = true; error = ''
    try { const job = await startJob('compose_data_mkdir', { engine: selected.engine, project: selected.name, path: dataDirectory.trim() }); activeJob = job.id; jobMessage = `Data directory creation started for ${selected.name}.`; dataDirectory = '' }
    catch (e) { error = e instanceof Error ? e.message : 'Could not create data directory' }
    finally { submitting = false }
  }

  async function perform(project: Project, action: ComposeAction) {
    submitting = true; error = ''; jobMessage = ''; jobFailed = false
    try {
      const args: Record<string, unknown> = { engine: project.engine, project: project.name }
      if (destructive.has(action)) args.confirm = true
      const job = await startJob(`compose_${action}`, args)
      activeJob = job.id; jobMessage = `${actionLabels[action]} started for ${project.name}. Follow its output in Activity.`
    } catch (e) { error = e instanceof Error ? e.message : 'Could not start management job' }
    finally { submitting = false; pending = null }
  }

  function blocked(project: Project, action: ComposeAction) { return !allowWrites || !inventory?.engines.find(e => e.name === project.engine)?.available || submitting || !!activeJob || (project.disabled && action !== 'stop' && action !== 'down') }
  function confirmation() {
    if (!pending) return ''
    const target = `${pending.project.engine}/${pending.project.name}`
    if (pending.action === 'down') return `Stop and remove ${target}'s containers and networks. Named volumes and bind mounts are retained; data stored only in container writable layers is removed.`
    if (pending.action === 'up') return `Create or reconcile ${target} and start it in the background. Compose may recreate changed containers. Existing named volumes and bind mounts are retained.`
    return `${pending.action === 'stop' ? 'Stop' : 'Restart'} the services in ${target}. This interrupts the workload. Persistent volume and bind-mount data is retained.`
  }

  onMount(() => {
    const finished = async (event: Event) => {
      const job = (event as CustomEvent<Job>).detail
      if (job.id !== activeJob) return
      activeJob = null; jobMessage = job.state === 'succeeded' ? 'Project operation completed.' : job.error || `Operation ${job.state}.`
      jobFailed = job.state !== 'succeeded'
      const project = selected
      await load()
      if (stopped) return
      if (pendingSelection) { selectedKey = pendingSelection; pendingSelection = '' }
      const refreshed = inventory?.projects.find(p => key(p) === selectedKey)
      if (refreshed) await inspect(refreshed)
      else if (project && selectedKey === key(project)) await inspect(project)
    }
    window.addEventListener('serverman:job-finished', finished)
    void load()
    return () => { stopped = true; inventoryController?.abort(); detailController?.abort(); logController?.abort(); window.removeEventListener('serverman:job-finished', finished) }
  })
</script>

<div class="compose-toolbar"><div class="engine-tabs">{#each ['podman', 'docker'] as name}<button class:active={engine === name} aria-pressed={engine === name} onclick={() => { engine = name; selectedKey = ''; details = null; logs = null; showLogs = false; detailController?.abort(); logController?.abort(); detailLoading = false; logLoading = false }}><Icon name="containers" size={17} />{name === 'podman' ? 'Podman' : 'Docker'}<span>{inventory?.projects.filter(p => p.engine === name).length ?? '—'}</span></button>{/each}</div><div class="compose-search"><label class="sr-only" for="project-filter">Filter projects</label><input id="project-filter" type="search" placeholder="Filter projects…" bind:value={filter} /><button onclick={load} disabled={loading}>{loading ? 'Refreshing…' : 'Refresh'}</button>{#if allowWrites}<button class="primary" onclick={() => beginEditor('new-project', null)} disabled={!currentEngine?.available || !!activeJob}>New project</button>{/if}</div></div>
{#if !allowWrites}<div class="policy-note"><span class="connection-dot"></span>Read only. Management writes can be enabled in the server configuration.</div>{/if}
{#if error}<div class="notice error compose-notice" role="alert"><p>{error}</p><button onclick={load}>Retry</button></div>{/if}
{#if jobMessage}<div class="job-feedback" class:failed={jobFailed} role="status">{jobMessage}</div>{/if}
{#if currentEngine}
  <div class="compose-root"><span class="mono">{currentEngine.root}</span><span class="capability-state" class:available={currentEngine.available}>{currentEngine.available ? 'Engine installed' : 'Unavailable'}</span></div>
  {#if currentEngine.reason || currentEngine.notice}<div class="backend-notice" role="status">{currentEngine.reason || currentEngine.notice}</div>{/if}
{/if}
{#if inventory?.warnings.length}<details class="monitor-warnings"><summary>Discovery notices ({inventory.warnings.length})</summary><ul>{#each inventory.warnings as warning}<li>{warning}</li>{/each}</ul></details>{/if}
{#if loading && !inventory}<section class="panel empty-state" aria-busy="true"><span class="loading-ring"></span><h2>Discovering Compose projects</h2><p>Reading project directories and runtime state…</p></section>{:else if inventory && !projects.length}<section class="panel empty-state"><span class="module-icon"><Icon name="containers" size={28} /></span><h2>{filter ? 'No matching projects' : `No ${engine === 'podman' ? 'Podman' : 'Docker'} projects yet`}</h2><p>{filter ? 'Try another project name.' : 'Place a Compose file in an individual project directory under the configured root, then refresh.'}</p></section>{:else}
  <div class="project-grid">{#each projects as project}<article class="panel project-card" class:selected={selectedKey === key(project)}><div class="project-card-top"><span class="module-icon"><Icon name="containers" size={20} /></span><span class="project-state" class:running={project.state === 'running'} class:attention={project.state === 'degraded'}>{stateLabels[project.state] ?? project.state}</span></div><button class="project-name" onclick={() => inspect(project)} title={project.directory}>{project.name}</button><div class="project-meta"><span>{project.containers ? `${project.running} / ${project.containers} running` : 'Open for provider status'}</span>{#if project.disabled}<span class="disabled-badge">Disabled</span>{/if}</div><div class="project-card-footer"><span>{project.envFiles.length} env files</span><button class="small-button" onclick={() => inspect(project)}>Details <Icon name="arrow" size={13} /></button></div></article>{/each}</div>
{/if}

{#if selected}
  <section class="panel project-details" aria-labelledby="project-heading"><div class="project-detail-heading"><div><div class="eyebrow">{selected.engine.toUpperCase()} COMPOSE</div><h2 id="project-heading">{selected.name}</h2></div><div class="compose-edit-tools">{#if allowWrites}<button class="small-button" onclick={() => beginEditor('compose', selected)} disabled={!!activeJob}>Edit Compose</button>{/if}<button class="small-button" onclick={() => inspect(selected)} disabled={detailLoading}>Refresh status</button></div></div><p class="compose-path mono">{selected.file}</p>
    {#if selected.disabled}<div class="backend-notice">This project has a .disabled marker. Starting, applying, restarting and pulling are blocked. Stop and remove remain available.</div>{/if}
    <div class="project-actions">{#each ['up', 'start', 'stop', 'restart', 'pull', 'down'] as action}<button class:danger-button={action === 'down'} class:primary={action === 'up'} disabled={blocked(selected, action as ComposeAction)} onclick={() => requestAction(selected, action as ComposeAction)}>{actionLabels[action as ComposeAction]}</button>{/each}</div>
    {#if detailError}<p class="form-error" role="alert">{detailError}</p>{:else if detailLoading && !details}<p class="resource-hint">Reading provider status…</p>{/if}
    {#if details}
      {#each details.warnings as warning}<p class="resource-hint">{warning}</p>{/each}
      {#if details.pods.length}<div class="pod-list">{#each details.pods as pod}<div><Icon name="containers" size={17} /><strong>{pod.name}</strong><span>{pod.state} · {pod.containers} containers</span></div>{/each}</div>{/if}
      <div class="container-list">{#each details.containers as container}<article class="container-row"><div class="container-identity"><strong>{container.name}</strong><span>{container.service || (container.infra ? 'Pod infrastructure' : 'Container')} · {container.id.slice(0, 12)}</span><span class="container-image mono">{container.image}</span></div><div class="container-runtime"><span class="project-state" class:running={container.state === 'running'}>{container.state}{container.health ? ` · ${container.health}` : ''}</span><span>{container.status}</span>{#if container.podID}<span class="mono">Pod {container.podName || container.podID.slice(0, 12)}</span>{/if}{#if container.ports}<span class="mono">{container.ports}</span>{/if}</div></article>{/each}{#if !details.containers.length}<p class="resource-hint">No containers are created for this project.</p>{/if}</div>
    {/if}
    <details class="environment-files"><summary>Environment files ({selected.envFiles.length})</summary>{#each selected.envFiles as file}<div class="env-file-row"><code>{file}</code>{#if allowWrites && file.split('/').at(-2) === selected.name}<button class="small-button" onclick={() => beginEditor('env', selected, file.split('/').at(-1) ?? '')} disabled={!!activeJob}>Edit</button>{/if}</div>{/each}{#if allowWrites}<button class="small-button" onclick={() => beginEditor('env', selected)} disabled={!!activeJob}>Add env file</button>{/if}{#if !selected.envFiles.length}<p>Using the provider's default environment-file behavior.</p>{/if}<p class="resource-hint">Environment values are private and travel over HTTP. Use this editor only on a trusted LAN.</p></details>
    {#if allowWrites}<div class="data-folder-manager"><div><strong>Project data folders</strong><p>New folders are private and confined to data/{selected.name}.</p></div><div><label class="sr-only" for="new-data-directory">Folder path</label><input id="new-data-directory" bind:value={dataDirectory} placeholder="e.g. config/cache" /><button class="small-button" onclick={createDataDirectory} disabled={!dataDirectory.trim() || submitting || !!activeJob}>Create folder</button></div></div>{/if}
    <div class="project-log-controls"><h3>Project logs</h3><div><label class="sr-only" for="log-service">Log service</label><select id="log-service" bind:value={service}><option value="">All services</option>{#each services as name}<option value={name}>{name}</option>{/each}</select><label class="sr-only" for="log-tail">Log lines</label><select id="log-tail" bind:value={tail}>{#each [100, 200, 500, 1000, 2000] as count}<option value={count}>{count} lines</option>{/each}</select><button onclick={readLogs} disabled={logLoading}>{logLoading ? 'Reading…' : showLogs ? 'Refresh logs' : 'Read logs'}</button></div></div>
    {#if logError}<p class="form-error" role="alert">{logError}</p>{/if}{#if showLogs && logs}<pre class="project-log-output">{logs.text || 'No log output is available.'}</pre>{#if logs.truncated}<p class="resource-hint">Showing the most recent 128 KiB of output.</p>{/if}{/if}
  </section>
{/if}
<dialog bind:this={editorDialog} class="compose-editor" aria-labelledby="compose-editor-title" oncancel={event => { if (editorBusy) event.preventDefault(); else { editorOpen = false; editorContent = '' } }}>
  <form onsubmit={event => { event.preventDefault(); void saveEditor() }}>
    <div class="eyebrow">{editorKind === 'env' ? 'PRIVATE ENVIRONMENT FILE' : 'COMPOSE PROJECT FILE'}</div>
    <h2 id="compose-editor-title">{editorKind === 'new-project' ? `New ${engine} project` : editorKind === 'compose' ? `Edit ${editorProject?.name ?? 'Compose'}` : `${editorFilename ? 'Edit' : 'New'} environment file`}</h2>
    {#if editorKind === 'new-project'}<label for="compose-project-name">Project name</label><input id="compose-project-name" bind:value={editorFilename} required maxlength="128" pattern="[A-Za-z0-9][A-Za-z0-9_.\-]*" placeholder="my-service" />{/if}
    {#if editorKind === 'env' && editorNewEnv}<label for="compose-env-name">Environment filename</label><input id="compose-env-name" bind:value={editorFilename} required maxlength="128" pattern="[A-Za-z0-9][A-Za-z0-9_.\-]*\.env" placeholder="app.env" />{/if}
    {#if editorBusy && !editorContent}<p class="resource-hint">Loading file…</p>{/if}
    <label for="compose-file-content">{editorKind === 'env' ? 'Environment values' : 'Compose YAML'}</label><textarea id="compose-file-content" bind:value={editorContent} rows="18" spellcheck="false" required disabled={editorBusy && !editorContent}></textarea>
    {#if editorError}<p class="form-error" role="alert">{editorError}</p>{/if}
    <p class="resource-hint">Host bind mounts are limited to this project's data folder. Devices, privileged mode, host namespaces, and named volumes are rejected. Raw file contents are available to authenticated MCP clients with this access token; environment values travel over HTTP.</p>
    <div class="dialog-actions"><button type="button" onclick={() => { editorOpen = false; editorContent = '' }} disabled={editorBusy}>Cancel</button><button class="primary" type="submit" disabled={editorBusy || !allowWrites}>{editorBusy ? 'Working…' : editorKind === 'new-project' ? 'Create project' : 'Save file'}</button></div>
  </form>
</dialog>
<ConfirmDialog open={pending !== null} title={pending ? `${actionLabels[pending.action]}: ${pending.project.name}` : ''} description={confirmation()} label={pending ? actionLabels[pending.action] : 'Confirm'} busy={submitting} onConfirm={() => { if (pending) void perform(pending.project, pending.action) }} onClose={() => { pending = null }} />
