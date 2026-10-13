import { account, APIError, login, logout } from './api';
import type { CodexConnection } from './types';

export function setupAuth(container: HTMLElement) {
  container.innerHTML = `
    <div class="account-heading"><strong>Codex</strong><span id="account-status" class="quiet" role="status">正在连接…</span><button id="account-refresh" class="text-button" type="button">刷新状态</button><button id="connection-edit" class="text-button" type="button" hidden>切换连接</button></div>
    <div id="login-options" class="login-options">
      <button id="oauth-login" type="button" class="choose-file">OAuth 登录（ChatGPT）</button>
      <details id="key-details"><summary>API Key / 自定义供应商</summary>
        <form id="key-form" class="provider-form">
          <label for="api-base-url">API 地址 <span class="quiet">（留空使用 OpenAI）</span></label><input id="api-base-url" type="url" placeholder="https://api.example.com/v1" autocomplete="off" maxlength="2048" />
          <label for="api-model">模型 <span class="quiet">（自定义地址必填）</span></label><input id="api-model" type="text" placeholder="填写供应商支持的模型名称" autocomplete="off" maxlength="256" />
          <label for="api-key">API Key</label><input id="api-key" type="password" placeholder="API Key" autocomplete="off" required maxlength="8192" />
          <button class="choose-file" type="submit">连接</button>
        </form>
        <div class="ccswitch-import">
          <button id="ccswitch-import" class="choose-file" type="button">从 CC Switch 导入并连接</button>
          <p class="quiet">读取 CC Switch 当前启用的 Codex 配置。切换供应商后，请在这里重新导入；使用本地路由时需保持 CC Switch 运行。</p>
          <details><summary>指定配置目录（可选）</summary><label class="sr-only" for="ccswitch-dir">Codex 配置目录</label><input id="ccswitch-dir" type="text" placeholder="包含 config.toml 的目录，默认 ~/.codex" autocomplete="off" maxlength="4096" /></details>
        </div>
      </details>
    </div>
    <p id="oauth-pending" class="message" hidden>请在授权页面完成登录。<a id="oauth-link" target="_blank" rel="noopener noreferrer" hidden>打开授权页面 ↗</a></p>
    <button id="logout-button" class="text-button" type="button" hidden>退出登录 / 取消授权</button>
    <p id="auth-error" class="message error" role="alert" hidden></p>
  `;
  const el = <T extends HTMLElement>(id: string) => container.querySelector<T>(`#${id}`)!;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let epoch = 0;
  let busy = false;
  let editing = false;
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
      const provider = info.provider ? ` · ${info.provider}` : '';
      const model = info.model ? ` · ${info.model}` : '';
      const source = info.source === 'ccswitch' ? ' · 来自 CC Switch' : '';
      el('account-status').textContent = info.pending ? '等待授权' : info.loggedIn ? `已连接 · ${info.authType === 'apiKey' ? 'API Key' : 'ChatGPT'}${info.email ? ` · ${info.email}` : ''}${provider}${model}${source}` : '未登录';
      el('login-options').hidden = info.loggedIn && !info.pending && !editing;
      el('connection-edit').hidden = !info.loggedIn || info.pending;
      el('connection-edit').textContent = editing ? '收起连接设置' : '切换连接';
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
  async function signIn(connection: CodexConnection) {
    if (busy) return;
    epoch++;
    clearTimeout(timer);
    disable(true);
    el('auth-error').hidden = true;
    const input = el<HTMLInputElement>('api-key');
    try {
      input.value = '';
      const result = await login(connection);
      if (result.authUrl) {
        const url = new URL(result.authUrl);
        if (url.protocol !== 'https:' || !['auth.openai.com', 'chatgpt.com', 'auth0.openai.com'].includes(url.hostname)) throw new APIError('invalid_auth_url', '授权地址无效，请检查 Codex 版本。');
        const link = el<HTMLAnchorElement>('oauth-link');
        link.href = url.href;
        link.hidden = false;
        el('oauth-pending').hidden = false;
      }
      el<HTMLDetailsElement>('key-details').open = false;
      editing = false;
      await refresh();
    } catch (error) { errorMessage(error); }
    finally { input.value = ''; disable(false); }
  }
  el('oauth-login').addEventListener('click', () => { void signIn({ type: 'chatgpt' }); });
  el('key-form').addEventListener('submit', event => {
    event.preventDefault();
    const baseUrl = el<HTMLInputElement>('api-base-url').value.trim();
    const model = el<HTMLInputElement>('api-model').value.trim();
    void signIn({ type: 'apiKey', apiKey: el<HTMLInputElement>('api-key').value, baseUrl: baseUrl || undefined, model: model || undefined });
  });
  el<HTMLInputElement>('api-base-url').addEventListener('input', () => { el<HTMLInputElement>('api-model').required = el<HTMLInputElement>('api-base-url').value.trim() !== ''; });
  el('ccswitch-import').addEventListener('click', () => { void signIn({ type: 'apiKey', source: 'ccswitch', configDir: el<HTMLInputElement>('ccswitch-dir').value.trim() || undefined }); });
  el('connection-edit').addEventListener('click', () => { editing = !editing; void refresh(); });
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
