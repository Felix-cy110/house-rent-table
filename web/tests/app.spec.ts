import { expect, test } from '@playwright/test';
import { file } from './xlsx';

test.beforeEach(async ({ page }) => {
  await page.route('**/api/codex/account', route => route.fulfill({ json: { loggedIn: false, pending: false } }));
});

test('导入动态项目、区分空白与零、未登录时给出明确提示', async ({ page }) => {
  await page.route('**/api/analyze', route => route.fulfill({ status: 401, json: { error: { code: 'codex_login_required', message: '请先登录 Codex，再发送并分析' } } }));
  await page.goto('/');
  await page.screenshot({ path: 'test-results/desktop-empty.png', fullPage: true });
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([
    [null, '数值'], ['小区/地点', '示例小区'], ['月租', '2800 元/月'],
    ['押几付几', '押一付三'], ['新增停车费', '0'], ['燃气费', null], ['月租', '另一个报价'],
  ]));
  await expect(page.getByText('6 个项目，1 项未填写')).toBeVisible();
  await expect(page.getByRole('cell', { name: '0', exact: true })).toBeVisible();
  await expect(page.getByRole('cell', { name: '未填写', exact: true })).toHaveCount(1);
  await expect(page.getByText('“月租”出现多次，已按原表分别保留，请核对。')).toBeVisible();
  await expect(page.getByText('Sheet1 · B5', { exact: true })).toBeVisible();
  const response = page.waitForResponse('**/api/analyze');
  await page.getByRole('button', { name: '发送并分析' }).click();
  expect((await response).status()).toBe(401);
  await expect(page.getByText('请先登录 Codex，再发送并分析', { exact: true })).toBeVisible();
  await expect(page.locator('.codex-response')).toHaveCount(0);
  await page.screenshot({ path: 'test-results/desktop-imported.png', fullPage: true });
  await page.getByRole('button', { name: '清空', exact: true }).click();
  await expect(page.locator('#document-panel')).toBeHidden();
  await expect(page.locator('#analysis-panel')).toBeHidden();
});

test('同名文件重新上传可增删和重排字段，刷新不保留内容', async ({ page }) => {
  await page.goto('/');
  const input = page.getByLabel('选择 Excel 文件');
  await input.setInputFiles(file([['旧字段', '旧值'], ['月租', '2000']]));
  await expect(page.getByRole('cell', { name: '旧值', exact: true })).toBeVisible();
  await input.setInputFiles(file([['月租', '3000'], ['将来添加的任意字段', '新的值']]));
  await expect(page.getByRole('cell', { name: '新的值', exact: true })).toBeVisible();
  await expect(page.getByText('旧字段', { exact: true })).toHaveCount(0);
  await expect(page.locator('#field-list tr').first()).toContainText('月租');
  await page.reload();
  await expect(page.locator('#document-panel')).toBeHidden();
  expect(await page.evaluate(() => localStorage.length)).toBe(0);
});

test('说明表头下的小区地点按原顺序和来源显示', async ({ page }) => {
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([
    ['详细填写，不要含糊', '数值'], ['小区地点', '测试小区 2 栋'], ['中介姓名', null],
  ]));
  await expect(page.getByText('2 个项目，1 项未填写')).toBeVisible();
  const rows = page.locator('#field-list tr');
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText('小区地点');
  await expect(rows.nth(0)).toContainText('Sheet1 · B2');
  await expect(rows.nth(0).getByRole('cell')).toHaveText('测试小区 2 栋');
  await expect(rows.nth(1)).toContainText('中介姓名');
  await expect(rows.nth(1).getByRole('cell')).toHaveText('未填写');
});

test('错误文件或额外列给出提示并清空上一份结果', async ({ page }) => {
  await page.goto('/');
  const input = page.getByLabel('选择 Excel 文件');
  await input.setInputFiles(file([['月租', '2000']]));
  await expect(page.locator('#document-panel')).toBeVisible();
  await input.setInputFiles({ name: '损坏.xlsx', mimeType: 'application/octet-stream', buffer: Buffer.from('not xlsx') });
  await expect(page.getByRole('alert')).toContainText('无法读取 Excel');
  await expect(page.locator('#document-panel')).toBeHidden();
  await input.setInputFiles(file([['月租', '2000', '额外内容']]));
  await expect(page.getByRole('alert')).toContainText('B 列之后');
  await input.setInputFiles({ name: '旧格式.xls', mimeType: 'application/octet-stream', buffer: Buffer.from('xls') });
  await expect(page.getByRole('alert')).toContainText('请选择 .xlsx');
});

