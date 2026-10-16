<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from './lib/Icon.svelte'
  import Login from './lib/Login.svelte'
  import Jobs from './lib/Jobs.svelte'
  import Overview from './pages/Overview.svelte'
  import AgentAccess from './pages/AgentAccess.svelte'
  import { ApiError, request, type Status } from './lib/api'

  const pages = [
    { id: 'overview', name: 'Overview', subtitle: 'Your server, at a glance.' },
    { id: 'containers', name: 'Containers', subtitle: 'Compose projects across Podman and Docker.' },
    { id: 'machines', name: 'Virtual machines', subtitle: 'LXC, LXD, and KVM workloads.' },
    { id: 'services', name: 'Services', subtitle: 'System services and their logs.' },
    { id: 'shares', name: 'File shares', subtitle: 'Samba and NFS exports.' },
    { id: 'updates', name: 'Updates', subtitle: 'Keep your Linux system up to date.' },
    { id: 'agent', name: 'Agent access', subtitle: 'Manage your server through MCP.' },
  ]
  let selected = $state('overview')
  let menuOpen = $state(false)
  let status = $state<Status | null>(null)
  let loading = $state(true)
  let error = $state('')
  let needsAuth = $state(false)
  const page = $derived(pages.find(p => p.id === selected) ?? pages[0])

  async function connect() {
    loading = true
    error = ''
    try { status = await request<Status>('/api/status'); needsAuth = false }
    catch (e) { if (e instanceof ApiError && e.status === 401) { needsAuth = true; status = null } else { error = e instanceof Error ? e.message : 'Could not connect to the server' } }
    finally { loading = false }
  }

  async function signOut() {
    try { await request('/api/session', { method: 'DELETE' }); needsAuth = true; status = null; menuOpen = false }
    catch (e) { error = e instanceof Error ? e.message : 'Could not sign out' }
  }

  onMount(() => {
    const expired = () => { needsAuth = true; status = null; menuOpen = false }
    window.addEventListener('serverman:unauthorized', expired)
    void connect()
    return () => window.removeEventListener('serverman:unauthorized', expired)
  })
</script>

<svelte:head><title>{page.name} · Serverman</title></svelte:head>

<a class="skip-link" href="#main">Skip to content</a>
{#if needsAuth}
  <Login onSignedIn={async () => { needsAuth = false; await connect() }} />
{:else}
<div class="app-shell">
  <aside class:open={menuOpen} aria-label="Main navigation">
    <a class="brand" href="#overview" onclick={() => { selected = 'overview'; menuOpen = false }}>
      <span class="brand-mark"><Icon name="server" size={24} /></span>
      <span>serverman<span class="brand-caption">LINUX CONTROL</span></span>
    </a>
    <div class="nav-label">WORKSPACE</div>
    <nav>
      {#each pages as p}
        <button class:active={selected === p.id} aria-current={selected === p.id ? 'page' : undefined} onclick={() => { selected = p.id; menuOpen = false }}>
          <Icon name={p.id} /><span>{p.name}</span>
        </button>
      {/each}
    </nav>
    <div class="sidebar-footer">
      <span class="connection-dot" class:connected={status && !error}></span>
      <div><strong>{status?.hostname ?? 'Local server'}</strong><span>{loading ? 'Connecting…' : error ? 'Connection failed' : 'Connected'}</span></div>
    </div>
  </aside>
  {#if menuOpen}<button class="nav-backdrop" aria-label="Close navigation" onclick={() => { menuOpen = false }}></button>{/if}
  <div class="workspace">
    <header class="topbar">
      <div class="breadcrumb"><button class="icon-button mobile-menu" aria-label="Toggle navigation" aria-expanded={menuOpen} onclick={() => { menuOpen = !menuOpen }}><Icon name="menu" /></button><span class="muted">Workspace</span><span class="breadcrumb-divider">/</span><span>{page.name}</span></div>
      <div class="topbar-actions">{#if status}<Jobs allowWrites={status.allowWrites} /><button class="small-button sign-out" onclick={signOut}>Sign out</button>{/if}<div class="host-pill"><span class="connection-dot" class:connected={status && !error}></span><span>{status?.hostname ?? 'Connecting'}</span></div></div>
    </header>
    <main id="main">
      <div class="page-heading"><div><div class="eyebrow">SERVER WORKSPACE</div><h1>{page.name}</h1><p>{page.subtitle}</p></div><span class="badge">{status?.allowWrites ? 'Management enabled' : 'Read only'}</span></div>
      {#if error}
        <div class="notice error" role="alert"><div><strong>Could not connect</strong><p>{error}</p></div><button onclick={connect} disabled={loading}>Try again</button></div>
      {:else if loading}
        <section class="panel empty-state" aria-busy="true"><span class="loading-ring"></span><h2>Connecting to your server</h2><p>Reading server status…</p></section>
      {:else if selected === 'overview'}
        <Overview />
      {:else if selected === 'agent' && status}
        <AgentAccess allowWrites={status.allowWrites} />
      {:else}
        <section class="panel empty-state"><span class="module-icon"><Icon name={selected} size={30} /></span><h2>{page.name}</h2><p>This module is next in the implementation plan. Controls will appear here when its backend is ready.</p><button onclick={() => { selected = 'overview' }}>Back to overview</button></section>
      {/if}
      <footer class="page-footer"><span>Serverman<span class="muted"> / </span>{status?.version ?? 'dev'}</span><span>One place for your Linux server</span></footer>
    </main>
  </div>
</div>
{/if}
