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

test('Responses 引用显示可点击来源，保留 Unicode 原文并拒绝危险引用', async ({ page }) => {
  const text = '参考价🏠[来源]。';
  const citations = [
    { title: '<img onerror="window.hacked=true">', url: 'https://example.com/fees', startIndex: 4, endIndex: 8 },
    { title: '补充', url: 'https://example.com/other', startIndex: 4, endIndex: 8 },
    { title: '危险', url: 'javascript:alert(1)', startIndex: 4, endIndex: 8 },
    { title: '越界', url: 'https://example.com/invalid', startIndex: 50, endIndex: 60 },
  ];
  await page.route('**/api/analyze', route => route.fulfill({ json: { text, citations } }));
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '1000']]));
  await page.getByRole('button', { name: '发送并分析' }).click();
  const body = page.locator('.review-report');
  await expect(body).toContainText('参考价🏠');
  await expect(body.getByRole('link')).toHaveCount(2);
  await expect(body.getByRole('link', { name: '1', exact: true })).toHaveAttribute('href', 'https://example.com/fees');
  await expect(body.getByRole('link', { name: '2', exact: true })).toHaveAttribute('href', 'https://example.com/other');
  await expect(body.locator('img, script')).toHaveCount(0);
  await page.getByText('查看 AI 原文', { exact: true }).click();
  expect(await page.locator('.ai-response').textContent()).toBe(text);
});

test('费用对照表和来源可阅读，保留完整回复原文，手机仅表格横向滚动', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected: true, model: 'test-model', baseUrl: 'https://example.com/v1' } }));
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
  await page.getByText('查看 AI 原文', { exact: true }).click();
  await expect(page.locator('.ai-response')).toBeVisible();
  expect(await page.locator('.ai-response').textContent()).toBe(report);
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.getByText('查看 AI 原文', { exact: true }).click();
  await page.screenshot({ path: 'test-results/desktop-review.png', fullPage: true });
});

test('来源不足正常展示未能核实，模型输出不能注入 HTML 或危险链接', async ({ page }) => {
  const answer = '未能核实：地点不明确，未找到可比来源。\n\n<script>window.hacked=true</script>\n\n<img src="https://example.com/track" onerror="window.hacked=true">\n\n[危险](javascript:alert(1)) [本地](file:///etc/passwd) [相对](/api/ai/disconnect) ![追踪](https://example.com/image)';
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

test('精简报告段落清晰，手机两三列表格无需横向滚动', async ({ page }) => {
  const conciseReport = `**首月付款有待解释余额；后续月费的物业费口径不清。**

### 首次付款

若下列费用均首次另收：押金1500＋首期租金1500＋中介费1500＋物业费300＋保洁费100＋燃气费100＝**5000元**。

填写6200元，与该条件小计相差**1200元**，需中介解释。电费用量及部分费用收取时间未知，不能直接认定为多收。

### 收费口径

| 项目 | 报价 | 待确认 |
| --- | --- | --- |
| 物业费 | 300元/月 | 后续月费1500元是否已含 |
| 保洁、燃气 | 各100元 | 覆盖期间及首次是否收取 |

| 价格核实 | 结果 |
| --- | --- |
| 电费 | 本测试使用[虚构收费说明](https://example.com/fees)，仅用于验证来源链接排版 |

### 向中介确认

1. 提供首次付款明细，解释余额及电费是否计入。
2. 确认物业费是否另付，以及一次性费用的覆盖期间。`;
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected: true, model: 'test-model', baseUrl: 'https://example.com/v1' } }));
  await page.route('**/api/analyze', route => route.fulfill({ json: { text: conciseReport } }));
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '1500'], ['押几付几', '押一付一'], ['首月总共要付多少', '6200']], '精简报告.xlsx', ['A3']));
  await page.getByRole('button', { name: '发送并分析' }).click();
  const reportBody = page.locator('.review-report');
  await expect(reportBody.locator('h3')).toHaveCount(3);
  await expect(reportBody.locator('table')).toHaveCount(2);
  await expect(reportBody.locator('strong')).toContainText(['首月付款有待解释余额', '5000元', '1200元']);
  await expect(reportBody).not.toContainText('field-');
  await expect(reportBody.locator('.report-table-wide')).toHaveCount(0);
  for (const table of await reportBody.locator('.report-table').all()) {
    expect(await table.evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
    await expect(table).toHaveAttribute('aria-label', '费用明细表');
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await reportBody.screenshot({ path: 'test-results/mobile-review-concise.png' });
  await page.getByText('查看 AI 原文', { exact: true }).click();
  expect(await page.locator('.ai-response').textContent()).toBe(conciseReport);
  await page.getByText('查看 AI 原文', { exact: true }).click();
  await page.setViewportSize({ width: 1280, height: 900 });
  await reportBody.screenshot({ path: 'test-results/desktop-review-concise.png' });
});
