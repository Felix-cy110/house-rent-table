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
          <button id="key-connect" class="choose-file" type="submit">连接</button>
        </form>
        <p class="quiet">连接时会发送一次简短模型请求验证配置，可能产生少量费用。</p>
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
    <dialog id="connection-success" class="connection-success" aria-labelledby="connection-success-title" aria-describedby="connection-success-message">
      <span class="connection-success-icon" aria-hidden="true">✓</span>
      <h2 id="connection-success-title">连接成功</h2>
      <p id="connection-success-message"></p>
      <form method="dialog"><button class="primary-button" type="submit" autofocus>知道了</button></form>
    </dialog>
  `;
  const el = <T extends HTMLElement>(id: string) => container.querySelector<T>(`#${id}`)!;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let epoch = 0;
  let busy = false;
  let editing = false;
  let connectionSuccessMessage: string | undefined;
  function showConnectionSuccess(message: string) {
    el('connection-success-message').textContent = message;
    el<HTMLDialogElement>('connection-success').showModal();
  }
  function errorMessage(error: unknown) {
    const node = el('auth-error');
    node.textContent = error instanceof APIError ? error.message : '无法连接服务，请稍后重试。';
    node.hidden = false;
  }
  function disable(disabled: boolean) {
    busy = disabled;
    container.querySelectorAll<HTMLButtonElement>('button').forEach(button => { if (!button.closest('dialog')) button.disabled = disabled; });
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
      else if (connectionSuccessMessage) {
        const message = connectionSuccessMessage;
        connectionSuccessMessage = undefined;
        if (info.loggedIn && !info.error) showConnectionSuccess(message);
      }
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
    connectionSuccessMessage = undefined;
    el('auth-error').hidden = true;
    const previousStatus = el('account-status').textContent;
    const button = el<HTMLButtonElement>(connection.type === 'apiKey' ? (connection.source === 'ccswitch' ? 'ccswitch-import' : 'key-connect') : 'oauth-login');
    const previousButtonText = button.textContent;
    if (connection.type === 'apiKey') button.textContent = '正在验证连接…';
    if (connection.type === 'apiKey') el('account-status').textContent = '正在验证连接…';
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
      connectionSuccessMessage = result.type === 'apiKey' ? '已收到模型的测试回复，可以开始分析。' : '已完成 ChatGPT 授权，可以开始分析。';
      el<HTMLDetailsElement>('key-details').open = false;
      editing = false;
      await refresh();
    } catch (error) {
      el('account-status').textContent = previousStatus;
      errorMessage(error);
    }
    finally { input.value = ''; button.textContent = previousButtonText; disable(false); }
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
  el('connection-success').addEventListener('close', () => { el('account-refresh').focus(); });
  el('logout-button').addEventListener('click', async () => {
    if (busy) return;
    epoch++;
    clearTimeout(timer);
    disable(true);
    connectionSuccessMessage = undefined;
    try { await logout(); await refresh(); } catch (error) { errorMessage(error); }
    finally { disable(false); }
  });
  void refresh();
}
