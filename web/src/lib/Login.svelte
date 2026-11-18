<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from './Icon.svelte'
  import { request } from './api'

  let { onSignedIn }: { onSignedIn: () => Promise<void> } = $props()
  let adminConfigured = $state<boolean | null>(null)
  let tokenSignIn = $state(false)
  let username = $state('')
  let password = $state('')
  let confirmPassword = $state('')
  let token = $state('')
  let busy = $state(false)
  let error = $state('')

  async function loadMode() {
    busy = true
    error = ''
    try {
      const result = await request<{ adminConfigured: boolean }>('/api/auth/mode')
      adminConfigured = result.adminConfigured
    } catch (e) { error = e instanceof Error ? e.message : 'Could not check login options' }
    finally { busy = false }
  }

  async function signIn(event: SubmitEvent) {
    event.preventDefault()
    error = ''
    const setup = adminConfigured === false && !tokenSignIn
    if (setup && password !== confirmPassword) { error = 'Passwords do not match'; return }
    busy = true
    try {
      await request(setup ? '/api/admin/setup' : '/api/session', {
        method: 'POST',
        body: JSON.stringify(setup ? { token, username, password } : adminConfigured ? { username, password } : { token }),
      })
      token = ''
      password = ''
      confirmPassword = ''
      await onSignedIn()
    } catch (e) { error = e instanceof Error ? e.message : 'Sign in failed' }
    finally { busy = false }
  }

  onMount(() => { void loadMode() })
</script>

<main id="main" class="login-shell">
  <div class="login-brand"><span class="brand-mark"><Icon name="server" size={25} /></span><span>server-man</span></div>
  <section class="panel login-panel" aria-labelledby="sign-in-heading">
    <div class="eyebrow">YOUR SERVER WORKSPACE</div>
    <h1 id="sign-in-heading">{adminConfigured === false && !tokenSignIn ? 'Set up administrator.' : 'Welcome back.'}</h1>
    {#if adminConfigured === null}
      <p>Checking authentication…</p>
      {#if error}<p class="form-error" role="alert">{error}</p><button onclick={loadMode} disabled={busy}>Retry</button>{/if}
    {:else}
      <p>{adminConfigured ? 'Sign in with your administrator account.' : tokenSignIn ? 'Sign in with the server access token.' : 'Create your single administrator account using the existing server access token.'}</p>
      <form onsubmit={signIn}>
        {#if adminConfigured || !tokenSignIn}
          <label for="admin-username">Username</label>
          <input id="admin-username" name="username" type="text" bind:value={username} autocomplete="username" required maxlength="64" pattern="[A-Za-z][A-Za-z0-9_.-]*" disabled={busy} placeholder="Administrator username" />
          <label for="admin-password">Password</label>
          <input id="admin-password" name="password" type="password" bind:value={password} autocomplete={adminConfigured ? 'current-password' : 'new-password'} required minlength={adminConfigured ? undefined : 12} maxlength="256" disabled={busy} placeholder="Administrator password" />
          {#if !adminConfigured}
            <label for="confirm-password">Confirm password</label>
            <input id="confirm-password" name="confirm-password" type="password" bind:value={confirmPassword} autocomplete="new-password" required minlength="12" maxlength="256" disabled={busy} placeholder="Repeat password" />
          {/if}
        {/if}
        {#if !adminConfigured}
          <label for="access-token">Access token</label>
          <input id="access-token" name="token" type="password" bind:value={token} autocomplete="off" required minlength="32" maxlength="256" placeholder="Enter your access token" disabled={busy} />
        {/if}
        {#if error}<p class="form-error" role="alert">{error}</p>{/if}
        <button class="primary" type="submit" disabled={busy}>{busy ? 'Please wait…' : adminConfigured || tokenSignIn ? 'Sign in' : 'Create administrator'}<Icon name="arrow" size={17} /></button>
      </form>
      {#if !adminConfigured}
        <div class="login-help">
          <strong>Where is my token?</strong>
          <p>The server creates a private token file on first start. Read it locally from the path configured by <code>server.token_file</code>.</p>
          <button type="button" class="small-button" disabled={busy} onclick={() => { tokenSignIn = !tokenSignIn; error = '' }}>{tokenSignIn ? 'Set up administrator instead' : 'Sign in with token instead'}</button>
        </div>
      {/if}
    {/if}
  </section>
  <p class="login-caption">A quiet place to manage your Linux server.</p>
</main>
