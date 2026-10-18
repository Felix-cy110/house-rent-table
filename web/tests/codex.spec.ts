import { expect, test } from '@playwright/test';
import { file } from './xlsx';

for (const source of ['manual', 'ccswitch'] as const) {
  test(`${source} 验证完成前显示等待，失败后可重试且保留原连接`, async ({ page }) => {
    let connected = false;
    let attempts = 0;
    let finishProbe: (success: boolean) => void = () => {};
    await page.route('**/api/codex/account', route => route.fulfill({ json: { loggedIn: true, pending: false, authType: 'apiKey', provider: connected ? '新供应商' : '原供应商' } }));
    await page.route('**/api/codex/login', async route => {
      attempts++;
      const success = await new Promise<boolean>(resolve => { finishProbe = resolve; });
      connected = success;
      await route.fulfill(success
        ? { json: { type: 'apiKey' } }
        : { status: 502, json: { error: { code: 'connection_failed', message: '连接验证失败：API Key 无效或已过期，请检查后重新填写' } } });
    });
    await page.goto('/');
    await expect(page.locator('#account-status')).toHaveText('已连接 · API Key · 原供应商');
    await page.getByRole('button', { name: '切换连接', exact: true }).click();
    await page.getByText('API Key / 自定义供应商', { exact: true }).click();
    const button = page.locator(source === 'manual' ? '#key-connect' : '#ccswitch-import');
    const successDialog = page.getByRole('dialog', { name: '连接成功', exact: true });
    if (source === 'manual') {
      await page.getByLabel('API 地址').fill('https://gateway.example/v1');
      await page.getByLabel('模型', { exact: false }).fill('chosen-model');
      await page.getByLabel('API Key', { exact: true }).fill('wrong-test-secret');
    }
    await button.click();
    await expect.poll(() => attempts).toBe(1);
    await expect(page.locator('#account-status')).toHaveText('正在验证连接…');
    await expect(button).toHaveText('正在验证连接…');
    await expect(button).toBeDisabled();
    await expect(successDialog).toBeHidden();
    await expect(page.locator('#account-refresh')).toBeDisabled();
    await expect(page.getByLabel('API Key', { exact: true })).toHaveValue('');
    finishProbe(false);
    await expect(page.locator('#auth-error')).toContainText('API Key 无效');
    await expect(successDialog).toBeHidden();
    await expect(page.locator('#account-status')).toHaveText('已连接 · API Key · 原供应商');
    await expect(button).toBeEnabled();
    await expect(page.locator('#key-details')).toHaveAttribute('open', '');
    if (source === 'manual') await page.getByLabel('API Key', { exact: true }).fill('correct-test-secret');
    await button.click();
    await expect.poll(() => attempts).toBe(2);
    await expect(page.locator('#account-status')).toHaveText('正在验证连接…');
    await expect(successDialog).toBeHidden();
    finishProbe(true);
    await expect(page.locator('#account-status')).toHaveText('已连接 · API Key · 新供应商');
    await expect(page.locator('#auth-error')).toBeHidden();
    await expect(page.locator('#login-options')).toBeHidden();
    await expect(successDialog).toBeVisible();
    await expect(successDialog).toContainText('已收到模型的测试回复');
    await expect(successDialog.getByRole('button', { name: '知道了', exact: true })).toBeFocused();
    expect(new URL(page.url()).pathname).toBe('/');
    await successDialog.getByRole('button', { name: '知道了', exact: true }).click();
    await expect(successDialog).toBeHidden();
    await expect(page.locator('#account-refresh')).toBeFocused();
    await page.getByRole('button', { name: '刷新状态', exact: true }).click();
    await expect(successDialog).toBeHidden();
    await expect(page.locator('body')).not.toContainText('test-secret');
    expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0]);
  });
}

test('首次连接验证失败时保持未登录并显示错误', async ({ page }) => {
  await page.route('**/api/codex/account', route => route.fulfill({ json: { loggedIn: false, pending: false } }));
  await page.route('**/api/codex/login', route => route.fulfill({ status: 504, json: { error: { code: 'connection_failed', message: '连接验证超时，请检查 API 地址、网络和供应商服务后重试' } } }));
  await page.goto('/');
  await expect(page.locator('#account-status')).toHaveText('未登录');
  await page.getByText('API Key / 自定义供应商', { exact: true }).click();
  await page.getByLabel('API Key', { exact: true }).fill('test-only-wrong-key');
  await page.getByRole('button', { name: '连接', exact: true }).click();
  await expect(page.locator('#auth-error')).toContainText('连接验证超时');
  await expect(page.getByRole('dialog', { name: '连接成功', exact: true })).toBeHidden();
  await expect(page.locator('#account-status')).toHaveText('未登录');
  await expect(page.getByLabel('API Key', { exact: true })).toHaveValue('');
  await expect(page.getByRole('button', { name: '连接', exact: true })).toBeEnabled();
});

