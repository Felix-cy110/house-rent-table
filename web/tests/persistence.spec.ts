import { expect, test } from '@playwright/test';
import { file } from './xlsx';

test.beforeEach(async ({ page }) => {
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected: true, model: 'test-model', baseUrl: 'https://example.com/v1' } }));
  await page.route('**/api/persistence', route => route.fulfill({ json: { enabled: true } }));
});

test('报告独立返回，后台保存从重试切换到成功', async ({ page }) => {
  const id = '11111111-1111-4111-8111-111111111111';
  let checks = 0;
  await page.route('**/api/analyze', route => route.fulfill({
    headers: { 'X-Rental-Persistence': 'queued', 'X-Rental-Task-Id': id },
    json: { text: '原有分析报告完整保留' },
  }));
  await page.route(`**/api/persistence/tasks/${id}`, route => route.fulfill({ json: ++checks === 1
    ? { taskId: id, stage: 'PERSIST', status: 'RETRY_WAIT', message: 'MySQL 写入失败，已保留提取结果等待补写' }
    : { taskId: id, stage: 'PERSIST', status: 'SUCCEEDED' },
  }));
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '2500元/月']]));
  await expect(page.locator('#persistence-notice')).toBeVisible();
  await page.getByRole('button', { name: '发送并分析' }).click();
  await expect(page.locator('.ai-response')).toHaveText('原有分析报告完整保留');
  await expect(page.locator('#persistence-status')).toContainText('等待补写');
  await expect(page.locator('#persistence-status')).toHaveText('本次业务数据已保存。');
  await page.getByRole('button', { name: '清空', exact: true }).click();
  await expect(page.locator('#persistence-status')).toBeHidden();
});

test('入队失败明确提示，仍显示主分析报告', async ({ page }) => {
  await page.route('**/api/analyze', route => route.fulfill({ headers: { 'X-Rental-Persistence': 'failed' }, json: { text: '报告正常生成' } }));
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '2500元/月']]));
  await page.getByRole('button', { name: '发送并分析' }).click();
  await expect(page.locator('.ai-response')).toHaveText('报告正常生成');
  await expect(page.locator('#persistence-status')).toContainText('后台保存任务未创建');
});

test('主分析报错仍能查询独立后台任务', async ({ page }) => {
  const id = '22222222-2222-4222-8222-222222222222';
  await page.route('**/api/analyze', route => route.fulfill({ status: 502,
    headers: { 'X-Rental-Persistence': 'queued', 'X-Rental-Task-Id': id },
    json: { error: { code: 'ai_failed', message: '主分析暂时失败' } },
  }));
  await page.route(`**/api/persistence/tasks/${id}`, route => route.fulfill({ json: { taskId: id, stage: 'PERSIST', status: 'SUCCEEDED' } }));
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '2500元/月']]));
  await page.getByRole('button', { name: '发送并分析' }).click();
  await expect(page.locator('#analysis-content')).toContainText('主分析暂时失败');
  await expect(page.locator('#persistence-status')).toHaveText('本次业务数据已保存。');
});
