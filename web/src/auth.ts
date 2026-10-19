import { account, models, APIError, login, logout } from './api';
import type { APIConnection, ModelProvider } from './types';

export function setupAuth(container: HTMLElement) {
  container.innerHTML = `
    <div class="account-heading"><strong>AI 接口配置</strong><span id="account-status" class="quiet" role="status">正在读取配置…</span><button id="account-refresh" class="text-button" type="button">刷新状态</button><button id="connection-edit" class="text-button" type="button" hidden>修改配置</button></div>
    <p id="connected-version" class="quiet" hidden></p>
    <div id="login-options" class="login-options">
      <form id="key-form" class="provider-form">
        <label for="api-provider">模型</label><select id="api-provider" required aria-describedby="provider-note" disabled><option value="">正在读取模型…</option></select>
        <label for="api-key">API Key</label><input id="api-key" type="password" placeholder="填写 API Key" autocomplete="off" required maxlength="8192" />
        <button id="key-connect" class="choose-file" type="submit" disabled>验证并保存</button>
      </form>
      <p class="quiet">三种模型均开启思考模式。选择模型后，只需填写该供应商的 API Key。</p>
      <p id="provider-note" class="quiet" role="status"></p>
      <p class="quiet">保存前会发送一次简短模型请求验证配置，可能产生少量费用。密钥仅保存在本机服务中。</p>
    </div>
    <button id="logout-button" class="text-button" type="button" hidden>清除配置</button>
    <p id="auth-error" class="message error" role="alert" hidden></p>
    <dialog id="connection-result" class="connection-result" aria-labelledby="connection-result-title" aria-describedby="connection-result-message">
      <span id="connection-result-icon" class="connection-result-icon" aria-hidden="true"></span>
      <h2 id="connection-result-title"></h2>
      <p id="connection-result-message"></p>
      <form method="dialog"><button class="primary-button" type="submit" autofocus>知道了</button></form>
    </dialog>
  `;
  const el = <T extends HTMLElement>(id: string) => container.querySelector<T>(`#${id}`)!;
  let epoch = 0;
  let busy = false;
  let editing = false;
  let providers: ModelProvider[] = [];
  function showProviderNote() {
    const selected = providers.find(p => p.id === el<HTMLSelectElement>('api-provider').value);
    el('provider-note').textContent = selected ? [selected.versionNote, selected.searchNote].filter(Boolean).join(' ') : '';
  }
  el('api-provider').addEventListener('change', showProviderNote);
  async function loadModels() {
    if (providers.length) return;
    providers = await models();
    const select = el<HTMLSelectElement>('api-provider');
    select.replaceChildren(...providers.map(p => new Option(p.name, p.id)));
    showProviderNote();
    if (!busy) disable(false);
  }
  function showResult(status: 'success' | 'error', message: string) {
    const dialog = el<HTMLDialogElement>('connection-result');
    dialog.dataset.status = status;
    el('connection-result-icon').textContent = status === 'success' ? '✓' : '!';
    el('connection-result-title').textContent = status === 'success' ? '连接成功' : '连接失败';
    el('connection-result-message').textContent = message;
    if (!dialog.open) dialog.showModal();
  }
  function showError(error: unknown) {
    const node = el('auth-error');
    node.textContent = error instanceof APIError ? error.message : '无法连接服务，请稍后重试。';
    node.hidden = false;
    return node.textContent;
  }
  function disable(disabled: boolean) {
    busy = disabled;
    container.querySelectorAll<HTMLButtonElement | HTMLInputElement | HTMLSelectElement>('button, input, select').forEach(control => {
      if (!control.closest('dialog')) control.disabled = disabled;
    });
  }
  async function refresh() {
    const version = ++epoch;
    try {
      await loadModels();
      const info = await account();
      if (version !== epoch) return false;
      el('account-status').textContent = info.connected ? `已配置 · ${info.name ?? info.model} · 思考已开启` : '未配置';
      el('login-options').hidden = info.connected && !editing;
      el('connection-edit').hidden = !info.connected;
      el('connection-edit').textContent = editing ? '收起配置' : '修改配置';
      el('logout-button').hidden = !info.connected;
      if (info.connected && !editing && info.provider) el<HTMLSelectElement>('api-provider').value = info.provider;
      showProviderNote();
      const versionNote = providers.find(p => p.id === info.provider)?.versionNote;
      el('connected-version').textContent = versionNote ?? '';
      el('connected-version').hidden = !info.connected || editing || !versionNote;
      if (info.needsReconnect) el('account-status').textContent = '请重新配置：现仅支持三种指定模型';
      el('auth-error').hidden = true;
      return info.connected;
    } catch (error) {
      if (version !== epoch) return false;
      el('account-status').textContent = '配置读取失败';
      showError(error);
      return false;
    }
  }
  async function connect(connection: APIConnection) {
    if (busy) return;
    epoch++;
    disable(true);
    el('auth-error').hidden = true;
    const previousStatus = el('account-status').textContent;
    const button = el<HTMLButtonElement>('key-connect');
    button.textContent = '正在验证连接…';
    el('account-status').textContent = '正在验证连接…';
    const input = el<HTMLInputElement>('api-key');
    input.value = '';
    try {
      await login(connection);
      editing = false;
      if (await refresh()) showResult('success', '已收到模型的测试回复并保存配置，可以开始分析。联网搜索权限将在实际核价时检查。');
      else showResult('error', '配置已提交，但未能读取连接状态，请刷新状态确认。');
    } catch (error) {
      el('account-status').textContent = previousStatus;
      showResult('error', showError(error));
    } finally {
      input.value = '';
      button.textContent = '验证并保存';
      disable(false);
    }
  }
  el('key-form').addEventListener('submit', event => {
    event.preventDefault();
    void connect({ apiKey: el<HTMLInputElement>('api-key').value, provider: el<HTMLSelectElement>('api-provider').value });
  });
  el('connection-edit').addEventListener('click', () => { editing = !editing; void refresh(); });
  el('account-refresh').addEventListener('click', () => { void refresh(); });
  el('connection-result').addEventListener('close', () => {
    if (el('connection-result').dataset.status === 'error' && el('key-connect').getClientRects().length) el('key-connect').focus();
    else el('account-refresh').focus();
  });
  el('logout-button').addEventListener('click', async () => {
    if (busy) return;
    epoch++;
    disable(true);
    try {
      await logout();
      editing = false;
      el<HTMLFormElement>('key-form').reset();
      await refresh();
    } catch (error) { showError(error); }
    finally { disable(false); }
  });
  void refresh();
}
