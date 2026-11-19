<script lang="ts">
  import { onMount } from 'svelte'
  import { invokeTool, startJob } from './api'
  import type { ResourceKind, ModelSnapshot, ServiceDefinition, ContainerDefinition, ShareDefinition, StorageDefinition, BindingDefinition, ImpactPlan } from './managed'
  let {
    kind,
    resourceID = '',
    initialKind,
    initialEngine,
    initialBackend,
    onClose,
    onSaved
  }: {
    kind: ResourceKind
    resourceID?: string
    initialKind?: 'container' | 'vm'
    initialEngine?: 'podman' | 'docker'
    initialBackend?: 'quadlet' | 'compose' | 'libvirt' | 'incus' | 'firecracker'
    onClose: () => void
    onSaved: (job: string) => void
  } = $props()
  let snapshot = $state<ModelSnapshot | null>(null)
  let error = $state('')
  let busy = $state(false)
  let id = $state('')

  function createInitialService(): ServiceDefinition {
    const k = initialKind ?? 'container'
    const b = initialBackend ?? (k === 'vm' ? 'libvirt' : 'quadlet')
    const e = initialEngine ?? (k === 'container' ? 'podman' : undefined)
    if (k === 'vm') {
      return { kind: 'vm', backend: b, enabled: false, name: '', description: '', vm: { cpus: 2, memoryMiB: b === 'firecracker' ? 512 : 2048, network: '', disks: [], ...(b === 'firecracker' ? { firecracker: { kernel: '' } } : {}) } }
    }
    return { kind: 'container', engine: e, backend: b, enabled: false, name: '', description: '', containers: { app: { image: '', mounts: [] } } }
  }

  let service = $state<ServiceDefinition>(createInitialService())
  let storage = $state<StorageDefinition>({ path: '', kind: 'directory', external: false })
  let share = $state<ShareDefinition>({ protocol: 'samba', storage: '', enabled: false, readOnly: true, browseable: true })
  let binding = $state<BindingDefinition>({ kind: 'network', name: '' })
  let privatePath = $state('')
  let clients = $state('')
  let dependencyText = $state('')
  let selectedContainer = $state('app')
  let newContainer = $state('')
  let commandText = $state('')
  let portText = $state('')
  let envText = $state('')
  let privateRefs = $state('')
  let advancedText = $state('{}')
  const advancedKeys = ['entrypoint', 'workingDir', 'uidMaps', 'gidMaps', 'groupAdd', 'dependsOn', 'devices', 'capAdd', 'capDrop', 'securityOpt', 'init', 'tty', 'tmpfs', 'shmSize', 'stopGracePeriod', 'pullPolicy', 'logDriver', 'logOptions', 'healthcheck'] as const
  let vmDisks = $state('')
  let plan = $state<ImpactPlan | null>(null)
  let reviewDefinition = $state('')
  let operation = $state('save')
  let mode = $state('now')
  const firecrackerEditNotice = 'Firecracker requires a prepared Linux kernel, raw disk images and an existing Linux bridge. Guest networking and application startup must be configured inside the VM. The native launcher is experimental and requires verified host KVM, jailer, systemd and privileged-helper setup; native boot and crash-recovery acceptance is pending.'
  const incusEditNotice = 'Incus v1: Stop the VM before changing CPU, memory, storage, network, firmware or native description. Save for next start does not defer running hardware edits. Start at boot can be changed without stopping the VM.'
  const storageChoices = $derived(Object.entries(snapshot?.config.storage ?? {}))
  const directoryStorageChoices = $derived(storageChoices.filter(([, s]) => s.kind !== 'file'))
  let folderSource = $state<'storage' | 'manual'>('storage')
  let manualPath = $state('')
  type BrowseTarget = 'share' | { mountIndex: number } | null
  let browseTarget = $state<BrowseTarget>(null)
  let browsePath = $state('/')
  let browseEntries = $state<string[]>([])
  let browseFiles = $state<string[]>([])
  let browseError = $state('')
  let browseLoading = $state(false)
  async function loadBrowse(dir: string) {
    browseLoading = true; browseError = ''
    try {
      const res = await invokeTool<{ path: string; parent: string; directories: string[]; files?: string[] }>('managed_directory_browse', { path: dir })
      browsePath = res.path; browseEntries = res.directories; browseFiles = res.files ?? []
    } catch (e) { browseError = e instanceof Error ? e.message : 'Could not read directory' }
    finally { browseLoading = false }
  }
  function openBrowser(target: BrowseTarget = 'share', initialPath?: string) {
    browseTarget = target
    const initial = initialPath?.trim().startsWith('/') ? initialPath.trim() : target === 'share' && manualPath.trim().startsWith('/') ? manualPath.trim() : '/'
    void loadBrowse(initial)
  }
  function openMountBrowser(index: number) {
    const m = container?.mounts?.[index]
    openBrowser({ mountIndex: index }, m?.path)
  }
  function getSelectorValue(m: any): string {
    if (m.storage && !m.path) return `storage:${m.storage}`
    if (m.path) return m.path
    return ''
  }
  function handleSelectorChange(m: any, index: number, value: string) {
    if (value === '__browse__') {
      openMountBrowser(index)
      return
    }
    if (value.startsWith('storage:')) {
      m.storage = value.slice(8)
      m.path = ''
      m.kind = 'directory'
      return
    }
    if (value) {
      m.path = value
      m.storage = ''
      m.kind = 'directory'
      return
    }
    m.path = ''
    m.storage = ''
  }
  function selectCurrentFolder() {
    if (browseTarget === 'share') {
      manualPath = browsePath
    } else if (browseTarget && typeof browseTarget === 'object' && 'mountIndex' in browseTarget) {
      const m = container?.mounts?.[browseTarget.mountIndex]
      if (m) {
        m.path = browsePath
        m.storage = ''
        m.kind = 'directory'
      }
    }
    browseTarget = null
  }
  function selectFile(filename: string) {
    const fullPath = browsePath === '/' ? `/${filename}` : `${browsePath}/${filename}`
    if (browseTarget && typeof browseTarget === 'object' && 'mountIndex' in browseTarget) {
      const m = container?.mounts?.[browseTarget.mountIndex]
      if (m) {
        m.path = fullPath
        m.storage = ''
        m.kind = 'file'
        m.source = 'manual'
      }
    } else if (browseTarget === 'share') {
      manualPath = fullPath
    }
    browseTarget = null
  }
  function closeBrowser() {
    browseTarget = null
  }
  function selectDirectory(name: string) {
    const next = browsePath === '/' ? `/${name}` : `${browsePath}/${name}`
    void loadBrowse(next)
  }
  function browseUp() {
    const idx = browsePath.lastIndexOf('/')
    const next = idx <= 0 ? '/' : browsePath.slice(0, idx)
    void loadBrowse(next)
  }
  const bindingChoices = $derived(Object.entries(snapshot?.config.bindings ?? {}))
  const container = $derived(service.containers?.[selectedContainer])
  const splitList = (text: string) => text.split(/[\s,]+/).filter(Boolean)
  function selectContainer(name: string) {
    selectedContainer = name
    const c = service.containers?.[name]
    commandText = (c?.command ?? []).map(a => JSON.stringify(a)).join('\n'); portText = (c?.ports ?? []).join('\n'); envText = Object.entries(c?.environment ?? {}).map(([k, v]) => `${k}=${JSON.stringify(v)}`).join('\n'); privateRefs = (c?.envFiles ?? []).join(', ')
    const opts = c as unknown as Record<string, unknown> | undefined
    advancedText = JSON.stringify(Object.fromEntries(advancedKeys.filter(k => opts?.[k] !== undefined).map(k => [k, opts?.[k]])), null, 2)
  }
  function saveContainerInputs() {
    const c = service.containers?.[selectedContainer]; if (!c) return
    c.command = commandText ? commandText.split('\n').map(a => a.startsWith('"') ? JSON.parse(a) as string : a) : []; c.ports = splitList(portText); c.envFiles = splitList(privateRefs)
    const env: Record<string, string> = Object.create(null); for (const line of envText.split('\n').filter(Boolean)) { const at = line.indexOf('='); if (at < 1) throw new Error('Environment entries need NAME=value.'); const value = line.slice(at + 1); env[line.slice(0, at)] = value.startsWith('"') ? JSON.parse(value) as string : value }; c.environment = env
    const advanced: unknown = JSON.parse(advancedText || '{}')
    if (!advanced || typeof advanced !== 'object' || Array.isArray(advanced)) throw new Error('Advanced container settings must be a JSON object.')
    const values = advanced as Record<string, unknown>
    for (const key of Object.keys(values)) if (!(advancedKeys as readonly string[]).includes(key)) throw new Error(`Unsupported advanced setting: ${key}`)
    const opts = c as unknown as Record<string, unknown>
    for (const key of advancedKeys) delete opts[key]
    Object.assign(opts, values)
  }
  function switchContainer(name: string) { try { saveContainerInputs(); selectContainer(name); error = '' } catch (e) { error = e instanceof Error ? e.message : 'Invalid container fields' } }
  function addContainer() {
    if (!/^[a-z][a-z0-9_-]{0,62}$/.test(newContainer) || Object.prototype.hasOwnProperty.call(service.containers ?? {},newContainer)) { error = 'Choose a unique container ID.'; return }
    try { saveContainerInputs(); service.containers ??= {}; service.containers[newContainer] = { image: '', mounts: [] }; selectContainer(newContainer); newContainer = ''; error = '' } catch(e) { error = e instanceof Error ? e.message : 'Invalid container fields' }
  }
  function setKind(value: string) {
    service = value === 'vm' ? { kind: 'vm', backend: 'libvirt', enabled: false, name: service.name, description: service.description, vm: { cpus: 2, memoryMiB: 2048, network: '', disks: [] } } : { kind: 'container', engine: 'podman', backend: 'quadlet', enabled: false, name: service.name, description: service.description, containers: { app: { image: '', mounts: [] } } }
    selectContainer('app')
  }
  function payload() {
    if (kind === 'service') {
      saveContainerInputs()
      if (service.containers) {
        for (const c of Object.values(service.containers)) {
          if (c.mounts) {
            for (const m of c.mounts) {
              if (m.source === 'selector' && m.storage && !m.path) {
                m.path = ''
                delete m.kind
              } else {
                m.path = (m.path ?? '').trim()
                if (!m.kind) {
                  m.kind = 'directory'
                }
              }
              delete m.source
            }
          }
        }
      }
      service.dependsOn = splitList(dependencyText)
      if (service.vm) service.vm.disks = splitList(vmDisks)
      return service
    }
    if (kind === 'storage') return storage
    if (kind === 'share') {
      const result = { ...share }
      if (folderSource === 'manual' || !result.storage) {
        result.path = manualPath.trim()
      }
      if (share.protocol === 'samba') {
        delete result.users
        delete result.clients
        delete result.rootSquash
        delete result.security
      } else {
        result.clients = splitList(clients)
        result.rootSquash ??= true
        result.security ||= 'sys'
        delete result.users
        delete result.guest
        delete result.browseable
      }
      return result
    }
    if (kind === 'binding') return binding
    return privatePath
  }
  async function review(remove = false) {
    busy = true; error = ''
    try { operation = remove ? 'remove' : 'save'; reviewDefinition = remove ? '' : JSON.stringify(payload()); plan = await invokeTool<ImpactPlan>('managed_resource_plan', { kind, id, operation, definition: reviewDefinition, revision: snapshot?.revision }) }
    catch (e) { error = e instanceof Error ? e.message : 'Could not validate the change' }
    finally { busy = false }
  }
  async function save() {
    busy = true; error = ''
    try { const job = await startJob('managed_resource_save', { kind, id, operation, definition: reviewDefinition, revision: snapshot?.revision, mode, confirm: true }); onSaved(job.id) }
    catch (e) { error = e instanceof Error ? e.message : 'Could not save the definition' }
    finally { busy = false }
  }
  onMount(() => { id = resourceID; let stopped = false; const controller = new AbortController(); void (async () => { try { const result = await invokeTool<ModelSnapshot>('managed_config_model', {}, controller.signal); if (stopped) return;
    const definitions = kind === 'service' ? result.config.services : kind === 'storage' ? result.config.storage : kind === 'share' ? result.config.shares : kind === 'binding' ? result.config.bindings : result.config.secrets
    if (resourceID && !Object.prototype.hasOwnProperty.call(definitions ?? {},resourceID)) throw new Error('The definition no longer exists. Refresh before editing.')
    snapshot = result
    if (resourceID) {
      if (kind === 'service') {
        service = structuredClone(result.config.services?.[resourceID] ?? service)
        if (service.containers && result.config.storage) {
          for (const c of Object.values(service.containers)) {
            for (const m of c.mounts ?? []) {
              if (m.path) {
                m.source = 'manual'
                m.kind = m.kind || (result.config.storage[m.storage]?.kind === 'file' ? 'file' : 'directory')
              } else if (m.storage && result.config.storage[m.storage]) {
                m.source = 'selector'
                const st = result.config.storage[m.storage]
                m.path = st.path
                m.kind = st.kind === 'file' ? 'file' : 'directory'
              } else {
                m.source = 'selector'
                m.kind = 'directory'
              }
            }
          }
        }
        dependencyText = (service.dependsOn ?? []).join(', ')
        vmDisks = (service.vm?.disks ?? []).join(', ')
        selectContainer(Object.keys(service.containers ?? {})[0] ?? 'app')
      } else if (kind === 'storage') storage = structuredClone(result.config.storage?.[resourceID] ?? storage)
      else if (kind === 'share') { share = structuredClone(result.config.shares?.[resourceID] ?? share); clients = (share.clients ?? []).join(', '); if (share.storage && result.config.storage?.[share.storage]) manualPath = result.config.storage[share.storage]?.path ?? ''; else if (share.path) manualPath = share.path }
      else if (kind === 'binding') binding = structuredClone(result.config.bindings?.[resourceID] ?? binding)
      else privatePath = result.config.secrets?.[resourceID] ?? ''
    }
    if (kind === 'share') {
      const dirChoices = Object.entries(result.config.storage ?? {}).filter(([, s]) => s.kind !== 'file')
      if (dirChoices.length === 0 || (resourceID && !share.storage && manualPath)) folderSource = 'manual'
    }
  } catch (e) { if (!stopped) error = e instanceof Error ? e.message : 'Could not load configuration' } })(); return () => { stopped = true; controller.abort() } })
  const serviceTypeTitle = $derived.by(() => {
    if (kind !== 'service') return kind === 'secret' ? 'private file reference' : kind
    if (service.kind === 'vm') {
      return service.backend === 'incus' ? 'Incus virtual machine' : service.backend === 'firecracker' ? 'Firecracker microVM' : 'KVM/libvirt virtual machine'
    }
    if (service.engine === 'docker') return 'Docker Compose application'
    if (service.backend === 'compose') return 'Podman Compose application'
    return 'Podman Quadlet application'
  })
