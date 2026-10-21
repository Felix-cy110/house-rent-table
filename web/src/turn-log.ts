import { conciseTurnJSON } from './turn-log-summary';

type LogMode = 'concise' | 'full';
interface TurnSnapshot { id: string; json: string; fileName: string; mode: LogMode; concise?: string | null }

function node<K extends keyof HTMLElementTagNameMap>(tag: K, text = '', className = '') {
  const element = document.createElement(tag);
  element.textContent = text;
  element.className = className;
  return element;
}

// Highlight the original string, never a parsed/serialized copy: vendor IDs
// can exceed JavaScript's safe integer range. All vendor text stays in text nodes.
function highlightJSON(json: string, code: HTMLElement) {
  if (json.length > 2_000_000) { code.textContent = json; return; }
  const tokens = /"(?:\\.|[^"\\])*"(?=\s*:)|"(?:\\.|[^"\\])*"|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|\b(?:true|false|null)\b/g;
  const fragment = document.createDocumentFragment();
  let offset = 0;
  for (const match of json.matchAll(tokens)) {
    fragment.append(document.createTextNode(json.slice(offset, match.index)));
    const value = match[0];
    const kind = value.startsWith('"')
      ? (/^\s*:/.test(json.slice(match.index + value.length)) ? 'key' : 'string')
      : (value === 'null' ? 'null' : (value === 'true' || value === 'false' ? 'boolean' : 'number'));
    fragment.append(node('span', value, `json-${kind}`));
    offset = match.index + value.length;
  }
  fragment.append(document.createTextNode(json.slice(offset)));
  code.replaceChildren(fragment);
}

function renderSnapshot(snapshot: TurnSnapshot): HTMLElement {
  const content = node('div');
  const heading = node('div', '', 'turn-heading');
  const title = node('div');
  title.append(node('p', '本次 AI 分析', 'turn-eyebrow'), node('h1', '分析日志'));
  title.append(node('p', snapshot.fileName || '完整分析过程', 'turn-filename'));
  heading.append(title, node('span', '已完成', 'turn-badge'));
  content.append(heading);

  // Metadata may be parsed normally; the full view always uses the original.
  let metadata: Record<string, unknown> = {};
  try {
    const parsed: unknown = JSON.parse(snapshot.json);
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) metadata = parsed as Record<string, unknown>;
  } catch { /* The original response remains available for inspection. */ }
  const events = Array.isArray(metadata.events) ? metadata.events : [];
  const count = (kind: string) => events.filter(event => event && typeof event === 'object' && event.kind === kind).length;
  const duration = typeof metadata.durationMs === 'number' && Number.isFinite(metadata.durationMs) && metadata.durationMs >= 0
    ? `${(metadata.durationMs / 1000).toLocaleString('zh-CN', { maximumFractionDigits: 2 })} 秒` : '未记录';
  const stats = node('dl', '', 'turn-stats');
  const values = [
    ['分析模型', typeof metadata.model === 'string' ? metadata.model : '未记录'],
    ['模型请求', Array.isArray(metadata.events) ? `${count('model')} 轮` : '未记录'],
    ['工具调用', Array.isArray(metadata.events) ? `${count('tool')} 次` : '未记录'],
    ['总耗时', duration],
  ];
  for (const [label, value] of values) {
    const item = node('div', '', 'turn-stat');
    item.append(node('dt', label), node('dd', value));
    stats.append(item);
  }
  content.append(stats);

  if (snapshot.concise === undefined) snapshot.concise = conciseTurnJSON(snapshot.json);
  if (snapshot.concise === null) snapshot.mode = 'full';
  const viewer = node('section', '', 'turn-viewer');
  const toolbar = node('div', '', 'turn-toolbar');
  const label = node('div', '', 'turn-code-heading');
  const viewerTitle = node('h2');
  label.append(node('span', '{ }', 'turn-code-icon'), viewerTitle);
  const modes = node('div', '', 'turn-modes');
  modes.setAttribute('role', 'group');
  modes.setAttribute('aria-label', 'JSON 版本');
  const concise = node('button', '精简版', 'turn-mode-button');
  const full = node('button', '全量版', 'turn-mode-button');
  concise.type = full.type = 'button';
  concise.disabled = snapshot.concise === null;
  for (const button of [concise, full]) button.setAttribute('aria-controls', 'turn-json');
  modes.append(concise, full);
  label.append(modes);
  const hint = node('p', '', 'turn-mode-hint');
  hint.id = 'turn-mode-hint';
  modes.setAttribute('aria-describedby', hint.id);
  const actions = node('div', '', 'turn-tools');
  const wrap = node('button', '自动换行', 'turn-tool-button');
  wrap.type = 'button';
  wrap.setAttribute('aria-pressed', 'false');
  const copy = node('button', '复制 JSON', 'turn-tool-button');
  copy.type = 'button';
  const download = node('button', '下载 JSON', 'turn-tool-button turn-download');
  download.type = 'button';
  actions.append(wrap, copy, download);
  toolbar.append(label, actions);

  const scroll = node('div', '', 'turn-code-scroll');
  scroll.tabIndex = 0;
  scroll.setAttribute('role', 'region');
  scroll.setAttribute('aria-label', 'JSON 内容，可滚动');
  const lines = node('pre', '', 'turn-line-numbers');
  lines.setAttribute('aria-hidden', 'true');
  const pre = node('pre', '', 'turn-code');
  const code = node('code');
  code.id = 'turn-json';
  pre.append(code);
  scroll.append(lines, pre);
  const footer = node('div', '', 'turn-code-footer');
  const size = node('span');
  footer.append(size, node('span', 'JSON · UTF-8'));
  const status = node('p', '', 'turn-action-status');
  status.setAttribute('role', 'status');
  viewer.append(toolbar, hint, scroll, footer);
  content.append(viewer, status, node('p', '日志保留本次请求、响应和工具结果，密钥已脱敏。当前日志未持久化，刷新或清空后无法恢复，可下载留存。', 'turn-note'));

  const selectedJSON = () => snapshot.mode === 'concise' ? snapshot.concise! : snapshot.json;
  const modeName = () => snapshot.mode === 'concise' ? '精简版' : '全量版';
  function updateMode(mode: LogMode) {
    snapshot.mode = mode;
    const json = selectedJSON();
    viewerTitle.textContent = `${modeName()} JSON`;
    viewer.setAttribute('aria-label', `${modeName()} turn JSON`);
    concise.setAttribute('aria-pressed', String(mode === 'concise'));
    full.setAttribute('aria-pressed', String(mode === 'full'));
    hint.textContent = snapshot.concise === null
      ? '此日志格式暂不支持精简，已显示全量原文。'
      : mode === 'concise'
        ? '适合调提示词：保留实际提示词、输入、思考、回复、工具结果和中途变化，省去重复历史与 HTTP 元数据。'
        : '完整原始记录：包含每轮请求、响应、搜索过程、用量及供应商扩展字段。';
    highlightJSON(json, code);
    const lineCount = json.split('\n').length;
    lines.textContent = Array.from({ length: lineCount }, (_, index) => String(index + 1)).join('\n');
    const bytes = new Blob([json]).size;
    size.textContent = `${lineCount.toLocaleString('zh-CN')} 行 · ${bytes < 1024 ? `${bytes} B` : `${(bytes / 1024).toFixed(1)} KB`}`;
    status.textContent = '';
    scroll.scrollTo(0, 0);
  }
  concise.addEventListener('click', () => updateMode('concise'));
  full.addEventListener('click', () => updateMode('full'));
  updateMode(snapshot.mode);

  wrap.addEventListener('click', () => {
    const enabled = scroll.classList.toggle('turn-wrap');
    wrap.setAttribute('aria-pressed', String(enabled));
  });
  copy.addEventListener('click', async () => {
    copy.disabled = true;
    const name = modeName();
    try {
      await navigator.clipboard.writeText(selectedJSON());
      status.textContent = `${name} JSON 已复制。`;
    } catch {
      status.textContent = '复制失败，请选中 JSON 手动复制，或下载文件。';
    } finally { copy.disabled = false; }
  });
  download.addEventListener('click', () => {
    const url = URL.createObjectURL(new Blob([selectedJSON()], { type: 'application/json;charset=utf-8' }));
    const link = node('a');
    link.href = url;
    link.download = `turn-${snapshot.id}-${snapshot.mode}.json`;
    link.click();
    // Allow the browser to start consuming the download before releasing it.
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  });
  return content;
}

