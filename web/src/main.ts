import './style.css';
import { analyze, APIError, importExcel } from './api';
import type { AnalysisResult, RentalDocument, RentalField } from './types';

document.querySelector<HTMLDivElement>('#app')!.innerHTML = `
  <header class="page-header">
    <div class="wordmark"><span class="brand-icon" aria-hidden="true">⌂</span> 租房检查</div>
    <a class="template-link" href="/api/template" download>下载空白模板 <span aria-hidden="true">↗</span></a>
  </header>
  <section class="intro">
    <h1>先把租房信息看清楚。</h1>
    <p>上传中介填写的 Excel，核对房源和费用，再开始检查。</p>
  </section>
  <section class="upload-panel" aria-labelledby="upload-title">
    <div class="section-heading"><h2 id="upload-title"><span class="step">01</span> 上传表格</h2><span class="quiet">一份表格，一套房源</span></div>
    <div id="drop-zone" class="drop-zone">
      <svg class="upload-icon" viewBox="0 0 32 32" fill="none" aria-hidden="true"><path d="M10 3h9l6 6v19H7V3h3Z" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/><path d="M19 3v7h6M16 23V14m-4 4 4-4 4 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
      <p class="drop-title">把填写好的 Excel 拖到这里</p>
      <p class="drop-detail">.xlsx 格式，最大 10 MB</p>
      <label class="choose-file" for="excel-file">选择 Excel 文件</label>
      <input class="sr-only" id="excel-file" type="file" accept=".xlsx,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" />
    </div>
    <p class="format-hint">A 列填写项目，B 列填写内容。可以自由增减项目。</p>
    <p id="upload-status" class="message" role="status" aria-live="polite" hidden></p>
    <p id="upload-error" class="message error" role="alert" hidden></p>
  </section>
  <section id="document-panel" class="document-panel" aria-labelledby="document-title" hidden>
    <div class="section-heading">
      <h2 id="document-title"><span class="step">02</span> 核对信息</h2>
      <button id="clear-button" class="text-button" type="button">清空</button>
    </div>
    <div class="file-heading"><p id="file-name"></p><span id="field-count" class="quiet"></span></div>
    <div id="import-warnings" class="import-warnings" hidden></div>
    <div class="table-wrap"><table><caption class="sr-only">Excel 识别内容</caption><thead><tr><th scope="col">项目</th><th scope="col">填写内容</th></tr></thead><tbody id="field-list"></tbody></table></div>
    <div class="analysis-action"><p>请先核对识别内容。空白项目会保留为“未填写”。</p><button id="analyze-button" class="primary-button" type="button">开始检查 <span aria-hidden="true">→</span></button></div>
  </section>
  <section id="analysis-panel" class="analysis-panel" aria-labelledby="analysis-title" aria-live="polite" hidden>
    <h2 id="analysis-title"><span class="step">03</span> 检查结果</h2>
    <div id="analysis-content"></div>
  </section>
  <footer><span class="status-dot" aria-hidden="true"></span>当前支持表格导入与核对，风险分析暂未开放。<br /><span class="session-note">填写内容仅用于本次会话，刷新页面后清空。</span></footer>
`;

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

function showMessage(id: string, message: string) {
  const node = element(id);
  node.textContent = message;
  node.hidden = !message;
}

function clearDocument() {
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
  analyzeButton.textContent = '开始检查 →';
}

function renderFields(fields: RentalField[]) {
  const fragment = document.createDocumentFragment();
  for (const field of fields) {
    const row = document.createElement('tr');
    row.id = field.id;
    const label = document.createElement('th');
    label.scope = 'row';
    label.append(textElement('span', field.label), textElement('small', `${field.sheet} · ${field.cell}`, 'cell-source'));
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
    element('file-name').textContent = currentDocument.fileName;
    const blankCount = currentDocument.fields.filter(field => field.value === null).length;
    element('field-count').textContent = `${currentDocument.fields.length} 个项目${blankCount ? `，${blankCount} 项未填写` : ''}`;
    renderFields(currentDocument.fields);
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

function renderAnalysis(result: AnalysisResult, doc: RentalDocument) {
  analysisContent.replaceChildren(textElement('p', result.summary, 'result-summary'));
  const severityNames = { high: '重点核实', medium: '建议确认', low: '留意事项' };
  for (const finding of result.findings) {
    const card = textElement('article', '', 'finding');
    card.append(textElement('span', severityNames[finding.severity], `severity ${finding.severity}`), textElement('h3', finding.title), textElement('p', finding.description));
    const evidence = doc.fields.filter(field => finding.evidenceFieldIds.includes(field.id));
    if (evidence.length) card.append(textElement('p', `依据：${evidence.map(field => `${field.label}（${field.sheet}!${field.cell}）`).join('、')}`, 'quiet'));
    if (finding.followUp) card.append(textElement('p', `向中介确认：${finding.followUp}`));
    analysisContent.append(card);
  }
  if (result.missingInformation.length) {
    const missing = textElement('div', '', 'missing-information');
    missing.append(textElement('h3', '还需要确认的信息'));
    for (const item of result.missingInformation) missing.append(textElement('p', `${item.label}：${item.reason}`));
    analysisContent.append(missing);
  }
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
  const doc = currentDocument;
  const requestGeneration = generation;
  activeRequest?.abort();
  activeRequest = new AbortController();
  analyzeButton.disabled = true;
  analyzeButton.textContent = '正在检查…';
  analysisPanel.hidden = false;
  analysisContent.replaceChildren(textElement('p', '正在提交检查…', 'quiet'));
  try {
    const result = await analyze(doc, activeRequest.signal);
    if (requestGeneration === generation) renderAnalysis(result, doc);
  } catch (error) {
    if (requestGeneration !== generation) return;
    if (error instanceof APIError && error.code === 'analysis_not_configured') {
      const notice = textElement('div', '', 'unavailable');
      notice.append(textElement('h3', '表格已就绪，分析暂未开放'), textElement('p', '你可以核对上方信息。当前没有生成风险判断。'));
      analysisContent.replaceChildren(notice);
    } else {
      analysisContent.replaceChildren(textElement('p', error instanceof APIError ? error.message : '无法连接分析服务，请稍后重试。', 'message error'));
    }
  } finally {
    if (requestGeneration === generation) {
      analyzeButton.disabled = false;
      analyzeButton.textContent = '开始检查 →';
    }
  }
});
