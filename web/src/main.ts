import './style.css';
import { analyze, APIError, importExcel, persistenceStatus } from './api';
import { setupAuth } from './auth';
import { renderReport } from './report';
import type { AnalysisResult, RentalDocument, RentalField } from './types';

document.querySelector<HTMLDivElement>('#app')!.innerHTML = `
  <header class="page-header">
    <div class="wordmark"><span class="brand-icon" aria-hidden="true">⌂</span> 租房检查</div>
    <a class="template-link" href="/api/template" download>下载空白模板 <span aria-hidden="true">↗</span></a>
  </header>
  <section class="intro">
    <h1>先把租房信息看清楚。</h1>
    <p>上传中介填写的 Excel，联网核对报价，核算加粗项目。</p>
  </section>
  <section id="codex-account" class="account-panel" aria-label="Codex 登录"></section>
  <section class="upload-panel" aria-labelledby="upload-title">
    <div class="section-heading"><h2 id="upload-title"><span class="step">01</span> 上传表格</h2><span class="quiet">一份表格，一套房源</span></div>
    <div id="drop-zone" class="drop-zone">
      <svg class="upload-icon" viewBox="0 0 32 32" fill="none" aria-hidden="true"><path d="M10 3h9l6 6v19H7V3h3Z" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/><path d="M19 3v7h6M16 23V14m-4 4 4-4 4 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
      <p class="drop-title">把填写好的 Excel 拖到这里</p>
      <p class="drop-detail">.xlsx 格式，最大 10 MB</p>
      <label class="choose-file" for="excel-file">选择 Excel 文件</label>
      <input class="sr-only" id="excel-file" type="file" accept=".xlsx,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" />
    </div>
    <p class="format-hint">A 列填写项目，B 列填写内容。将需要核算的项目名整体加粗，可以自由增减项目。</p>
    <p id="upload-status" class="message" role="status" aria-live="polite" hidden></p>
    <p id="upload-error" class="message error" role="alert" hidden></p>
  </section>
  <section id="document-panel" class="document-panel" aria-labelledby="document-title" hidden>
    <div class="section-heading">
      <h2 id="document-title"><span class="step">02</span> 核对信息</h2>
      <button id="clear-button" class="text-button" type="button">清空</button>
    </div>
    <div class="file-heading"><p id="file-name"></p><span id="field-count" class="quiet"></span></div>
    <p id="calculation-targets" class="format-hint"></p>
    <div id="import-warnings" class="import-warnings" hidden></div>
    <div class="table-wrap"><table><caption class="sr-only">Excel 识别内容</caption><thead><tr><th scope="col">项目</th><th scope="col">填写内容</th></tr></thead><tbody id="field-list"></tbody></table></div>
    <details class="payload-preview"><summary>查看将发送的完整内容</summary><pre id="payload-content"></pre></details>
    <div class="analysis-action"><p>完整内容与加粗标记将发送给 Codex，联网核价并核算加粗项目。</p><div class="analysis-buttons"><button id="cancel-analysis" class="text-button" type="button" hidden>取消分析</button><button id="analyze-button" class="primary-button" type="button">发送并分析 <span aria-hidden="true">→</span></button></div></div>
    <p id="persistence-notice" class="format-hint" hidden>已启用历史保存：发送分析时会另行调用 AI 提取业务数据。取消报告、清空或刷新页面不撤销已提交的保存任务。</p>
    <p id="persistence-status" class="message" role="status" hidden></p>
  </section>
  <section id="analysis-panel" class="analysis-panel" aria-labelledby="analysis-title" aria-live="polite" hidden>
    <h2 id="analysis-title"><span class="step">03</span> 检查结果</h2>
    <div id="analysis-content"></div>
  </section>
  <footer>只核查表内项目。价格对照附来源，无法核实的内容会单独说明。<br /><span class="session-note">清空或刷新只移除页面中的表格与回复；已启用的后台历史保存不受影响。登录凭据由本机 Codex 单独管理。</span></footer>
`;

setupAuth(document.getElementById('codex-account')!);
void fetch('/api/persistence').then(response => response.json()).then((config: { enabled?: boolean }) => {
  if (config.enabled) document.getElementById('persistence-notice')!.hidden = false;
}).catch(() => { /* Saving receipts still report admission even if configuration lookup fails. */ });

