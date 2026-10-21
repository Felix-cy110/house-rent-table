import { expect, test } from '@playwright/test';
import { readFile } from 'node:fs/promises';
import { file } from './xlsx';
import { promptTuningTurn } from './turn-log-fixture';

test('默认精简版可切换全量原文，复制下载跟随选择，返回后保留版本', async ({ page }) => {
  const turnJson = promptTuningTurn();
  let calls = 0;
  await page.route('**/api/analyze', route => {
    calls++;
    return route.fulfill({ json: { text: '合计 8000.00 元。', turnJson } });
  });
  await page.goto('/');
  await page.evaluate(() => {
    Object.defineProperty(navigator.clipboard, 'writeText', { value: async (text: string) => { (window as unknown as { copiedJSON: string }).copiedJSON = text; } });
  });
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '2000']]));
  await page.getByRole('button', { name: '发送并分析' }).click();
  await page.getByRole('link', { name: '查看日志', exact: true }).click();
  const url = page.url();
  const concise = page.getByRole('button', { name: '精简版', exact: true });
  const full = page.getByRole('button', { name: '全量版', exact: true });
  await expect(concise).toHaveAttribute('aria-pressed', 'true');
  await expect(full).toHaveAttribute('aria-pressed', 'false');
  await expect(page.locator('.turn-mode-hint')).toContainText('适合调提示词');
  const conciseJSON = (await page.locator('#turn-json').textContent())!;
  expect(conciseJSON.length).toBeLessThan(turnJson.length);
  expect(JSON.parse(conciseJSON).prompt.messages[0].content).toContain('请核算费用');
  await expect(page.locator('#turn-log-view script')).toHaveCount(0);
  expect(await page.evaluate(() => 'hacked' in window)).toBe(false);
  await page.getByRole('button', { name: '自动换行', exact: true }).click();
  for (const [mode, name, expected] of [['concise', '精简版', conciseJSON], ['full', '全量版', turnJson]]) {
    await page.getByRole('button', { name: name!, exact: true }).click();
    await expect(page).toHaveURL(url);
    expect(await page.locator('#turn-json').textContent()).toBe(expected);
    await expect(page.getByRole('button', { name: '自动换行', exact: true })).toHaveAttribute('aria-pressed', 'true');
    await expect(page.locator('.turn-line-numbers')).toBeHidden();
    await expect(page.locator('.turn-code-footer')).toContainText(`${expected!.split('\n').length.toLocaleString('zh-CN')} 行`);
    await page.getByRole('button', { name: '复制 JSON', exact: true }).click();
    await expect(page.locator('.turn-action-status')).toHaveText(`${name} JSON 已复制。`);
    expect(await page.evaluate(() => (window as unknown as { copiedJSON: string }).copiedJSON)).toBe(expected);
    const downloaded = page.waitForEvent('download');
    await page.getByRole('button', { name: '下载 JSON', exact: true }).click();
    const download = await downloaded;
    expect(download.suggestedFilename()).toMatch(new RegExp(`-${mode}\\.json$`));
    expect(await readFile((await download.path())!, 'utf8')).toBe(expected);
  }
  await page.getByRole('button', { name: '返回报告' }).click();
  await expect(page.locator('.review-report')).toHaveText('合计 8000.00 元。');
  await page.goForward();
  await expect(full).toHaveAttribute('aria-pressed', 'true');
  expect(await page.locator('#turn-json').textContent()).toBe(turnJson);
  await concise.click();
  expect(await page.locator('#turn-json').textContent()).toBe(conciseJSON);
  expect(calls).toBe(1);
  expect(page.context().pages()).toHaveLength(1);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(concise).toBeVisible();
  await expect(full).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test('日志在当前页面更新 URL，保留 JSON 精度、样式及原报告，支持返回和前进', async ({ page }) => {
  const turnJson = '{\n  "id": 9007199254740993123,\n  "message": "</pre><script>window.hacked=true</script><img src=https://example.com/track>",\n  "reasoning_content": "供应商返回的思考 🏠",\n  "usage": null\n}';
  let finishAnalysis: () => void = () => {};
  await page.route('**/api/analyze', async route => {
    await new Promise<void>(resolve => { finishAnalysis = resolve; });
    await route.fulfill({ json: { text: '费用已核对。', turnJson } });
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  const logLink = page.getByRole('link', { name: '查看日志', exact: true });
  await expect(logLink).toHaveCount(0);
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '2000']]));
  const started = page.waitForRequest('**/api/analyze');
  await page.getByRole('button', { name: '发送并分析' }).click();
  await started;
  await expect(logLink).toHaveCount(0);
  finishAnalysis();
  await expect(logLink).toBeVisible();
  await expect(logLink).not.toHaveAttribute('target', '_blank');
  await page.evaluate(() => { (window as unknown as { documentMarker: boolean }).documentMarker = true; });
  await logLink.click();
  const logURL = page.url();
  await expect(page).toHaveURL(/#\/logs\//);
  await expect(page).toHaveTitle('分析日志 · 租房信息检查');
  await expect(page.getByRole('heading', { name: '分析日志', exact: true })).toBeVisible();
  await expect(page.locator('#report-view')).toBeHidden();
  expect(await page.locator('#turn-json').textContent()).toBe(turnJson);
  await expect(page.locator('#turn-log-view script, #turn-log-view img')).toHaveCount(0);
  await expect(page.locator('#turn-json .json-key')).toHaveCount(4);
  await expect(page.locator('#turn-json .json-number')).toHaveText('9007199254740993123');
  expect(await page.evaluate(() => ({ hacked: 'hacked' in window, retained: 'documentMarker' in window }))).toEqual({ hacked: false, retained: true });
  expect(page.context().pages()).toHaveLength(1);
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const wrap = page.getByRole('button', { name: '自动换行', exact: true });
  await wrap.click();
  await expect(wrap).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('.turn-line-numbers')).toBeHidden();
  expect(await page.locator('#turn-json').textContent()).toBe(turnJson);
  expect(await page.locator('.turn-code-scroll').evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
  await wrap.click();
  await expect(wrap).toHaveAttribute('aria-pressed', 'false');
  await page.getByRole('button', { name: '返回报告' }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.locator('.review-report')).toBeVisible();
  await expect(page.locator('.review-report')).toHaveText('费用已核对。');
  await expect(page.getByRole('cell', { name: '2000', exact: true })).toBeVisible();
  await expect(logLink).toBeFocused();
  await page.goForward();
  await expect(page).toHaveURL(logURL);
  expect(await page.locator('#turn-json').textContent()).toBe(turnJson);
  await page.goBack();
  await expect(page.locator('.review-report')).toBeVisible();
  await page.getByRole('button', { name: '清空', exact: true }).click();
  await expect(logLink).toHaveCount(0);
  await page.goForward();
  await expect(page.getByRole('heading', { name: '日志已失效' })).toBeVisible();
  await expect(page.locator('#turn-json')).toHaveCount(0);
});