test('自定义供应商提交地址和模型，密钥不留在页面或存储中', async ({ page }) => {
  let loggedIn = false;
  let submitted: unknown;
  await page.route('**/api/codex/account', route => route.fulfill({ json: { loggedIn, pending: false, authType: 'apiKey', provider: loggedIn ? '测试供应商' : undefined, model: loggedIn ? 'chosen-model' : undefined, source: 'manual' } }));
  await page.route('**/api/codex/login', route => {
    submitted = route.request().postDataJSON(); loggedIn = true;
    return route.fulfill({ json: { type: 'apiKey' } });
  });
  await page.goto('/');
  await page.getByText('API Key / 自定义供应商', { exact: true }).click();
  await page.getByLabel('API 地址').fill('https://gateway.example/v1');
  await page.getByLabel('模型', { exact: false }).fill('chosen-model');
  await page.getByLabel('API Key', { exact: true }).fill('custom-test-secret');
  await page.getByRole('button', { name: '连接', exact: true }).click();
  expect(submitted).toEqual({ type: 'apiKey', apiKey: 'custom-test-secret', baseUrl: 'https://gateway.example/v1', model: 'chosen-model' });
  await expect(page.locator('#account-status')).toContainText('测试供应商 · chosen-model');
  await expect(page.locator('#api-key')).toHaveValue('');
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0]);
  await expect(page.locator('body')).not.toContainText('custom-test-secret');
  await expect(page.getByRole('dialog', { name: '连接成功', exact: true })).toBeVisible();
});

test('CC Switch 导入复用 API Key，失败后保留原状态并允许重试', async ({ page }) => {
  let imported = false;
  let attempts = 0;
  await page.route('**/api/codex/account', route => route.fulfill({ json: { loggedIn: true, pending: false, authType: imported ? 'apiKey' : 'chatgpt', provider: imported ? '本地路由' : undefined, model: imported ? 'imported-model' : undefined, source: imported ? 'ccswitch' : undefined } }));
  await page.route('**/api/codex/login', route => {
    expect(route.request().postDataJSON()).toEqual({ type: 'apiKey', source: 'ccswitch', configDir: 'C:/example/.codex' });
    attempts++;
    if (attempts === 1) return route.fulfill({ status: 400, json: { error: { code: 'invalid_provider', message: '未找到可读取的 Codex 配置' } } });
    imported = true;
    return route.fulfill({ json: { type: 'apiKey' } });
  });
  await page.goto('/');
  await page.getByRole('button', { name: '切换连接', exact: true }).click();
  await page.getByText('API Key / 自定义供应商', { exact: true }).click();
  await page.getByText('指定配置目录（可选）', { exact: true }).click();
  await page.getByLabel('Codex 配置目录', { exact: true }).fill('C:/example/.codex');
  await page.getByRole('button', { name: '从 CC Switch 导入并连接', exact: true }).click();
  await expect(page.locator('#auth-error')).toContainText('未找到');
  await expect(page.locator('#account-status')).toHaveText('已连接 · ChatGPT');
  await page.getByRole('button', { name: '从 CC Switch 导入并连接', exact: true }).click();
  await expect(page.locator('#account-status')).toHaveText('已连接 · API Key · 本地路由 · imported-model · 来自 CC Switch');
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0]);
});

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
  await page.getByText('API Key / 自定义供应商', { exact: true }).click();
  await page.getByLabel('API Key', { exact: true }).fill('test-only-not-real');
  await page.getByRole('button', { name: '连接', exact: true }).click();
  await expect(page.getByText('已连接 · API Key', { exact: true })).toBeVisible();
  expect(submitted).toEqual({ type: 'apiKey', apiKey: 'test-only-not-real' });
  await expect(page.getByLabel('API Key', { exact: true })).toHaveValue('');
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0]);
  await page.getByRole('button', { name: '知道了', exact: true }).click();
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
  const successDialog = page.getByRole('dialog', { name: '连接成功', exact: true });
  await expect(successDialog).toBeHidden();
  pending = false; loggedIn = true;
  await expect(page.locator('#account-status')).toHaveText('已连接 · ChatGPT · test@example.com');
  await expect(link).toBeHidden();
  await expect(successDialog).toBeVisible();
  await expect(successDialog).toContainText('已完成 ChatGPT 授权');
  await successDialog.getByRole('button', { name: '知道了', exact: true }).click();
  await page.getByRole('button', { name: '刷新状态', exact: true }).click();
  await expect(successDialog).toBeHidden();
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
  await expect(page.getByRole('dialog', { name: '连接成功', exact: true })).toBeHidden();
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
