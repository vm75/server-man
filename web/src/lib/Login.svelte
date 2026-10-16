<script lang="ts">
  import Icon from './Icon.svelte'
  import { request } from './api'

  let { onSignedIn }: { onSignedIn: () => Promise<void> } = $props()
  let token = $state('')
  let busy = $state(false)
  let error = $state('')

  async function signIn(event: SubmitEvent) {
    event.preventDefault()
    busy = true
    error = ''
    try {
      await request('/api/session', { method: 'POST', body: JSON.stringify({ token }) })
      token = ''
      await onSignedIn()
    } catch (e) { error = e instanceof Error ? e.message : 'Sign in failed' }
    finally { busy = false }
  }
</script>

<main id="main" class="login-shell">
  <div class="login-brand"><span class="brand-mark"><Icon name="server" size={25} /></span><span>serverman</span></div>
  <section class="panel login-panel" aria-labelledby="sign-in-heading">
    <div class="eyebrow">YOUR SERVER WORKSPACE</div>
    <h1 id="sign-in-heading">Welcome back.</h1>
    <p>Sign in with your server access token.</p>
    <form onsubmit={signIn}>
      <label for="access-token">Access token</label>
      <input id="access-token" name="token" type="password" bind:value={token} autocomplete="current-password" required minlength="32" maxlength="256" placeholder="Enter your access token" disabled={busy} />
      {#if error}<p class="form-error" role="alert">{error}</p>{/if}
      <button class="primary" type="submit" disabled={busy || !token.trim()}>{busy ? 'Signing in…' : 'Sign in'}<Icon name="arrow" size={17} /></button>
    </form>
    <div class="login-help"><strong>Where is my token?</strong><p>The server creates a private token file on first start. Its path is shown in the server's startup message.</p></div>
  </section>
  <p class="login-caption">A quiet place to manage your Linux server.</p>
</main>