test('重复分析对应各自日志，失败或旧版响应不会留下上一次入口', async ({ page }) => {
  let count = 0;
  await page.route('**/api/analyze', route => {
    count++;
    if (count === 3) return route.fulfill({ status: 502, json: { error: { code: 'ai_failed', message: '分析失败' } } });
    return route.fulfill({ json: { text: `报告 ${count}`, ...(count < 3 ? { turnJson: `{\n  "turn": ${count}\n}` } : {}) } });
  });
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '2000']]));
  const send = page.getByRole('button', { name: '发送并分析' });
  const link = page.getByRole('link', { name: '查看日志', exact: true });
  let oldHash = '';
  for (let turn = 1; turn <= 2; turn++) {
    await send.click();
    await expect(page.locator('.review-report')).toHaveText(`报告 ${turn}`);
    await link.click();
    await expect(page).toHaveURL(/#\/logs\//);
    expect(JSON.parse((await page.locator('#turn-json').textContent())!)).toEqual({ turn });
    if (turn === 1) oldHash = new URL(page.url()).hash;
    else {
      expect(new URL(page.url()).hash).not.toBe(oldHash);
      await page.evaluate(hash => { location.hash = hash; }, oldHash);
      await expect(page.getByRole('heading', { name: '日志已失效' })).toBeVisible();
      await page.goBack();
      await expect(page.locator('#turn-json')).toContainText('2');
    }
    await page.getByRole('button', { name: '返回报告' }).click();
    await expect(page.locator('#report-view')).toBeVisible();
  }
  await send.click();
  await expect(page.locator('#analysis-content')).toContainText('分析失败');
  await expect(link).toHaveCount(0);
  await send.click();
  await expect(page.locator('.review-report')).toHaveText('报告 4');
  await expect(link).toHaveCount(0);
});

