import { expect, test } from '@playwright/test';
import { file } from './xlsx';
import type { RentalDocument } from '../src/types';

test('加粗项目作为目标展示，完整布尔标记与原文一起发送，取消加粗后清除标记', async ({ page }) => {
  const submitted: string[] = [];
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected: true, model: 'test-model', baseUrl: 'https://example.com/v1' } }));
  await page.route('**/api/analyze', route => {
    submitted.push(route.request().postData()!);
    return route.fulfill({ json: { text: '本测试仅验证加粗标记传递。' } });
  });
  await page.goto('/');
  const rows = [
    ['说明', '数值'], ['月租', '3000'], ['押付方式', '押一付三'],
    ['首月总共要付多少', '12000'], ['将来新增的核算项', null], ['将来新增的核算项', '0'],
  ];
  const imported = page.waitForResponse('**/api/import');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file(rows, '标记.xlsx', ['A1', 'A4', 'A5', 'B2']));
  const { document } = await (await imported).json() as { document: RentalDocument };
  expect(document.fields.map(field => field.bold)).toEqual([false, false, true, true, false]);
  expect(document.fields[3]?.value).toBeNull();
  expect(document.fields[4]?.value).toBe('0');
  await expect(page.locator('#calculation-targets')).toHaveText('2 个加粗项目需核算，其余项目可作为依据。');
  await expect(page.locator('#field-list strong')).toHaveText(['首月总共要付多少', '将来新增的核算项']);
  await expect(page.locator('#field-list .calculation-marker')).toHaveCount(2);
  await page.getByText('查看将发送的完整内容', { exact: true }).click();
  const preview = await page.locator('#payload-content').textContent();
  await page.getByRole('button', { name: '发送并分析' }).click();
  await expect(page.locator('.review-report')).toBeVisible();
  expect(submitted[0]).toBe(preview);
  expect(JSON.parse(submitted[0]!)).toEqual(document);
  await page.screenshot({ path: 'test-results/bold-targets.png', fullPage: true });

  // Same names and values, only the formatting changes. A bold header/value
  // cannot keep a removed calculation target selected.
  await page.getByLabel('选择 Excel 文件').setInputFiles(file(rows, '标记.xlsx', ['A1', 'B2']));
  await expect(page.locator('#calculation-targets')).toContainText('未标记核算项目');
  await expect(page.locator('#field-list strong')).toHaveCount(0);
  await expect(page.locator('#analysis-panel')).toBeHidden();
  await page.getByRole('button', { name: '发送并分析' }).click();
  await expect.poll(() => submitted.length).toBe(2);
  expect((JSON.parse(submitted[1]!) as RentalDocument).fields.every(field => field.bold === false)).toBe(true);
});
