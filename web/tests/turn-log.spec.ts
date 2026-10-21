import { expect, test } from '@playwright/test';
import { file } from './xlsx';

test('日志在独立页面显示原始 JSON，大整数和 HTML 字符串保持原样', async ({ page }) => {
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
  await expect(logLink).toHaveAttribute('target', '_blank');
  await expect(logLink).toHaveAttribute('rel', 'noopener noreferrer');
  const opened = page.context().waitForEvent('page');
  await logLink.click();
  const logPage = await opened;
  await logPage.waitForLoadState('domcontentloaded');
  expect(await logPage.locator('pre').textContent()).toBe(turnJson);
  await expect(logPage.locator('script, img')).toHaveCount(0);
  expect(await logPage.evaluate(() => ({ hacked: 'hacked' in window, opener: window.opener }))).toEqual({ hacked: false, opener: null });
  await expect(page.locator('.review-report')).toHaveText('费用已核对。');
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: 'test-results/mobile-turn-log-button.png', fullPage: true });
  await logPage.screenshot({ path: 'test-results/turn-json.png' });
  await logPage.reload();
  expect(await logPage.locator('pre').textContent()).toBe(turnJson);
  await page.getByRole('button', { name: '清空', exact: true }).click();
  await expect(logLink).toHaveCount(0);
  // An already-open snapshot remains readable after clearing the report.
  expect(await logPage.locator('pre').textContent()).toBe(turnJson);
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
  for (let turn = 1; turn <= 2; turn++) {
    await send.click();
    await expect(page.locator('.review-report')).toHaveText(`报告 ${turn}`);
    const opened = page.context().waitForEvent('page');
    await link.click();
    const logPage = await opened;
    await logPage.waitForLoadState('domcontentloaded');
    expect(JSON.parse((await logPage.locator('pre').textContent())!)).toEqual({ turn });
    await logPage.close();
  }
  await send.click();
  await expect(page.locator('#analysis-content')).toContainText('分析失败');
  await expect(link).toHaveCount(0);
  await send.click();
  await expect(page.locator('.review-report')).toHaveText('报告 4');
  await expect(link).toHaveCount(0);
});
