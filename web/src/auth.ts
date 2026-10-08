import { account, APIError, login, logout } from './api';

export function setupAuth(container: HTMLElement) {
  container.innerHTML = `
    <div class="account-heading"><strong>Codex</strong><span id="account-status" class="quiet" role="status">正在连接…</span><button id="account-refresh" class="text-button" type="button">刷新状态</button></div>
    <div id="login-options" class="login-options">
      <button id="oauth-login" type="button" class="choose-file">OAuth 登录（ChatGPT）</button>
      <details id="key-details"><summary>使用 API Key</summary><form id="key-form"><label class="sr-only" for="api-key">OpenAI API Key</label><input id="api-key" type="password" placeholder="OpenAI API Key" autocomplete="off" required maxlength="8192" /><button class="choose-file" type="submit">连接</button></form></details>
    </div>
    <p id="oauth-pending" class="message" hidden>请在授权页面完成登录。<a id="oauth-link" target="_blank" rel="noopener noreferrer" hidden>打开授权页面 ↗</a></p>
    <button id="logout-button" class="text-button" type="button" hidden>退出登录 / 取消授权</button>
    <p id="auth-error" class="message error" role="alert" hidden></p>
  `;
  const el = <T extends HTMLElement>(id: string) => container.querySelector<T>(`#${id}`)!;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let epoch = 0;
  let busy = false;
  function errorMessage(error: unknown) {
    const node = el('auth-error');
    node.textContent = error instanceof APIError ? error.message : '无法连接服务，请稍后重试。';
    node.hidden = false;
  }
  function disable(disabled: boolean) {
    busy = disabled;
    container.querySelectorAll<HTMLButtonElement>('button').forEach(button => { button.disabled = disabled; });
  }
  async function refresh() {
    clearTimeout(timer);
    const version = epoch;
    try {
      const info = await account();
      if (version !== epoch) return;
      el('account-status').textContent = info.pending ? '等待授权' : info.loggedIn ? `已连接 · ${info.authType === 'apiKey' ? 'API Key' : 'ChatGPT'}${info.email ? ` · ${info.email}` : ''}` : '未登录';
      el('login-options').hidden = info.loggedIn && !info.pending;
      el('logout-button').hidden = !info.loggedIn && !info.pending;
      el('oauth-pending').hidden = !info.pending;
      if (!info.pending) { el<HTMLAnchorElement>('oauth-link').removeAttribute('href'); el('oauth-link').hidden = true; }
      el('auth-error').hidden = !info.error;
      if (info.error) el('auth-error').textContent = info.error;
      if (info.pending) timer = setTimeout(() => { void refresh(); }, 2000);
    } catch (error) {
      if (version !== epoch) return;
      el('account-status').textContent = '未连接';
      errorMessage(error);
    }
  }
  async function signIn(type: 'chatgpt' | 'apiKey') {
    if (busy) return;
    epoch++;
    clearTimeout(timer);
    disable(true);
    el('auth-error').hidden = true;
    const input = el<HTMLInputElement>('api-key');
    try {
      const key = type === 'apiKey' ? input.value : undefined;
      input.value = '';
      const result = await login(type, key);
      if (result.authUrl) {
        const url = new URL(result.authUrl);
        if (url.protocol !== 'https:' || !['auth.openai.com', 'chatgpt.com', 'auth0.openai.com'].includes(url.hostname)) throw new APIError('invalid_auth_url', '授权地址无效，请检查 Codex 版本。');
        const link = el<HTMLAnchorElement>('oauth-link');
        link.href = url.href;
        link.hidden = false;
        el('oauth-pending').hidden = false;
      }
      el<HTMLDetailsElement>('key-details').open = false;
      await refresh();
    } catch (error) { errorMessage(error); }
    finally { input.value = ''; disable(false); }
  }
  el('oauth-login').addEventListener('click', () => { void signIn('chatgpt'); });
  el('key-form').addEventListener('submit', event => { event.preventDefault(); void signIn('apiKey'); });
  el('account-refresh').addEventListener('click', () => { epoch++; void refresh(); });
  el('logout-button').addEventListener('click', async () => {
    if (busy) return;
    epoch++;
    clearTimeout(timer);
    disable(true);
    try { await logout(); await refresh(); } catch (error) { errorMessage(error); }
    finally { disable(false); }
  });
  void refresh();
}