function element<T extends HTMLElement>(id: string): T {
  return document.getElementById(id) as T;
}

function textElement<K extends keyof HTMLElementTagNameMap>(tag: K, text: string, className = '') {
  const node = document.createElement(tag);
  node.textContent = text;
  node.className = className;
  return node;
}

const fileInput = element<HTMLInputElement>('excel-file');
const dropZone = element<HTMLDivElement>('drop-zone');
const panel = element<HTMLElement>('document-panel');
const list = element<HTMLTableSectionElement>('field-list');
const analyzeButton = element<HTMLButtonElement>('analyze-button');
const analysisPanel = element<HTMLElement>('analysis-panel');
const analysisContent = element<HTMLDivElement>('analysis-content');
let currentDocument: RentalDocument | null = null;
let activeRequest: AbortController | null = null;
let generation = 0;
let persistenceGeneration = 0;

async function trackPersistence(id: string, version: number) {
  for (let attempt = 0; attempt < 300 && version === persistenceGeneration; attempt++) {
    try {
      const status = await persistenceStatus(id);
      if (version !== persistenceGeneration) return;
      if (status.status === 'SUCCEEDED') { showMessage('persistence-status', '本次业务数据已保存。'); return; }
      if (status.status === 'FAILED') { showMessage('persistence-status', status.message ?? '后台提取失败，未生成业务记录。'); return; }
      showMessage('persistence-status', status.message ?? (status.stage === 'EXTRACT' ? '后台正在提取业务数据…' : '后台正在保存业务数据…'));
    } catch { if (version === persistenceGeneration) showMessage('persistence-status', '暂时无法查询后台保存状态，任务不会因关闭页面而取消。'); }
    await new Promise(resolve => setTimeout(resolve, 2000));
  }
}

function showMessage(id: string, message: string) {
  const node = element(id);
  node.textContent = message;
  node.hidden = !message;
}

function clearDocument() {
  persistenceGeneration++;
  showMessage('persistence-status', '');
  generation++;
  activeRequest?.abort();
  activeRequest = null;
  currentDocument = null;
  panel.hidden = true;
  analysisPanel.hidden = true;
  list.replaceChildren();
  analysisContent.replaceChildren();
  element('import-warnings').replaceChildren();
  element('import-warnings').hidden = true;
  fileInput.value = '';
  showMessage('upload-status', '');
  showMessage('upload-error', '');
  analyzeButton.disabled = false;
  analyzeButton.textContent = '发送并分析 →';
  element('cancel-analysis').hidden = true;
  element('payload-content').textContent = '';
  element('calculation-targets').textContent = '';
}

function renderFields(fields: RentalField[]) {
  const fragment = document.createDocumentFragment();
  for (const field of fields) {
    const row = document.createElement('tr');
    row.id = field.id;
    const label = document.createElement('th');
    label.scope = 'row';
    label.append(textElement(field.bold ? 'strong' : 'span', field.label));
    if (field.bold) label.append(textElement('small', '需核算', 'calculation-marker'));
    label.append(textElement('small', `${field.sheet} · ${field.cell}`, 'cell-source'));
    const value = textElement('td', field.value ?? '未填写', field.value === null ? 'empty-value' : '');
    row.append(label, value);
    fragment.append(row);
  }
  list.replaceChildren(fragment);
}

