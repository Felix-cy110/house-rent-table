import { expect, test } from '@playwright/test';
import { createServer } from 'node:http';
import { file } from './xlsx';

test('直接显示三项必填配置，页面不再提供 Codex 登录或导入', async ({ page }) => {
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected: false } }));
  await page.goto('/');
  await expect(page.locator('#account-status')).toHaveText('未配置');
  for (const name of ['API URL', '模型', 'API Key']) {
    await expect(page.getByLabel(name, { exact: true })).toBeVisible();
    await expect(page.getByLabel(name, { exact: true })).toHaveAttribute('required', '');
  }
  await expect(page.getByRole('button', { name: '验证并保存', exact: true })).toBeVisible();
  await expect(page.locator('body')).not.toContainText('Codex');
  await expect(page.locator('body')).not.toContainText('OAuth');
  await expect(page.locator('body')).not.toContainText('CC Switch');
});

test('验证完成前显示等待，失败可重试并保留原配置', async ({ page }) => {
  let connected = false;
  let attempts = 0;
  let finishProbe: (success: boolean) => void = () => {};
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected: true, model: connected ? 'new-model' : 'original-model', baseUrl: 'https://gateway.example/v1' } }));
  await page.route('**/api/ai/connect', async route => {
    attempts++;
    const success = await new Promise<boolean>(resolve => { finishProbe = resolve; });
    connected = success;
    await route.fulfill(success ? { json: { ok: true } } : { status: 502, json: { error: { code: 'connection_failed', message: 'API Key 无效或已过期，请检查后重新填写' } } });
  });
  await page.goto('/');
  const original = '已配置 · original-model · https://gateway.example/v1';
  await expect(page.locator('#account-status')).toHaveText(original);
  await page.getByRole('button', { name: '修改配置', exact: true }).click();
  await expect(page.getByLabel('API URL', { exact: true })).toHaveValue('https://gateway.example/v1');
  await expect(page.getByLabel('模型', { exact: true })).toHaveValue('original-model');
  await page.getByLabel('模型', { exact: true }).fill('new-model');
  await page.getByLabel('API Key', { exact: true }).fill('wrong-test-secret');
  const button = page.locator('#key-connect');
  const successDialog = page.getByRole('dialog', { name: '连接成功', exact: true });
  const failureDialog = page.getByRole('dialog', { name: '连接失败', exact: true });
  await button.click();
  await expect.poll(() => attempts).toBe(1);
  await expect(page.locator('#account-status')).toHaveText('正在验证连接…');
  await expect(button).toBeDisabled();
  await expect(page.locator('#account-refresh')).toBeDisabled();
  await expect(page.getByLabel('API URL', { exact: true })).toBeDisabled();
  await expect(page.getByLabel('API Key', { exact: true })).toHaveValue('');
  await expect(successDialog).toBeHidden();
  finishProbe(false);
  await expect(failureDialog).toBeVisible();
  await expect(failureDialog).toContainText('API Key 无效');
  await expect(page.locator('#account-status')).toHaveText(original);
  await expect(button).toBeEnabled();
  await failureDialog.getByRole('button', { name: '知道了', exact: true }).click();
  await expect(button).toBeFocused();
  await expect(page.getByLabel('模型', { exact: true })).toHaveValue('new-model');
  await page.getByLabel('API Key', { exact: true }).fill('correct-test-secret');
  await button.click();
  await expect.poll(() => attempts).toBe(2);
  finishProbe(true);
  await expect(page.locator('#account-status')).toHaveText('已配置 · new-model · https://gateway.example/v1');
  await expect(successDialog).toBeVisible();
  await expect(successDialog).toContainText('已收到模型的测试回复');
  await expect(page.locator('#login-options')).toBeHidden();
  await expect(page.locator('#auth-error')).toBeHidden();
  await successDialog.getByRole('button', { name: '知道了', exact: true }).click();
  await expect(page.locator('#account-refresh')).toBeFocused();
  await page.getByRole('button', { name: '刷新状态', exact: true }).click();
  await expect(successDialog).toBeHidden();
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0]);
});

test('连接只提交三项配置，刷新可恢复且清除后移除配置', async ({ page }) => {
  let connected = false;
  let submitted: unknown;
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected, model: 'chosen-model', baseUrl: 'https://gateway.example/v1/responses' } }));
  await page.route('**/api/ai/connect', route => {
    submitted = route.request().postDataJSON(); connected = true;
    return route.fulfill({ json: { ok: true } });
  });
  await page.route('**/api/ai/disconnect', route => { connected = false; return route.fulfill({ json: { ok: true } }); });
  await page.goto('/');
  await page.getByLabel('API URL', { exact: true }).fill('https://gateway.example/v1/responses');
  await page.getByLabel('模型', { exact: true }).fill('chosen-model');
  await page.getByLabel('API Key', { exact: true }).fill('custom-test-secret');
  await page.getByRole('button', { name: '验证并保存', exact: true }).click();
  await expect(page.getByRole('dialog', { name: '连接成功', exact: true })).toBeVisible();
  expect(submitted).toEqual({ apiKey: 'custom-test-secret', baseUrl: 'https://gateway.example/v1/responses', model: 'chosen-model' });
  await expect(page.locator('#api-key')).toHaveValue('');
  await expect(page.locator('body')).not.toContainText('custom-test-secret');
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0]);
  await page.reload();
  await expect(page.locator('#account-status')).toHaveText('已配置 · chosen-model · https://gateway.example/v1/responses');
  await page.getByRole('button', { name: '清除配置', exact: true }).click();
  await expect(page.locator('#account-status')).toHaveText('未配置');
  await expect(page.getByLabel('API URL', { exact: true })).toHaveValue('');
  await expect(page.getByLabel('模型', { exact: true })).toHaveValue('');
  await expect(page.getByLabel('API Key', { exact: true })).toHaveValue('');
});