test('摘要、复制与下载保留完整原文，复制失败给出提示', async ({ page, context }) => {
  const turnJson = '{\n  "model": "mimo-v2.6-flash",\n  "durationMs": 12500,\n  "events": [{"kind":"model"},{"kind":"tool"},{"kind":"search"},{"kind":"model"}],\n  "id": 9007199254740993123,\n  "text": "中文 🏠"\n}';
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.route('**/api/analyze', route => route.fulfill({ json: { text: '费用已核对', turnJson } }));
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '2000']]));
  await page.getByRole('button', { name: '发送并分析' }).click();
  await page.getByRole('link', { name: '查看日志', exact: true }).click();
  await expect(page.locator('.turn-stats')).toHaveText('分析模型mimo-v2.6-flash模型请求2 轮工具调用1 次总耗时12.5 秒');
  await page.getByRole('button', { name: '复制 JSON', exact: true }).click();
  await expect(page.getByRole('status').filter({ hasText: '全量版 JSON 已复制。' })).toBeVisible();
  // Windows clipboard normalizes line endings to CRLF.
  expect((await page.evaluate(() => navigator.clipboard.readText())).replace(/\r\n/g, '\n')).toBe(turnJson);
  const downloaded = page.waitForEvent('download');
  await page.getByRole('button', { name: '下载 JSON', exact: true }).click();
  const download = await downloaded;
  expect(download.suggestedFilename()).toMatch(/^turn-[\w-]+\.json$/);
  expect(await readFile((await download.path())!, 'utf8')).toBe(turnJson);
  await page.evaluate(() => {
    Object.defineProperty(navigator.clipboard, 'writeText', { value: () => Promise.reject(new Error('denied')) });
  });
  await page.getByRole('button', { name: '复制 JSON', exact: true }).click();
  await expect(page.locator('.turn-action-status')).toContainText('复制失败');
});

test('刷新或直接打开日志地址显示失效提示，返回按钮仍可使用', async ({ page }) => {
  await page.route('**/api/analyze', route => route.fulfill({ json: { text: '报告', turnJson: '{"text":"原始日志"}' } }));
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '2000']]));
  await page.getByRole('button', { name: '发送并分析' }).click();
  await page.getByRole('link', { name: '查看日志', exact: true }).click();
  await page.reload();
  await expect(page.getByRole('heading', { name: '日志已失效' })).toBeVisible();
  await expect(page.locator('#turn-json')).toHaveCount(0);
  await page.getByRole('button', { name: '返回报告' }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole('heading', { name: '先把租房信息看清楚。' })).toBeVisible();
  await page.goto('/#/logs/not-existing');
  await expect(page.getByRole('heading', { name: '日志已失效' })).toBeVisible();
  await page.getByRole('button', { name: '返回报告' }).click();
  await expect(page).toHaveURL(/\/$/);
});

test('超大日志仍能完整显示与下载，未知或损坏字段不会阻止返回', async ({ page }) => {
  let turnJson = `{"message":"${'大'.repeat(2_000_001)}","end":"完整结尾"}`;
  await page.route('**/api/analyze', route => route.fulfill({ json: { text: '报告', turnJson } }));
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '2000']]));
  for (const json of [turnJson, '{"model":null,"events":[null,{},123],"durationMs":-1}', '{invalid JSON']) {
    turnJson = json;
    await page.getByRole('button', { name: '发送并分析' }).click();
    await page.getByRole('link', { name: '查看日志', exact: true }).click();
    expect(await page.locator('#turn-json').textContent()).toBe(json);
    await expect(page.getByRole('button', { name: '精简版', exact: true })).toBeDisabled();
    await expect(page.getByRole('button', { name: '全量版', exact: true })).toHaveAttribute('aria-pressed', 'true');
    await expect(page.locator('.turn-mode-hint')).toContainText('暂不支持精简');
    if (json.length > 2_000_000) {
      const downloaded = page.waitForEvent('download');
      await page.getByRole('button', { name: '下载 JSON', exact: true }).click();
      expect(await readFile((await (await downloaded).path())!, 'utf8')).toBe(json);
    }
    await page.getByRole('button', { name: '返回报告' }).click();
    await expect(page.locator('.review-report')).toBeVisible();
  }
});
