import { expect, test } from '@playwright/test';
import { file } from './xlsx';

test('API Key 只提交到登录接口，输入和浏览器存储不保留密钥', async ({ page }) => {
  let loggedIn = false;
  let submitted: unknown;
  await page.route('**/api/codex/account', route => route.fulfill({ json: { loggedIn, pending: false, authType: loggedIn ? 'apiKey' : undefined } }));
  await page.route('**/api/codex/login', route => {
    submitted = route.request().postDataJSON(); loggedIn = true;
    return route.fulfill({ json: { type: 'apiKey' } });
  });
  await page.route('**/api/codex/logout', route => { loggedIn = false; return route.fulfill({ json: { ok: true } }); });
  await page.goto('/');
  await page.getByText('使用 API Key', { exact: true }).click();
  await page.getByLabel('OpenAI API Key').fill('test-only-not-real');
  await page.getByRole('button', { name: '连接', exact: true }).click();
  await expect(page.getByText('已连接 · API Key', { exact: true })).toBeVisible();
  expect(submitted).toEqual({ type: 'apiKey', apiKey: 'test-only-not-real' });
  await expect(page.getByLabel('OpenAI API Key')).toHaveValue('');
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0]);
  await page.getByRole('button', { name: '退出登录 / 取消授权', exact: true }).click();
  await expect(page.locator('#account-status')).toHaveText('未登录');
});

test('OAuth 展示官方授权链接，轮询成功后更新登录状态', async ({ page }) => {
  let pending = false;
  let loggedIn = false;
  await page.route('**/api/codex/account', route => route.fulfill({ json: { loggedIn, pending, authType: 'chatgpt', email: loggedIn ? 'test@example.com' : undefined } }));
  await page.route('**/api/codex/login', route => {
    expect(route.request().postDataJSON()).toEqual({ type: 'chatgpt' });
    pending = true;
    return route.fulfill({ json: { type: 'chatgpt', authUrl: 'https://auth.openai.com/test-authorize' } });
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'OAuth 登录（ChatGPT）' }).click();
  const link = page.getByRole('link', { name: '打开授权页面' });
  await expect(link).toHaveAttribute('href', 'https://auth.openai.com/test-authorize');
  await expect(page.locator('#account-status')).toHaveText('等待授权');
  pending = false; loggedIn = true;
  await expect(page.locator('#account-status')).toHaveText('已连接 · ChatGPT · test@example.com');
  await expect(link).toBeHidden();
});

test('OAuth 失败可重试，取消授权能恢复未登录状态', async ({ page }) => {
  let pending = false;
  let error: string | undefined;
  await page.route('**/api/codex/account', route => route.fulfill({ json: { loggedIn: false, pending, error } }));
  await page.route('**/api/codex/login', route => { pending = true; error = undefined; return route.fulfill({ json: { type: 'chatgpt', authUrl: 'https://auth.openai.com/test' } }); });
  await page.route('**/api/codex/logout', route => { pending = false; return route.fulfill({ json: { ok: true } }); });
  await page.goto('/');
  await page.getByRole('button', { name: 'OAuth 登录（ChatGPT）' }).click();
  await expect(page.locator('#account-status')).toHaveText('等待授权');
  pending = false; error = '登录未完成，请重新登录。';
  await expect(page.locator('#auth-error')).toContainText('登录未完成');
  await page.getByRole('button', { name: 'OAuth 登录（ChatGPT）' }).click();
  await expect(page.locator('#account-status')).toHaveText('等待授权');
  await page.getByRole('button', { name: '退出登录 / 取消授权' }).click();
  await expect(page.locator('#account-status')).toHaveText('未登录');
});

test('分析失败可重试，取消后旧回复不会覆盖新分析', async ({ page }) => {
  await page.route('**/api/codex/account', route => route.fulfill({ json: { loggedIn: true, pending: false, authType: 'apiKey' } }));
  let calls = 0;
  await page.route('**/api/analyze', async route => {
    calls++;
    if (calls === 1) return route.fulfill({ status: 502, json: { error: { code: 'codex_failed', message: 'Codex 未完成请求，请重试' } } });
    if (calls === 2) {
      await new Promise(resolve => setTimeout(resolve, 600));
      await route.fulfill({ json: { text: '过期回复' } }).catch(() => {});
      return;
    }
    return route.fulfill({ json: { text: '最新回复' } });
  });
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '2000']]));
  const send = page.getByRole('button', { name: '发送并分析' });
  await send.click();
  await expect(page.locator('#analysis-content')).toContainText('Codex 未完成请求');
  await send.click();
  await expect.poll(() => calls).toBe(2);
  await page.getByRole('button', { name: '取消分析', exact: true }).click();
  await expect(page.locator('#analysis-content')).toContainText('已取消本次分析');
  await send.click();
  await expect(page.locator('.codex-response')).toHaveText('最新回复');
  await page.waitForTimeout(700);
  await expect(page.locator('.codex-response')).toHaveText('最新回复');
});
