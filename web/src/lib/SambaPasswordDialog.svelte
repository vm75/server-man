<script lang="ts">
  import { onMount } from 'svelte'
  import { startJob } from './api'

  let { username, onClose, onStarted }: { username: string; onClose: () => void; onStarted: (id: string) => void } = $props()
  let dialog: HTMLDialogElement
  onMount(() => { dialog.showModal() })
  let password = $state('')
  let busy = $state(false)
  let error = $state('')

  async function save(event: SubmitEvent) {
    event.preventDefault()
    if (busy) return
    if (!password.trim() || password.length > 256 || /[\r\n\u0000]/.test(password)) {
      error = 'Enter a non-empty password of at most 256 characters without line breaks.'
      return
    }
    busy = true; error = ''
    const request = startJob('shares_samba_password', { password, confirm: true })
    password = ''
    try { const job = await request; onStarted(job.id) }
    catch (e) { error = e instanceof Error ? e.message : 'Could not set Samba password' }
    finally { busy = false }
  }
</script>

<dialog bind:this={dialog} class="panel password-dialog" aria-labelledby="samba-password-title" oncancel={(event) => { event.preventDefault(); if (!busy) onClose() }}>
    <h2 id="samba-password-title">Set Samba password</h2>
    <p>Set or replace the Samba password for <strong>{username}</strong>. Use this username and password to connect to authenticated Samba shares. Your Linux login password stays unchanged.</p>
    <form onsubmit={save}>
      <label>New Samba password
        <input type="password" autocomplete="new-password" required maxlength="256" bind:value={password} disabled={busy} />
      </label>
      {#if error}<p class="notice error" role="alert">{error}</p>{/if}
      <div class="password-actions">
        <button type="button" disabled={busy} onclick={onClose}>Cancel</button>
        <button class="primary" type="submit" disabled={busy || !password.trim()}>{busy ? 'Setting…' : 'Set password'}</button>
      </div>
    </form>
</dialog>

<style>
  .password-dialog::backdrop { background: var(--backdrop-bg, rgba(5, 9, 15, 0.75)); }
  .password-dialog { color: var(--text); margin: auto; width: min(480px, calc(100% - 32px)); max-height: calc(100vh - 32px); overflow-y: auto; }
  .password-dialog p { margin: 12px 0; }
  .password-actions { display: flex; justify-content: flex-end; gap: 12px; margin-top: 16px; }
</style>
