import { account, APIError, login, logout } from './api';
import type { APIConnection } from './types';

export function setupAuth(container: HTMLElement) {
  container.innerHTML = `
    <div class="account-heading"><strong>AI 接口配置</strong><span id="account-status" class="quiet" role="status">正在读取配置…</span><button id="account-refresh" class="text-button" type="button">刷新状态</button><button id="connection-edit" class="text-button" type="button" hidden>修改配置</button></div>
    <div id="login-options" class="login-options">
      <form id="key-form" class="provider-form">
        <label for="api-base-url">API URL</label><input id="api-base-url" type="url" placeholder="https://api.example.com/v1" autocomplete="off" required maxlength="2048" />
        <label for="api-model">模型</label><input id="api-model" type="text" placeholder="填写供应商支持的模型名称" autocomplete="off" required maxlength="256" />
        <label for="api-key">API Key</label><input id="api-key" type="password" placeholder="填写 API Key" autocomplete="off" required maxlength="8192" />
        <button id="key-connect" class="choose-file" type="submit">验证并保存</button>
      </form>
      <p class="quiet">支持 OpenAI 兼容接口的基础 URL 或完整接口地址。使用 Responses 时请填写以 /responses 结尾的地址，联网核价还需供应商支持网页搜索。</p>
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
    container.querySelectorAll<HTMLButtonElement | HTMLInputElement>('button, input').forEach(control => {
      if (!control.closest('dialog')) control.disabled = disabled;
    });
  }
  async function refresh() {
    const version = ++epoch;
    try {
      const info = await account();
      if (version !== epoch) return false;
      el('account-status').textContent = info.connected ? `已配置 · ${info.model} · ${info.baseUrl}` : '未配置';
      el('login-options').hidden = info.connected && !editing;
      el('connection-edit').hidden = !info.connected;
      el('connection-edit').textContent = editing ? '收起配置' : '修改配置';
      el('logout-button').hidden = !info.connected;
      if (info.connected) {
        el<HTMLInputElement>('api-base-url').value = info.baseUrl;
        el<HTMLInputElement>('api-model').value = info.model;
      }
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
      if (await refresh()) showResult('success', '已收到模型的测试回复并保存配置，可以开始分析。');
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
    void connect({ apiKey: el<HTMLInputElement>('api-key').value, baseUrl: el<HTMLInputElement>('api-base-url').value.trim(), model: el<HTMLInputElement>('api-model').value.trim() });
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