export function setupTurnLog(report: HTMLElement) {
  const app = report.parentElement!;
  const view = node('section', '', 'turn-page');
  view.id = 'turn-log-view';
  view.hidden = true;
  view.setAttribute('aria-label', '分析日志');
  app.append(view);
  const reportTitle = document.title;
  const baseURL = `${location.pathname}${location.search}`;
  const session = crypto.randomUUID();
  let snapshot: TurnSnapshot | null = null;
  let returnLink: HTMLAnchorElement | null = null;
  let reportScroll = 0;
  let lastRoute: string | null = null;

  function back() {
    if (history.state?.turnLogSession === session) {
      history.back();
    } else {
      history.replaceState(null, '', baseURL);
      renderRoute();
    }
  }

  function renderRoute() {
    if (lastRoute === location.hash) return;
    lastRoute = location.hash;
    const showingLog = location.hash.startsWith('#/logs/');
    const wasShowingLog = !view.hidden;
    if (showingLog && !wasShowingLog) reportScroll = window.scrollY;
    report.hidden = showingLog;
    view.hidden = !showingLog;
    app.classList.toggle('showing-turn-log', showingLog);
    document.title = showingLog ? '分析日志 · 租房信息检查' : reportTitle;
    view.replaceChildren();
    if (!showingLog) {
      if (wasShowingLog) {
        returnLink?.focus({ preventScroll: true });
        window.scrollTo(0, reportScroll);
      }
      return;
    }
    const nav = node('nav', '', 'turn-nav');
    nav.setAttribute('aria-label', '日志导航');
    const backButton = node('button', '← 返回报告', 'turn-back');
    backButton.type = 'button';
    backButton.addEventListener('click', back);
    nav.append(backButton, node('span', '租房检查 / 分析日志', 'turn-breadcrumb'));
    view.append(nav);
    if (snapshot && location.hash === `#/logs/${snapshot.id}`) {
      view.append(renderSnapshot(snapshot));
    } else {
      const empty = node('div', '', 'turn-empty');
      empty.append(node('div', '{ }', 'turn-empty-icon'), node('h1', '日志已失效'), node('p', '刷新、清空或重新分析后，原日志不再保留。请返回报告，重新分析后查看日志。'));
      view.append(empty);
    }
    backButton.focus({ preventScroll: true });
    window.scrollTo(0, 0);
  }

  window.addEventListener('popstate', renderRoute);
  window.addEventListener('hashchange', renderRoute);
  renderRoute();

  return {
    clear() {
      snapshot = null;
      returnLink = null;
      if (!view.hidden) { lastRoute = null; renderRoute(); }
    },
    createLink(json: string, fileName: string) {
      snapshot = { id: crypto.randomUUID(), json, fileName, mode: 'concise' };
      const hash = `#/logs/${snapshot.id}`;
      const link = node('a', '查看日志', 'choose-file');
      link.href = `${baseURL}${hash}`;
      link.addEventListener('click', event => {
        if (event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
        event.preventDefault();
        history.pushState({ turnLogSession: session }, '', link.href);
        renderRoute();
      });
      returnLink = link;
      return link;
    },
  };
}