</script>

<section class="panel definition-editor" aria-label="Definition editor">
  <header><h2>{resourceID ? `Edit ${kind === 'service' ? 'application' : kind}: ${resourceID}` : `Define ${serviceTypeTitle}`}</h2><button onclick={onClose} disabled={busy}>Close editor</button></header>
  {#if error}<p class="form-error" role="alert">{error}</p>{/if}
  {#if !snapshot}{#if !error}<p role="status">Loading private definition…</p>{/if}
  {:else if plan}
    <h3>Review changes</h3><p>{operation === 'remove' ? 'Remove the definition. Persistent data is retained where supported; unsafe removal is refused.' : 'Save this definition and update affected native configuration. Existing running workloads may need recreation or shutdown/start.'}</p>
    {#if kind === 'service' && service.kind === 'vm' && service.backend === 'incus' && operation !== 'remove'}<p>{incusEditNotice}</p>{/if}
    {#if kind === 'service' && service.kind === 'vm' && service.backend === 'firecracker' && operation !== 'remove'}<p>{firecrackerEditNotice}</p>{/if}
    {#if plan.changes.length}<ul>{#each plan.changes as change}<li>{change.operation}: {change.resource} ({change.backend})</li>{/each}</ul>{:else}<p>No native definition changes. Reference settings will still be saved.</p>{/if}
    {#if plan.files.issues.length}<ul>{#each plan.files.issues as issue}<li><strong>{issue.resource}: {issue.message}</strong> {issue.remedy}</li>{/each}</ul>{/if}
    <label>Apply changes <select aria-label="Apply changes" bind:value={mode}><option value="now">Apply now</option><option value="next_start">Save for next start</option></select></label>
    <div class="actions"><button disabled={busy} onclick={() => { plan = null }}>Back to editing</button><button disabled={busy} onclick={save}>{busy ? 'Starting save…' : 'Confirm and save'}</button></div>
  {:else}
    {#snippet directoryBrowser(isMount: boolean)}
      <div class="directory-browser" role="region" aria-label="Directory browser">
        <header class="browser-header">
          <strong class="mono">{browsePath}</strong>
          <div class="browser-actions">
            <button type="button" class="small-button" disabled={browsePath === '/'} onclick={browseUp}>Up</button>
            <button type="button" class="small-button primary" onclick={selectCurrentFolder}>Select this folder</button>
            <button type="button" class="small-button" onclick={closeBrowser}>Close</button>
          </div>
        </header>
        {#if browseLoading}
          <p class="browser-status">Reading directory…</p>
        {:else if browseError}
          <p class="browser-status error">{browseError}</p>
        {:else}
          <div class="browser-contents">
            {#if browseEntries.length > 0}
              <div class="browser-section">
                <span class="browser-section-title">Folders</span>
                <ul class="dir-grid">
                  {#each browseEntries as dir}
                    <li>
                      <button type="button" class="dir-button mono" onclick={() => selectDirectory(dir)}>
                        📁 {dir}
                      </button>
                    </li>
                  {/each}
                </ul>
              </div>
            {/if}
            {#if browseFiles.length > 0}
              <div class="browser-section">
                <span class="browser-section-title">Files {isMount ? '(click to mount file)' : ''}</span>
                <ul class="dir-grid">
                  {#each browseFiles as file}
                    <li>
                      <button type="button" class="file-button mono" onclick={() => selectFile(file)}>
                        📄 {file}
                      </button>
                    </li>
                  {/each}
                </ul>
              </div>
            {/if}
            {#if browseEntries.length === 0 && browseFiles.length === 0}
              <p class="browser-status">Empty directory.</p>
            {/if}
          </div>
        {/if}
      </div>
    {/snippet}

    <form onsubmit={(e) => { e.preventDefault(); void review() }}>
      <label>Resource ID <input required maxlength="63" bind:value={id} disabled={!!resourceID} /></label>
      {#if kind === 'service'}
        <label>Display name <input bind:value={service.name} maxlength="128" /></label><label>Description <textarea bind:value={service.description}></textarea></label>
        <label>Application type <select aria-label="Application type" value={service.kind} disabled={!!resourceID} onchange={(e) => setKind(e.currentTarget.value)}><option value="container">Container application</option><option value="vm">Virtual machine</option></select></label>
        <label><input type="checkbox" bind:checked={service.enabled} /> Start at boot (does not start now)</label>
        <label>Required application IDs <input bind:value={dependencyText} placeholder="database, cache" /></label>
        {#if service.kind === 'container'}
          <label>Container engine <select aria-label="Container engine" bind:value={service.engine} disabled={!!resourceID} onchange={(e) => { service.backend = e.currentTarget.value === 'docker' ? 'compose' : 'quadlet' }}><option value="podman">Podman</option><option value="docker">Docker</option></select></label>
          <label>Container backend <select aria-label="Container backend" bind:value={service.backend} disabled={!!resourceID || service.engine === 'docker'}><option value="quadlet" disabled={service.engine === 'docker'}>Quadlet (default for Podman)</option><option value="compose">Compose</option></select></label>
          <fieldset><legend>Containers</legend><label>Selected container <select aria-label="Selected container" value={selectedContainer} onchange={(e) => switchContainer(e.currentTarget.value)}>{#each Object.keys(service.containers ?? {}) as name}<option value={name}>{name}</option>{/each}</select></label>
            <div class="actions"><label>New container ID <input bind:value={newContainer} /></label><button type="button" onclick={addContainer}>Add container</button><button type="button" disabled={Object.keys(service.containers ?? {}).length <= 1} onclick={() => { if (!service.containers) return; delete service.containers[selectedContainer]; selectContainer(Object.keys(service.containers)[0] ?? 'app') }}>Remove selected container</button></div>
            {#if container}
              <label>Container image <input required bind:value={container.image} placeholder="docker.io/library/nginx:stable" /></label>
               <label>Native container name (optional) <input bind:value={container.containerName} placeholder="Leave blank for a generated managed name" /></label>
              <label>Command arguments (one per line) <textarea bind:value={commandText}></textarea></label><label>Published ports (one per line) <textarea bind:value={portText} placeholder="8080:80"></textarea></label>
              <label>Non-secret environment (NAME=value per line) <textarea bind:value={envText}></textarea></label><label>Private environment-file reference IDs <input bind:value={privateRefs} /></label>
              <fieldset><legend>Rootless identity, networking and recovery</legend>
                 <label>Container user (UID[:GID] or name) <input bind:value={container.user} placeholder="0:0" /></label>
                 <label>User namespace <input bind:value={container.usernsMode} placeholder="keep-id:uid=1000,gid=1000" /></label>
                 <label>Network mode <input bind:value={container.networkMode} placeholder="blank, host, none or service:vpn" /></label>
                 <label>Restart recovery (Quadlet only) <select bind:value={container.restart} disabled={service.backend === 'compose'}><option value="">Default</option><option value="no">Never</option><option value="on-failure">On failure</option><option value="always">Always</option><option value="unless-stopped">Unless stopped</option></select></label>
                 {#if service.backend === 'compose'}<p>Compose restart remains fixed to no to keep boot enablement independent. Remove any previous explicit restart value before switching to Compose.</p>{/if}
                 <label><input type="checkbox" bind:checked={container.privileged} /> Privileged (high risk; host permissions still apply)</label>
                 <label><input type="checkbox" bind:checked={container.readOnly} /> Read-only container root filesystem</label>
                 <p>Advanced options use common JSON fields for UID/GID maps, groups, devices, capabilities, healthchecks, dependencies, logging, tmpfs and more. Validation rejects unsupported options.</p>
                 <label>Advanced container options (JSON) <textarea bind:value={advancedText} rows="10" spellcheck="false" placeholder="Optional JSON object with typed advanced container settings"></textarea></label>
               </fieldset>
               <label>CPU limit (0 for default) <input type="number" min="0" max="1024" bind:value={container.cpus} /></label><label>Memory limit in MiB (0 for default) <input type="number" min="0" bind:value={container.memoryMiB} /></label>
              <fieldset><legend>Data mounts</legend>
                {#each container.mounts ?? [] as mount, index}
                  <div class="mount">
                    <div class="storage-choice-toggle">
                      <label class="toggle-option">
                        <input type="radio" name={`mount-source-${index}`} value="selector" bind:group={mount.source} />
                        <span>Select folder using a selector</span>
                      </label>
                      <label class="toggle-option">
                        <input type="radio" name={`mount-source-${index}`} value="manual" bind:group={mount.source} />
                        <span>Specify path in UI</span>
                      </label>
                    </div>

                    {#if mount.source === 'selector'}
                      <div class="mount-row">
                        <label class="mount-field-grow">Folder selector
                          <div class="input-with-button">
                            <select aria-label="Select folder" value={getSelectorValue(mount)} onchange={(e) => handleSelectorChange(mount, index, e.currentTarget.value)}>
                              <option value="">Choose a folder…</option>
                              {#if storageChoices.length > 0}
                                <optgroup label="Configured storage">
                                  {#each storageChoices as [sID, st]}
                                    <option value={`storage:${sID}`}>{sID} ({st.kind}: {st.path})</option>
                                  {/each}
                                </optgroup>
                              {/if}
                              <optgroup label="Common host folders">
                                <option value="/srv">/srv</option>
                                <option value="/var">/var</option>
                                <option value="/var/log">/var/log</option>
                                <option value="/opt">/opt</option>
                                <option value="/etc">/etc</option>
                                <option value="/home">/home</option>
                                <option value="/tmp">/tmp</option>
                              </optgroup>
                              {#if mount.path && !['/srv', '/var', '/var/log', '/opt', '/etc', '/home', '/tmp'].includes(mount.path) && !mount.storage}
                                <optgroup label="Selected folder">
                                  <option value={mount.path}>📁 {mount.path}</option>
                                </optgroup>
                              {/if}
                              <option value="__browse__">📁 Browse to select any folder…</option>
                            </select>
                            <button type="button" class="browse-button" onclick={() => openMountBrowser(index)}>Browse any folder…</button>
                          </div>
                        </label>
                      </div>
                    {:else}
                      <div class="mount-row">
                        <label>Host type
                          <select aria-label="Host type" bind:value={mount.kind}>
                            <option value="directory">Directory</option>
                            <option value="file">File</option>
                          </select>
                        </label>
                        <label class="mount-field-grow">Host {mount.kind === 'file' ? 'file' : 'directory'} path
                          <div class="input-with-button">
                            <input required aria-label="Host path" bind:value={mount.path} placeholder={mount.kind === 'file' ? '/etc/localtime or config/app.conf' : '/srv/data or data/app'} />
                            <button type="button" class="browse-button" onclick={() => openMountBrowser(index)}>Browse…</button>
                          </div>
                        </label>
                      </div>
                    {/if}

                    {#if browseTarget && typeof browseTarget === 'object' && browseTarget.mountIndex === index}
                      {@render directoryBrowser(true)}
                    {/if}

                    <div class="mount-row">
                      <label class="mount-field-grow">Container target path
                        <input required aria-label="Container path" bind:value={mount.target} placeholder={mount.kind === 'file' ? '/etc/config.json' : '/data'} />
                      </label>
                      <label class="checkbox-inline">
                        <input type="checkbox" bind:checked={mount.readOnly} /> Read only
                      </label>
                       <label>SELinux relabel <select bind:value={mount.selinux}><option value="">None</option><option value="private">Private :Z</option><option value="shared">Shared :z</option></select></label>
                       <label class="checkbox-inline"><input type="checkbox" bind:checked={mount.chown} /> :U recursively changes host ownership</label>
                      <button type="button" class="danger-button small" onclick={() => { container.mounts = container.mounts?.filter((_, i) => i !== index) }}>Remove mount</button>
                    </div>
                  </div>
                {/each}
                <button type="button" onclick={() => {
                  container.mounts ??= []
                  container.mounts.push({
                    storage: '',
                    path: '',
                    kind: 'directory',
                    source: 'selector',
                    target: '/data',
                    readOnly: false
                  })
                }}>Add data mount</button>
              </fieldset>
            {/if}
          </fieldset>
        {:else if service.vm}
          <label>VM backend <select aria-label="VM backend" bind:value={service.backend} disabled={!!resourceID} onchange={() => { if (!service.vm) return; if (service.backend === 'firecracker') { service.vm.firecracker ??= { kernel: '' }; service.vm.memoryMiB = Math.min(service.vm.memoryMiB, 512) } else { delete service.vm.firecracker } }}><option value="libvirt">KVM/libvirt</option><option value="incus">Incus</option><option value="firecracker">Firecracker microVM</option></select></label>
          {#if service.backend === 'incus'}<p>{incusEditNotice}</p>{/if}
          {#if service.backend === 'firecracker'}<p class="resource-hint">{firecrackerEditNotice}</p>{/if}
          <label>Virtual CPUs <input required type="number" min="1" max={service.backend === "firecracker" ? 32 : 1024} bind:value={service.vm.cpus} /></label><label>Memory in MiB <input required type="number" min="128" max={service.backend === "firecracker" ? 262144 : undefined} bind:value={service.vm.memoryMiB} /></label>
          <label>Network binding <select aria-label="Network binding" required bind:value={service.vm.network}><option value="">Choose network</option>{#each bindingChoices.filter(([,b]) => service.backend === 'firecracker' ? b.kind === 'bridge' : b.kind === 'network' || b.kind === 'bridge') as [bindingID,b]}<option value={bindingID}>{bindingID} ({b.name})</option>{/each}</select></label>
          {#if service.backend === 'incus'}<label>Local image alias or fingerprint <input bind:value={service.vm.image} disabled={!!resourceID} /></label><label>Root disk GiB <input type="number" min="1" bind:value={service.vm.rootSizeGiB} placeholder="20" /></label>
            {#each ['pool', 'project'] as bindingType}<label>{bindingType === 'pool' ? 'Storage pool binding' : 'Project binding'} <select aria-label={bindingType === 'pool' ? 'Storage pool binding' : 'Project binding'} required value={bindingType === 'pool' ? service.vm.pool : service.vm.project} onchange={(e) => { if (!service.vm) return; if (bindingType === 'pool') service.vm.pool = e.currentTarget.value; else service.vm.project = e.currentTarget.value }}><option value="">Choose binding</option>{#each bindingChoices.filter(([,b]) => b.kind === bindingType) as [bindingID,b]}<option value={bindingID}>{bindingID} ({b.name})</option>{/each}</select></label>{/each}
          {:else if service.backend === 'firecracker'}
            {#if service.vm.firecracker}
              <label>Guest kernel storage <select aria-label="Guest Linux kernel storage" required bind:value={service.vm.firecracker.kernel}><option value="">Choose kernel file</option>{#each storageChoices.filter(([,st]) => st.kind === 'file' && !st.format) as [storageID, st]}<option value={storageID}>{storageID} ({st.path})</option>{/each}</select></label>
              <label>Kernel boot arguments (optional) <input bind:value={service.vm.firecracker.bootArgs} maxlength="2048" placeholder="console=ttyS0 reboot=k panic=1 root=/dev/vda rw" /></label>
            {/if}
            <label>Existing raw disk IDs (root first) <input required bind:value={vmDisks} placeholder="microvm-root" /></label>
            <p class="resource-hint">Select prepared raw file storage, not QCOW2. The first disk is the guest root at /dev/vda; other disk IDs follow. The kernel must be separate file storage without a disk format.</p>
          {:else}<label>Existing disk storage IDs <input bind:value={vmDisks} placeholder="vm-root" /></label><p>Choose existing file storage with an explicit raw or qcow2 format. Defining a guest never replaces its disk.</p>{/if}
        {/if}
      {:else if kind === 'storage'}
        <label>Data path <input required bind:value={storage.path} placeholder="data/documents" /></label><label><input type="checkbox" bind:checked={storage.external} /> External absolute path (host requirement)</label><label>Data type <select aria-label="Data type" bind:value={storage.kind} onchange={(e) => { if (e.currentTarget.value !== 'file') { storage.source = undefined; storage.format = '' } }}><option value="directory">Directory</option><option value="file">File / VM disk</option></select></label>
        {#if storage.kind === 'file'}<label>Disk format <select aria-label="Disk format" bind:value={storage.format}><option value="">Not a VM disk</option><option value="qcow2">qcow2</option><option value="raw">raw</option></select></label>{/if}
        {#if storage.kind === 'file'}<label><input type="checkbox" checked={!!storage.source} onchange={(e) => { storage.source = e.currentTarget.checked ? {path:'assets/',format:'qcow2',sha256:''} : undefined }} /> Provision a new disk from a local image</label>
          {#if storage.source}<label>Initial image asset path<input aria-label="Initial image asset path" required bind:value={storage.source.path} placeholder="assets/debian.qcow2" /></label><label>Initial image format<select aria-label="Initial image format" bind:value={storage.source.format}><option value="qcow2">qcow2</option><option value="raw">raw</option></select></label><label>Initial image SHA-256<input aria-label="Initial image SHA-256" required pattern={'[0-9a-f]{64}'} bind:value={storage.source.sha256} /></label><p>Use a verified standalone local image under assets/. This source is used only by explicit new-disk provisioning; saving or starting never replaces guest data.</p>{/if}
        {/if}
        <label>Expected filesystem source (optional) <input bind:value={storage.mount} placeholder="/dev/mapper/data" /></label><p>This adds a reference only. Missing data stays blocked; it is never replaced with an empty folder.</p>
      {:else if kind === 'share'}
        <label>Share protocol <select aria-label="Share protocol" bind:value={share.protocol} disabled={!!resourceID}><option value="samba">Samba</option><option value="nfs">NFS</option></select></label>
        {#if directoryStorageChoices.length > 0}
          <div class="storage-choice-toggle">
            <label class="toggle-option">
              <input type="radio" name="folderSource" value="storage" bind:group={folderSource} />
              <span>Use configured storage reference</span>
            </label>
            <label class="toggle-option">
              <input type="radio" name="folderSource" value="manual" bind:group={folderSource} />
              <span>Specify directory path manually</span>
            </label>
          </div>
        {/if}
        {#if folderSource === 'storage' && directoryStorageChoices.length > 0}
          <label>Shared storage <select aria-label="Shared storage" required bind:value={share.storage}><option value="">Choose directory storage</option>{#each directoryStorageChoices as [storageID]}<option value={storageID}>{storageID}</option>{/each}</select></label>
        {:else}
          <div class="manual-path-box">
            <label>Directory path to share
              <div class="input-with-button">
                <input aria-label="Directory path to share" required bind:value={manualPath} placeholder="/srv/shares/documents or data/documents" />
                <button type="button" class="browse-button" onclick={() => openBrowser('share', manualPath)}>Browse…</button>
              </div>
            </label>
            {#if directoryStorageChoices.length === 0}
              <p class="field-hint">No directory storage references configured yet. Enter an absolute or relative directory path.</p>
            {/if}
          </div>
        {/if}
        {#if browseTarget === 'share'}
          {@render directoryBrowser(false)}
        {/if}
        <label><input type="checkbox" bind:checked={share.enabled} /> Publish this share</label><label><input type="checkbox" bind:checked={share.readOnly} /> Read only</label>
        {#if share.protocol === 'samba'}<label><input type="checkbox" bind:checked={share.guest} /> Guest access (requires host setup)</label><label><input type="checkbox" bind:checked={share.browseable} /> Browseable (discoverable on network)</label><p>Authenticated access uses the Linux account running server-man. Set its Samba password in File shares.</p>
        {:else}<label>Allowed client addresses/CIDRs <textarea required bind:value={clients} placeholder="192.168.1.0/24"></textarea></label><label><input type="checkbox" checked={share.rootSquash ?? true} onchange={(e) => { share.rootSquash = e.currentTarget.checked }} /> Root squashing</label><label>Security <select aria-label="Security" value={share.security || 'sys'} onchange={(e) => { share.security = e.currentTarget.value }}><option value="sys">sys</option><option value="krb5">krb5</option><option value="krb5i">krb5i</option><option value="krb5p">krb5p</option></select></label>{/if}
      {:else if kind === 'binding'}<label>Binding type <select aria-label="Binding type" bind:value={binding.kind}><option value="network">Managed network</option><option value="bridge">Host bridge</option><option value="pool">Incus storage pool</option><option value="project">Incus project</option></select></label><label>Native resource name <input required bind:value={binding.name} /></label>
      {:else}<label>Private file path <input required bind:value={privatePath} placeholder="secrets/application.env" /></label><p>Only the file reference is saved. Secret values are never loaded into this form.</p>{/if}
      <div class="actions"><button type="submit" disabled={busy}>{busy ? 'Validating…' : 'Review change'}</button>{#if resourceID}<button type="button" disabled={busy} onclick={() => review(true)}>Remove definition…</button>{/if}</div>
    </form>
  {/if}
</section>
<style>
  .definition-editor {
    margin-bottom: 24px;
    padding: 24px;
    min-width: 0;
    border: 1px solid var(--border-light);
    box-shadow: var(--shadow-md);
  }
  header, .actions {
    display: flex;
    gap: 12px;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
  }
  header {
    margin-bottom: 20px;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--border-subtle);
  }
  header h2 {
    font-size: 18px;
    font-weight: 650;
  }
  form {
    display: grid;
    gap: 16px;
  }
  label {
    display: grid;
    gap: 6px;
    font-size: 12px;
    font-weight: 600;
    color: var(--text-secondary);
  }
  input, select, textarea {
    max-width: 100%;
    min-width: 0;
    width: 100%;
  }
  input[type="checkbox"] {
    width: auto;
    justify-self: start;
    margin-right: 6px;
  }
  textarea {
    min-height: 5.5rem;
    font-family: var(--mono);
    font-size: 12px;
    line-height: 1.5;
  }
  fieldset {
    min-width: 0;
    display: grid;
    gap: 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 16px 20px;
    background: var(--panel-subtle);
    margin: 8px 0;
  }
  legend {
    font-size: 12px;
    font-weight: 650;
    color: var(--accent);
    padding: 0 8px;
    text-transform: uppercase;
    letter-spacing: 0.8px;
  }
  .mount {
    display: grid;
    gap: 12px;
    padding: 14px;
    background: var(--panel);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
  }
  .mount-row {
    display: flex;
    gap: 12px;
    align-items: flex-end;
    flex-wrap: wrap;
  }
  .mount-field-grow {
    flex: 1;
    min-width: 200px;
  }
  .checkbox-inline {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 8px;
    white-space: nowrap;
    cursor: pointer;
  }
  .checkbox-inline input {
    margin: 0;
  }
  .browser-contents {
    display: grid;
    gap: 12px;
  }
  .browser-section {
    display: grid;
    gap: 6px;
  }
  .browser-section-title {
    font-size: 11px;
    font-weight: 650;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--muted);
  }
  .file-button {
    width: 100%;
    text-align: left;
    padding: 6px 8px;
    font-size: 11.5px;
    background: var(--panel);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-xs);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text);
  }
  .file-button:hover {
    border-color: var(--accent);
    color: var(--accent);
  }
  p, li, h2 {
    overflow-wrap: anywhere;
  }
  ul {
    list-style: none;
    padding: 0;
    margin: 12px 0;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  li {
    padding: 8px 12px;
    background: var(--panel-subtle);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    font-size: 12px;
  }
  .actions {
    margin-top: 16px;
  }
  .storage-choice-toggle {
    display: flex;
    gap: 16px;
    margin: 4px 0 8px;
    flex-wrap: wrap;
  }
  .toggle-option {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    font-weight: 500;
    cursor: pointer;
  }
  .toggle-option input[type="radio"] {
    width: auto;
    margin: 0;
  }
  .manual-path-box {
    display: grid;
    gap: 6px;
  }
  .input-with-button {
    display: flex;
    gap: 8px;
    align-items: stretch;
  }
  .input-with-button input {
    flex: 1;
    min-width: 0;
  }
  .browse-button {
    white-space: nowrap;
    padding: 0 16px;
    font-size: 12px;
    flex-shrink: 0;
  }
  .field-hint {
    font-size: 11px;
    color: var(--muted);
    margin: 2px 0 0;
  }
  .directory-browser {
    margin: 8px 0 12px;
    padding: 12px;
    background: var(--panel-subtle);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
  }
  .browser-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 10px;
    margin-bottom: 10px;
    flex-wrap: wrap;
  }
  .browser-header strong {
    font-size: 12px;
    color: var(--text);
    overflow-wrap: anywhere;
  }
  .browser-actions {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .browser-status {
    font-size: 11px;
    color: var(--muted);
    padding: 6px 0;
  }
  .browser-status.error {
    color: var(--red);
  }
  .dir-grid {
    list-style: none;
    padding: 0;
    margin: 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 6px;
    max-height: 180px;
    overflow-y: auto;
  }
  .dir-button {
    width: 100%;
    text-align: left;
    padding: 6px 8px;
    font-size: 11.5px;
    background: var(--panel);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-xs);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .dir-button:hover {
    border-color: var(--accent);
    color: var(--accent);
  }
</style>