test('单元格内容按文本显示，窄屏可以阅读和操作', async ({ page }) => {
  await page.route('**/api/analyze', route => route.fulfill({ json: { text: '<img src=x onerror="window.hacked=true">\n这是测试回复。' } }));
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  const literal = '<img src=x onerror="window.hacked=true">';
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['自定义备注', literal], ['特殊值', '0']]));
  await expect(page.getByRole('cell', { name: literal, exact: true })).toBeVisible();
  expect(await page.evaluate(() => 'hacked' in window)).toBe(false);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.getByRole('button', { name: '发送并分析' }).click();
  await expect(page.locator('.codex-response')).toContainText('这是测试回复。');
  expect(await page.evaluate(() => 'hacked' in window)).toBe(false);
  await page.screenshot({ path: 'test-results/mobile-imported.png', fullPage: true });
});

test('切换文件时，较慢的旧请求不会覆盖新表格', async ({ page }) => {
  let call = 0;
  await page.route('**/api/import', async route => {
    call++;
    const first = call === 1;
    if (first) await new Promise(resolve => setTimeout(resolve, 400));
    await route.fulfill({ json: {
      document: { schemaVersion: 1, fileName: first ? '旧.xlsx' : '新.xlsx', fields: [{ id: 'field-1', label: first ? '旧字段' : '新字段', value: '1', sheet: 'Sheet1', cell: 'B1' }] }, warnings: [],
    } }).catch(() => { /* The previous fetch may have been aborted. */ });
  });
  await page.goto('/');
  const input = page.getByLabel('选择 Excel 文件');
  await input.setInputFiles(file([['旧字段', '1']], '旧.xlsx'));
  await expect.poll(() => call).toBe(1);
  await input.setInputFiles(file([['新字段', '1']], '新.xlsx'));
  await expect(page.getByText('新字段', { exact: true })).toBeVisible();
  await page.waitForTimeout(500);
  await expect(page.getByText('旧字段', { exact: true })).toHaveCount(0);
  await expect(page.locator('#file-name')).toHaveText('新.xlsx');
});

test('预览的完整 JSON 原样发送，Codex 原文回复保留空格与换行', async ({ page }) => {
  let sent = '';
  const answer = '  原始回复\n请确认费用。\n<script>window.hacked=true</script>';
  await page.route('**/api/analyze', route => {
    sent = route.request().postData()!;
    return route.fulfill({ json: { text: answer } });
  });
  await page.goto('/');
  const imported = page.waitForResponse('**/api/import');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['未来字段', '  原文\n0 <> &'], ['未来字段', null], ['零', '0']]));
  const { document } = await (await imported).json();
  await expect(page.locator('#document-panel')).toBeVisible();
  await page.getByText('查看将发送的完整内容', { exact: true }).click();
  const preview = await page.locator('#payload-content').textContent();
  await page.getByRole('button', { name: '发送并分析' }).click();
  await expect(page.locator('.codex-response')).toBeVisible();
  expect(sent).toBe(preview);
  expect(JSON.parse(sent)).toEqual(document);
  expect(await page.locator('.codex-response').textContent()).toBe(answer);
  expect(await page.evaluate(() => 'hacked' in window)).toBe(false);
});

test('空白模板下载后可重新上传，说明表头不作为业务字段', async ({ page }) => {
  await page.goto('/');
  const download = page.waitForEvent('download');
  await page.getByRole('link', { name: '下载空白模板' }).click();
  const downloaded = await download;
  expect(downloaded.suggestedFilename()).toBe('租房信息模板.xlsx');
  const stream = await downloaded.createReadStream();
  const chunks: Buffer[] = [];
  for await (const chunk of stream) chunks.push(Buffer.from(chunk));
  const imported = page.waitForResponse('**/api/import');
  await page.getByLabel('选择 Excel 文件').setInputFiles({
    name: downloaded.suggestedFilename(),
    mimeType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
    buffer: Buffer.concat(chunks),
  });
  const response = await imported;
  expect(response.status()).toBe(200);
  const { document } = await response.json();
  expect(document.fields.length).toBeGreaterThan(0);
  expect(document.fields.every((field: { cell: string; value: string | null }) => field.cell !== 'B1' && field.value === null)).toBe(true);
  await expect(page.getByText(`${document.fields.length} 个项目，${document.fields.length} 项未填写`)).toBeVisible();
  await expect(page.locator('#field-list tr')).toHaveCount(document.fields.length);
  await expect(page.getByRole('cell', { name: '未填写', exact: true })).toHaveCount(document.fields.length);
  const locationIndex = document.fields.findIndex((field: { label: string }) => field.label === '小区地点');
  expect(locationIndex).toBeGreaterThanOrEqual(0);
  expect(document.fields[locationIndex + 1]?.label).toBe('中介姓名');
  await expect(page.locator('#field-list tr').nth(locationIndex)).toContainText('小区地点');
  await expect(page.locator('#field-list tr').nth(locationIndex + 1)).toContainText('中介姓名');
});
