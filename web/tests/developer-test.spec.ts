import { expect, test, type Page } from '@playwright/test';
import { readFile } from 'node:fs/promises';
import { file } from './xlsx';
import type { RentalDocument } from '../src/types';

const fixturePattern = /\/assets\/rental-test-[^/]+\.xlsx$/;
const fixtureBytes = await readFile(new URL('../src/fixtures/rental-test.xlsx', import.meta.url));
const connected = { connected: true, provider: 'mimo', name: 'MiMo v2.6-Flash' };

async function ready(page: Page) {
  await page.goto('/');
  await expect(page.locator('#account-status')).toContainText('已配置');
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('house-rent:developer-mode', 'true'));
  await page.route('**/api/ai/config', route => route.fulfill({ json: connected }));
});

test('普通模式不显示测试入口，切换模式后立即显示并支持刷新恢复', async ({ page }) => {
  await ready(page);
  await page.getByRole('button', { name: '设置', exact: true }).click();
  await page.getByRole('button', { name: '版本号 1.0.0' }).dblclick();
  await page.getByRole('button', { name: '关闭设置' }).click();
  await expect(page.locator('#developer-panel')).toBeHidden();
  let requests = 0;
  page.on('request', request => { if (request.url().endsWith('/api/import') || request.url().endsWith('/api/analyze')) requests++; });
  await page.locator('#developer-test-button').dispatchEvent('click');
  expect(requests).toBe(0);
  await page.getByRole('button', { name: '设置', exact: true }).click();
  await page.getByRole('button', { name: '版本号 1.0.0' }).dblclick();
  await page.getByRole('button', { name: '关闭设置' }).click();
  await expect(page.getByRole('button', { name: '一键测试', exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByRole('button', { name: '一键测试', exact: true })).toBeVisible();
});

test('一键原样导入内置 Excel 并自动分析，运行中禁止重复提交', async ({ page }) => {
  let finishAnalysis = () => {};
  const gate = new Promise<void>(resolve => { finishAnalysis = resolve; });
  let sent: RentalDocument | undefined;
  let calls = 0;
  await page.route('**/api/analyze', async route => {
    calls++;
    sent = route.request().postDataJSON();
    await gate;
    await route.fulfill({ json: { text: '这是测试接口返回的完整分析结果。' } });
  });
  await ready(page);
  const uploadRequest = page.waitForRequest('**/api/import');
  const imported = page.waitForResponse('**/api/import');
  await page.getByRole('button', { name: '一键测试', exact: true }).click();
  const multipart = (await uploadRequest).postDataBuffer()!;
  const contentStart = multipart.indexOf('\r\n\r\n') + 4;
  const contentEnd = multipart.lastIndexOf('\r\n--');
  expect(multipart.subarray(contentStart, contentEnd).equals(fixtureBytes)).toBe(true);
  const result = await (await imported).json();
  await expect(page.locator('#developer-test-status')).toContainText('正在等待 AI 分析结果');
  await expect.poll(() => calls).toBe(1);
  expect(sent).toEqual(result.document);
  expect(sent?.fileName).toBe('租房信息测试.xlsx');
  expect(sent?.fields).toHaveLength(15);
  expect(sent?.fields.find(field => field.label === '房租多少钱一个月')?.value).toBe('1500');
  expect(sent?.fields.find(field => field.label === '首月总共要付多少')).toMatchObject({ value: '6200', bold: true });
  expect(sent?.fields.filter(field => field.bold).map(field => field.label)).toEqual(['首月总共要付多少']);
  await expect(page.locator('#developer-test-button')).toBeDisabled();
  await expect(page.locator('#analyze-button')).toBeDisabled();
  await page.locator('#developer-test-button').dispatchEvent('click');
  await page.locator('#analyze-button').dispatchEvent('click');
  expect(calls).toBe(1);
  finishAnalysis();
  await expect(page.locator('.review-report')).toHaveText('这是测试接口返回的完整分析结果。');
  await expect(page.locator('#developer-test-status')).toContainText('测试完成');
  await expect(page.locator('#developer-test-button')).toBeEnabled();
  await expect(page.getByRole('button', { name: '取消测试', exact: true })).toBeHidden();
  await page.locator('#developer-panel').screenshot({ path: 'test-results/developer-test-desktop.png' });
});

const failures = [
  { name: '未配置 AI', path: '**/api/ai/config', response: { json: { connected: false } }, message: '请先在上方配置并连接 AI', analyzeCalls: 0 },
  { name: '旧配置需要重新连接', path: '**/api/ai/config', response: { json: { connected: true, needsReconnect: true } }, message: '请先在上方配置并连接 AI', analyzeCalls: 0 },
  { name: '测试文件不可用', path: fixturePattern, response: { status: 404, body: 'missing' }, message: '测试文件加载失败', analyzeCalls: 0 },
  { name: '导入被拒绝', path: '**/api/import', response: { status: 422, json: { error: { message: '测试导入失败' } } }, message: '表格导入失败', analyzeCalls: 0 },
  { name: '分析返回错误', path: '**/api/analyze', response: { status: 502, json: { error: { message: '测试模型不可用' } } }, message: 'AI 分析失败', analyzeCalls: 1 },
] as const;

for (const failure of failures) {
  test(`${failure.name}时中断流程，修复后可重新一键测试`, async ({ page }) => {
    await ready(page);
    const handler = (route: import('@playwright/test').Route) => route.fulfill(failure.response);
    await page.route(failure.path, handler);
    let calls = 0;
    page.on('request', request => { if (request.url().endsWith('/api/analyze')) calls++; });
    await page.getByRole('button', { name: '一键测试', exact: true }).click();
    await expect(page.locator('#developer-test-status')).toContainText(failure.message);
    await expect(page.locator('#developer-test-button')).toBeEnabled();
    expect(calls).toBe(failure.analyzeCalls);
    if (failure.name === '导入被拒绝') await expect(page.locator('#upload-error')).toHaveText('测试导入失败');
    if (failure.name === '分析返回错误') await expect(page.locator('#analysis-content')).toHaveText('测试模型不可用');
    await page.unroute(failure.path, handler);
    await page.route('**/api/analyze', route => route.fulfill({ json: { text: '重试完成' } }));
    await page.getByRole('button', { name: '一键测试', exact: true }).click();
    await expect(page.locator('#developer-test-status')).toContainText('测试完成');
    await expect(page.locator('.review-report')).toHaveText('重试完成');
  });
}

for (const stage of [
  { name: '配置检查', path: '**/api/ai/config', response: { json: connected } },
  { name: '加载文件', path: fixturePattern, response: { body: fixtureBytes } },
  { name: '导入表格', path: '**/api/import', response: { json: { document: { schemaVersion: 1, fileName: '旧表格.xlsx', fields: [] }, warnings: [] } } },
  { name: 'AI 分析', path: '**/api/analyze', response: { json: { text: '不应展示的旧回复' } } },
] as const) {
  test(`${stage.name}阶段可取消，旧请求不会继续执行或覆盖重试结果`, async ({ page }) => {
    await ready(page);
    let release = () => {};
    const gate = new Promise<void>(resolve => { release = resolve; });
    let waiting = false;
    let released = false;
    const handler = async (route: import('@playwright/test').Route) => {
      waiting = true;
      await gate;
      await route.fulfill(stage.response).catch(() => {});
      released = true;
    };
    await page.route(stage.path, handler);
    let calls = 0;
    page.on('request', request => { if (request.url().endsWith('/api/analyze')) calls++; });
    await page.getByRole('button', { name: '一键测试', exact: true }).click();
    await expect.poll(() => waiting).toBe(true);
    await page.getByRole('button', { name: '取消测试', exact: true }).click();
    await expect(page.locator('#developer-test-status')).toContainText('已取消本次测试');
    await expect(page.locator('#developer-test-button')).toBeEnabled();
    await page.unroute(stage.path, handler);
    await page.route('**/api/analyze', route => route.fulfill({ json: { text: '新的测试结果' } }));
    await page.getByRole('button', { name: '一键测试', exact: true }).click();
    await expect(page.locator('#developer-test-status')).toContainText('测试完成');
    release();
    await expect.poll(() => released).toBe(true);
    await expect(page.locator('.review-report')).toHaveText('新的测试结果');
    await expect(page.locator('#file-name')).toHaveText('租房信息测试.xlsx');
    expect(calls).toBe(stage.name === 'AI 分析' ? 2 : 1);
  });
}

for (const action of ['手动换文件', '清空', '关闭开发者模式']) {
  test(`${action}会停止正在进行的一键测试`, async ({ page }) => {
    await ready(page);
    let release = () => {};
    const gate = new Promise<void>(resolve => { release = resolve; });
    let returned = false;
    await page.route('**/api/analyze', async route => {
      await gate;
      await route.fulfill({ json: { text: '不应展示的旧回复' } }).catch(() => {});
      returned = true;
    });
    const analysis = page.waitForRequest('**/api/analyze');
    await page.getByRole('button', { name: '一键测试', exact: true }).click();
    await analysis;
    if (action === '手动换文件') {
      await page.getByLabel('选择 Excel 文件').setInputFiles(file([['新房源', '2000']], '新文件.xlsx'));
      await expect(page.locator('#file-name')).toHaveText('新文件.xlsx');
    } else if (action === '清空') {
      await page.getByRole('button', { name: '清空', exact: true }).click();
      await expect(page.locator('#document-panel')).toBeHidden();
    } else {
      await page.getByRole('button', { name: '设置', exact: true }).click();
      await page.getByRole('button', { name: '版本号 1.0.0' }).dblclick();
      await page.getByRole('button', { name: '关闭设置' }).click();
      await expect(page.locator('#developer-panel')).toBeHidden();
      await expect(page.locator('#analysis-content')).toContainText('已取消本次分析');
    }
    release();
    await expect.poll(() => returned).toBe(true);
    await expect(page.locator('.review-report')).toHaveCount(0);
    await expect(page.getByRole('button', { name: '取消测试', exact: true })).toBeHidden();
    await expect(page.locator('#developer-test-button')).toBeEnabled();
  });
}