test('首次连接超时后保持未配置且可重试', async ({ page }) => {
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected: false } }));
  await page.route('**/api/ai/connect', route => route.fulfill({ status: 504, json: { error: { code: 'connection_failed', message: '连接验证超时，请检查网络后重试' } } }));
  await page.goto('/');
  await page.getByLabel('API URL', { exact: true }).fill('https://gateway.example/v1');
  await page.getByLabel('模型', { exact: true }).fill('chosen-model');
  await page.getByLabel('API Key', { exact: true }).fill('test-secret');
  await page.getByRole('button', { name: '验证并保存', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '连接失败', exact: true });
  await expect(dialog).toContainText('连接验证超时');
  await expect(page.locator('#account-status')).toHaveText('未配置');
  await expect(page.locator('#api-key')).toHaveValue('');
  await page.keyboard.press('Escape');
  await expect(page.locator('#key-connect')).toBeFocused();
});

test('分析失败可重试，取消后旧回复不会覆盖新分析', async ({ page }) => {
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected: true, model: 'm', baseUrl: 'https://example.com/v1' } }));
  let calls = 0;
  await page.route('**/api/analyze', async route => {
    calls++;
    if (calls === 1) return route.fulfill({ status: 502, json: { error: { code: 'ai_failed', message: 'AI 未完成请求，请重试' } } });
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
  await expect(page.locator('#analysis-content')).toContainText('AI 未完成请求');
  await send.click();
  await expect.poll(() => calls).toBe(2);
  await page.getByRole('button', { name: '取消分析', exact: true }).click();
  await expect(page.locator('#analysis-content')).toContainText('已取消本次分析');
  await send.click();
  await expect(page.locator('.ai-response')).toHaveText('最新回复');
  await page.waitForTimeout(700);
  await expect(page.locator('.ai-response')).toHaveText('最新回复');
});

test('真实 Go 服务直连本地 API 完成配置、工具核算和分析', async ({ page }) => {
  let calls = 0;
  const provider = createServer(async (request, response) => {
    const chunks: Buffer[] = [];
    for await (const chunk of request) chunks.push(Buffer.from(chunk));
    const input = JSON.parse(Buffer.concat(chunks).toString());
    expect(request.url).toBe('/v1/chat/completions');
    expect(request.headers.authorization).toBe('Bearer local-test-only-key');
    expect(input.model).toBe('local-test-model');
    calls++;
    response.setHeader('Content-Type', 'application/json');
    if (!input.tools) {
      response.end(JSON.stringify({ choices: [{ finish_reason: 'stop', message: { role: 'assistant', content: 'OK' } }] }));
    } else if (input.messages.at(-1).role !== 'tool') {
      expect(input.messages[1].content).toContain('月租');
      response.end(JSON.stringify({ choices: [{ finish_reason: 'tool_calls', message: { role: 'assistant', content: null, tool_calls: [{ id: 'calc1', type: 'function', function: { name: 'rental_calculate', arguments: JSON.stringify({ calculations: [{ label: '押一付三', expression: '2000*(1+3)' }] }) } }] } }] }));
    } else {
      expect(input.messages.at(-1).content).toContain('8000.00');
      response.end(JSON.stringify({ choices: [{ finish_reason: 'stop', message: { role: 'assistant', content: '押一付三合计 8000.00 元。\n\n未能核实：当前接口未提供联网搜索。' } }] }));
    }
  });
  await new Promise<void>(resolve => provider.listen(0, '127.0.0.1', resolve));
  const address = provider.address();
  if (!address || typeof address === 'string') throw new Error('provider did not start');
  try {
    await page.goto('/');
    await page.getByLabel('API URL', { exact: true }).fill(`http://127.0.0.1:${address.port}/v1`);
    await page.getByLabel('模型', { exact: true }).fill('local-test-model');
    await page.getByLabel('API Key', { exact: true }).fill('local-test-only-key');
    await page.getByRole('button', { name: '验证并保存', exact: true }).click();
    await expect(page.getByRole('dialog', { name: '连接成功', exact: true })).toBeVisible();
    await page.getByRole('button', { name: '知道了', exact: true }).click();
    await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '2000'], ['押几付几', '押一付三']]));
    await page.getByRole('button', { name: '发送并分析' }).click();
    await expect(page.locator('.review-report')).toContainText('8000.00 元');
    expect(calls).toBe(3);
    await page.reload();
    await expect(page.locator('#account-status')).toContainText('local-test-model');
    await page.getByRole('button', { name: '清除配置', exact: true }).click();
    await expect(page.locator('#account-status')).toHaveText('未配置');
  } finally {
    await page.request.post('/api/ai/disconnect');
    await new Promise<void>(resolve => provider.close(() => resolve()));
  }
});
