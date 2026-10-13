import { expect, test } from '@playwright/test';
import { file } from './xlsx';

const report = `## 主要发现

**明显异常**：物业费按同一口径换算为参考值的 12 倍（field-3，物业费，Sheet1!B3）。

## 价格与收费对照

| 项目 | 填写金额 | 参考金额 | 差异 | 依据 |
| --- | --- | --- | --- | --- |
| 物业费 | 300 元/月 | 300 元/年 | 3600/300 = 12 倍 | [1：收费公示](https://example.com/fees) |

## 加粗项目核算：首次付款

| 项目 | 计算式 | 计入金额 | 周期 |
| --- | --- | --- | --- |
| 押金及首期租金 | 3000 + 3000 * 3 | 12000 元 | 押一付三 |

水电费缺少用量，以上为已知小计。与填写总额的比较尚不能确定。

## 来源

[1：收费公示](https://example.com/fees)，发布于 2026-09-01，查询于 2026-10-13；本测试使用虚构来源。`;

test('费用对照表和来源可阅读，保留完整回复原文，手机仅表格横向滚动', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route('**/api/codex/account', route => route.fulfill({ json: { loggedIn: true, pending: false } }));
  await page.route('**/api/analyze', route => route.fulfill({ json: { text: report } }));
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '3000'], ['押几付几', '押一付三'], ['物业费', '300元/月'], ['首次付款', '12000']], '测试房源.xlsx', ['A4']));
  await page.getByRole('button', { name: '发送并分析' }).click();
  await expect(page.locator('.review-report table')).toHaveCount(2);
  await expect(page.locator('.review-report')).toContainText('以上为已知小计');
  const source = page.locator('.review-report').getByRole('link', { name: '1：收费公示' }).first();
  await expect(source).toHaveAttribute('href', 'https://example.com/fees');
  await expect(source).toHaveAttribute('rel', 'noopener noreferrer');
  await expect(source).toHaveAttribute('target', '_blank');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  expect(await page.locator('.report-table').first().evaluate(node => node.scrollWidth > node.clientWidth)).toBe(true);
  await page.screenshot({ path: 'test-results/mobile-review.png', fullPage: true });
  await page.getByText('查看 Codex 原文', { exact: true }).click();
  await expect(page.locator('.codex-response')).toBeVisible();
  expect(await page.locator('.codex-response').textContent()).toBe(report);
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.getByText('查看 Codex 原文', { exact: true }).click();
  await page.screenshot({ path: 'test-results/desktop-review.png', fullPage: true });
});

test('来源不足正常展示未能核实，模型输出不能注入 HTML 或危险链接', async ({ page }) => {
  const answer = '未能核实：地点不明确，未找到可比来源。\n\n<script>window.hacked=true</script>\n\n<img src="https://example.com/track" onerror="window.hacked=true">\n\n[危险](javascript:alert(1)) [本地](file:///etc/passwd) [相对](/api/codex/logout) ![追踪](https://example.com/image)';
  await page.route('**/api/analyze', route => route.fulfill({ json: { text: answer } }));
  let tracked = false;
  await page.route('https://example.com/**', route => { tracked = true; return route.abort(); });
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '1000']]));
  await page.getByRole('button', { name: '发送并分析' }).click();
  await expect(page.locator('.review-report')).toContainText('未能核实');
  await expect(page.locator('.review-report img, .review-report script')).toHaveCount(0);
  for (const link of await page.locator('.review-report a').all()) {
    expect(await link.getAttribute('href')).toMatch(/^https:\/\/example\.com\//);
  }
  await expect(page.locator('.review-report').getByRole('link', { name: /危险|本地|相对/ })).toHaveCount(0);
  expect(await page.evaluate(() => 'hacked' in window)).toBe(false);
  expect(tracked).toBe(false);
});
