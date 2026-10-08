import { expect, test } from '@playwright/test';
import { file } from './xlsx';

test('导入动态项目、区分空白与零、明确提示未接入分析', async ({ page }) => {
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
  await page.getByRole('button', { name: '开始检查' }).click();
  expect((await response).status()).toBe(501);
  await expect(page.getByText('表格已就绪，分析暂未开放')).toBeVisible();
  await expect(page.locator('.finding')).toHaveCount(0);
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
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  const literal = '<img src=x onerror="window.hacked=true">';
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['自定义备注', literal], ['特殊值', '0']]));
  await expect(page.getByRole('cell', { name: literal, exact: true })).toBeVisible();
  expect(await page.evaluate(() => 'hacked' in window)).toBe(false);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.getByRole('button', { name: '开始检查' }).click();
  await expect(page.getByText('表格已就绪，分析暂未开放')).toBeVisible();
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

test('结果组件支持未来分析接口的风险证据和待确认事项', async ({ page }) => {
  await page.route('**/api/analyze', route => route.fulfill({ json: {
    summary: '此内容仅由浏览器测试注入。',
    findings: [{ id: 'test-finding', severity: 'medium', title: '测试风险项', description: '测试说明', evidenceFieldIds: ['field-1'], followUp: '测试追问' }],
    missingInformation: [{ label: '测试缺失项', reason: '测试原因' }],
  } }));
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '2000']]));
  await expect(page.locator('#document-panel')).toBeVisible();
  await page.getByRole('button', { name: '开始检查' }).click();
  await expect(page.getByText('测试风险项', { exact: true })).toBeVisible();
  await expect(page.getByText('依据：月租（Sheet1!B1）')).toBeVisible();
  await expect(page.getByText('测试缺失项：测试原因')).toBeVisible();
});

test('空白模板可通过页面下载', async ({ page }) => {
  await page.goto('/');
  const download = page.waitForEvent('download');
  await page.getByRole('link', { name: '下载空白模板' }).click();
  expect((await download).suggestedFilename()).toBe('租房信息模板.xlsx');
});