async function upload(file: File) {
  clearDocument();
  const requestGeneration = generation;
  if (!file.name.toLowerCase().endsWith('.xlsx')) {
    showMessage('upload-error', '请选择 .xlsx 文件。其他格式请先在 Excel 中另存为 .xlsx。');
    return;
  }
  if (file.size > 10 * 1024 * 1024) {
    showMessage('upload-error', '文件不能超过 10 MB，请精简后重新上传。');
    return;
  }
  activeRequest = new AbortController();
  showMessage('upload-status', `正在读取 ${file.name}…`);
  try {
    const response = await importExcel(file, activeRequest.signal);
    if (requestGeneration !== generation) return;
    currentDocument = response.document;
    element('payload-content').textContent = JSON.stringify(currentDocument, null, 2);
    element('file-name').textContent = currentDocument.fileName;
    const blankCount = currentDocument.fields.filter(field => field.value === null).length;
    element('field-count').textContent = `${currentDocument.fields.length} 个项目${blankCount ? `，${blankCount} 项未填写` : ''}`;
    renderFields(currentDocument.fields);
    const targetCount = currentDocument.fields.filter(field => field.bold).length;
    element('calculation-targets').textContent = targetCount
      ? `${targetCount} 个加粗项目需核算，其余项目可作为依据。`
      : '未标记核算项目；本次仅核价和检查收费矛盾。';
    const warnings = element('import-warnings');
    warnings.replaceChildren(...response.warnings.map(message => textElement('p', message)));
    warnings.hidden = !response.warnings.length;
    panel.hidden = false;
    showMessage('upload-status', '表格已读取，可在下方核对。');
  } catch (error) {
    if (requestGeneration !== generation) return;
    showMessage('upload-status', '');
    showMessage('upload-error', error instanceof APIError ? error.message : '无法连接服务，请确认服务已启动后重试。');
  }
}

function renderAnalysis(result: AnalysisResult) {
  const reportButton = textElement('button', '坑人', 'choose-file');
  reportButton.type = 'button';
  reportButton.addEventListener('click', () => {
    window.alert('已经记录坑人中介');
  });
  const actions = textElement('div', '', 'analysis-action');
  actions.append(reportButton);
  analysisContent.replaceChildren(renderReport(result.text), actions);
}

fileInput.addEventListener('change', () => {
  const file = fileInput.files?.[0];
  if (file) void upload(file);
});

for (const event of ['dragenter', 'dragover']) {
  dropZone.addEventListener(event, e => { e.preventDefault(); dropZone.classList.add('drag-over'); });
}
dropZone.addEventListener('dragleave', e => {
  if (!(e.relatedTarget instanceof Node) || !dropZone.contains(e.relatedTarget)) dropZone.classList.remove('drag-over');
});
dropZone.addEventListener('drop', e => {
  e.preventDefault();
  dropZone.classList.remove('drag-over');
  const files = e.dataTransfer?.files;
  if (!files || files.length !== 1) {
    clearDocument();
    showMessage('upload-error', '每次请选择一份 Excel 文件。');
    return;
  }
  void upload(files[0]!);
});

element('clear-button').addEventListener('click', () => { clearDocument(); fileInput.focus(); });
analyzeButton.addEventListener('click', async () => {
  if (!currentDocument) return;
  const payload = JSON.stringify(currentDocument, null, 2);
  const requestGeneration = generation;
  const saveGeneration = ++persistenceGeneration;
  showMessage('persistence-status', '');
  activeRequest?.abort();
  activeRequest = new AbortController();
  analyzeButton.disabled = true;
  analyzeButton.textContent = 'Codex 正在分析…';
  element('cancel-analysis').hidden = false;
  analysisPanel.hidden = false;
  analysisContent.replaceChildren(textElement('p', '正在联网核对报价并检查标记项目，通常需要几分钟。可随时取消。', 'quiet'));
  try {
    const result = await analyze(payload, activeRequest.signal, receipt => {
      if (saveGeneration !== persistenceGeneration) return;
      if (receipt.status === 'failed') showMessage('persistence-status', '本次后台保存任务未创建，分析报告仍可正常生成。');
      else if (receipt.taskId) void trackPersistence(receipt.taskId, saveGeneration);
    });
    if (requestGeneration === generation) renderAnalysis(result);
  } catch (error) {
    if (requestGeneration !== generation) return;
    analysisContent.replaceChildren(textElement('p', error instanceof APIError ? error.message : '无法连接分析服务，请稍后重试。', 'message error'));
  } finally {
    if (requestGeneration === generation) {
      analyzeButton.disabled = false;
      analyzeButton.textContent = '发送并分析 →';
      element('cancel-analysis').hidden = true;
    }
  }
});

element('cancel-analysis').addEventListener('click', () => {
  generation++;
  activeRequest?.abort();
  activeRequest = null;
  analyzeButton.disabled = false;
  analyzeButton.textContent = '发送并分析 →';
  element('cancel-analysis').hidden = true;
  analysisContent.replaceChildren(textElement('p', '已取消本次分析。', 'quiet'));
});
