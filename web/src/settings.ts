import { version } from '../package.json';

const developerModeKey = 'house-rent:developer-mode';

export function setupSettings(container: HTMLElement) {
  container.innerHTML = `
    <button id="settings-button" class="settings-button" type="button" aria-haspopup="dialog" aria-controls="settings-dialog">
      <svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="m9 3-.7 2.1-1.9 1.1-2.2-.4-3 5.2 1.5 1.7v2.2l-1.5 1.7 3 5.2 2.2-.4 1.9 1.1L9 24h6l.7-2.1 1.9-1.1 2.2.4 3-5.2-1.5-1.7v-2.2l1.5-1.7-3-5.2-2.2.4-1.9-1.1L15 3Z" transform="translate(2 0) scale(.83)" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round"/><circle cx="12" cy="11.2" r="3" stroke="currentColor" stroke-width="1.5"/></svg>
      设置
    </button>
    <dialog id="settings-dialog" class="settings-dialog" aria-labelledby="settings-title">
      <div class="settings-heading">
        <h2 id="settings-title">设置</h2>
        <form method="dialog"><button class="text-button settings-close" type="submit" aria-label="关闭设置" autofocus>×</button></form>
      </div>
      <p class="settings-app-name">租房检查</p>
      <div class="settings-row">
        <span>当前版本</span>
        <button id="app-version" class="version-button" type="button" aria-label="版本号 ${version}" aria-describedby="version-hint" aria-pressed="false">${version}</button>
      </div>
      <p id="version-hint" class="format-hint">双击版本号可切换开发者模式。<span class="sr-only">键盘可按 Enter 或空格切换。</span></p>
      <div class="settings-row developer-mode-row" role="status" aria-live="polite">
        <span>开发者模式</span><span id="developer-mode-status" class="mode-badge">未开启</span>
      </div>
      <p id="settings-storage-notice" class="format-hint" role="status" hidden>当前浏览器无法保存设置，本次切换仅在当前页面生效。</p>
    </dialog>
  `;

  const el = <T extends HTMLElement>(id: string) => container.querySelector<T>(`#${id}`)!;
  const dialog = el<HTMLDialogElement>('settings-dialog');
  const button = el<HTMLButtonElement>('settings-button');
  const versionButton = el<HTMLButtonElement>('app-version');
  let developerMode = false;
  try {
    developerMode = localStorage.getItem(developerModeKey) === 'true';
  } catch { /* Settings remain usable when browser storage is unavailable. */ }

  function renderMode() {
    document.documentElement.dataset.developerMode = String(developerMode);
    versionButton.setAttribute('aria-pressed', String(developerMode));
    button.dataset.developerMode = String(developerMode);
    el('developer-mode-status').textContent = developerMode ? '已开启' : '未开启';
    el('developer-mode-status').classList.toggle('enabled', developerMode);
  }

  function toggleDeveloperMode() {
    developerMode = !developerMode;
    renderMode();
    try {
      if (developerMode) localStorage.setItem(developerModeKey, 'true');
      else localStorage.removeItem(developerModeKey);
      el('settings-storage-notice').hidden = true;
    } catch {
      el('settings-storage-notice').hidden = false;
    }
  }

  button.addEventListener('click', () => { if (!dialog.open) dialog.showModal(); });
  dialog.addEventListener('close', () => button.focus());
  versionButton.addEventListener('dblclick', toggleDeveloperMode);
  versionButton.addEventListener('click', event => {
    // Keyboard and assistive-technology activation has no pointer click count.
    if (event.detail === 0) toggleDeveloperMode();
  });
  renderMode();
}
