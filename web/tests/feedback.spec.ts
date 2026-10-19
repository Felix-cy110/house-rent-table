import { expect, test } from '@playwright/test';
import { file } from './xlsx';

test.beforeEach(async ({ page }) => {
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected: true, model: 'test-model', baseUrl: 'https://example.com/v1' } }));
  await page.route('**/api/analyze', route => route.fulfill({ json: { text: '测试分析结果' } }));
});

test('坑人按钮提交表格中的姓名，等待保存成功再提示，可继续累计反馈', async ({ page }) => {
  let calls = 0;
  let release!: () => void;
  const firstSave = new Promise<void>(resolve => { release = resolve; });
  await page.route('**/api/broker-feedback', async route => {
    expect(route.request().method()).toBe('POST');
    expect(route.request().postDataJSON()).toEqual({ brokerName: '张三' });
    calls++;
    if (calls === 1) await firstSave;
    await route.fulfill({ json: { brokerName: '张三', feedbackCount: calls } });
  });
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '3000'], ['中介姓名', '  张三　'], ['中介姓名', '张三']]));
  await page.getByRole('button', { name: '发送并分析' }).click();
  const button = page.getByRole('button', { name: '坑人', exact: true });
  await expect(button).toBeVisible();
  expect(calls).toBe(0);
  await button.click();
  const pending = page.getByRole('button', { name: '正在记录…' });
  await expect(pending).toBeDisabled();
  await expect(page.locator('#analysis-content')).toContainText('正在保存反馈…');
  await expect(page.locator('#analysis-content')).not.toContainText('已经记录');
  await pending.evaluate(node => (node as HTMLButtonElement).click());
  release();
  await expect(page.locator('#analysis-content')).toContainText('已经记录坑人中介：张三，累计反馈 1 次。');
  expect(calls).toBe(1);
  await button.click();
  await expect(page.locator('#analysis-content')).toContainText('累计反馈 2 次。');
  expect(calls).toBe(2);
});

for (const [label, fields, message] of [
  ['缺少姓名', [['月租', '3000']], '表格中缺少中介姓名'],
  ['空白姓名', [['中介姓名', '　 ']], '表格中缺少中介姓名'],
  ['多个不同姓名', [['中介姓名', '张三'], ['中介姓名', '李四']], '表格中有多个不同的中介姓名'],
] as const) {
  test(`${label}时不提交反馈并提示修正`, async ({ page }) => {
    let calls = 0;
    await page.route('**/api/broker-feedback', route => { calls++; return route.fulfill({ status: 500 }); });
    await page.goto('/');
    await page.getByLabel('选择 Excel 文件').setInputFiles(file(fields.map(row => [...row])));
    await page.getByRole('button', { name: '发送并分析' }).click();
    await page.getByRole('button', { name: '坑人', exact: true }).click();
    await expect(page.locator('#analysis-content')).toContainText(message);
    expect(calls).toBe(0);
  });
}

test('保存失败显示后端错误，按钮恢复后可重新提交', async ({ page }) => {
  let calls = 0;
  await page.route('**/api/broker-feedback', route => {
    calls++;
    return route.fulfill(calls === 1
      ? { status: 503, json: { error: { code: 'feedback_unavailable', message: '反馈未能确认保存，请检查数据库连接和迁移状态' } } }
      : { json: { brokerName: '李四', feedbackCount: 1 } });
  });
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['中介姓名', '李四']]));
  await page.getByRole('button', { name: '发送并分析' }).click();
  const button = page.getByRole('button', { name: '坑人', exact: true });
  await button.click();
  await expect(page.locator('#analysis-content')).toContainText('反馈未能确认保存');
  await expect(page.locator('#analysis-content')).not.toContainText('已经记录');
  await expect(button).toBeEnabled();
  await button.click();
  await expect(page.locator('#analysis-content')).toContainText('已经记录坑人中介：李四，累计反馈 1 次。');
});

test('未配置数据库时真实接口明确提示，页面不会假报成功', async ({ page }) => {
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['中介姓名', '测试中介']]));
  await page.getByRole('button', { name: '发送并分析' }).click();
  await page.getByRole('button', { name: '坑人', exact: true }).click();
  await expect(page.locator('#analysis-content')).toContainText('未启用反馈保存');
  await expect(page.locator('#analysis-content')).not.toContainText('已经记录');
});
