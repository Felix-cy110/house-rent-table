import { expect, test } from '@playwright/test';
import { file } from './xlsx';

test('显示三种固定模型与必填密钥，页面不再提供 Codex 登录或导入', async ({ page }) => {
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected: false } }));
  await page.goto('/');
  await expect(page.locator('#account-status')).toHaveText('未配置');
  for (const name of ['模型', 'API Key']) {
    await expect(page.getByLabel(name, { exact: true })).toBeVisible();
    await expect(page.getByLabel(name, { exact: true })).toHaveAttribute('required', '');
  }
  await expect(page.getByRole('button', { name: '验证并连接Enter', exact: true })).toBeVisible();
  await expect(page.locator('#api-provider option')).toHaveText(['MiMo v2.6-Flash', 'GLM 5.3-Flash', 'DeepSeek Flash']);
  await expect(page.locator('#api-base-url, #api-model')).toHaveCount(0);
  await page.getByLabel('模型', { exact: true }).selectOption('deepseek');
  await expect(page.locator('#provider-note')).toContainText('DeepSeek V4.1-Flash-1017');
  await expect(page.locator('#provider-note')).toContainText('2026-10-19');
  await expect(page.locator('body')).not.toContainText('Codex');
  await expect(page.locator('body')).not.toContainText('OAuth');
  await expect(page.locator('body')).not.toContainText('CC Switch');
});

test('验证完成前显示等待，失败可重试并保留原配置', async ({ page }) => {
  let connected = false;
  let attempts = 0;
  let finishProbe: (success: boolean) => void = () => {};
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected: true, provider: connected ? 'glm' : 'mimo', model: connected ? 'glm-5.3-flash' : 'mimo-v2.6-flash', name: connected ? 'GLM 5.3-Flash' : 'MiMo v2.6-Flash' } }));
  await page.route('**/api/ai/connect', async route => {
    attempts++;
    const success = await new Promise<boolean>(resolve => { finishProbe = resolve; });
    connected = success;
    await route.fulfill(success ? { json: { ok: true } } : { status: 502, json: { error: { code: 'connection_failed', message: 'API Key 无效或已过期，请检查后重新填写' } } });
  });
  await page.goto('/');
  const original = '已配置 · MiMo v2.6-Flash · 思考已开启';
  await expect(page.locator('#account-status')).toHaveText(original);
  await page.getByRole('button', { name: '修改配置', exact: true }).click();
  await expect(page.getByLabel('模型', { exact: true })).toHaveValue('mimo');
  await page.getByLabel('模型', { exact: true }).selectOption('glm');
  await page.getByLabel('API Key', { exact: true }).fill('wrong-test-secret');
  const button = page.locator('#key-connect');
  const successDialog = page.getByRole('dialog', { name: '连接成功', exact: true });
  const failureDialog = page.getByRole('dialog', { name: '连接失败', exact: true });
  await button.click();
  await expect.poll(() => attempts).toBe(1);
  await expect(page.locator('#account-status')).toHaveText('正在验证连接…');
  await expect(button).toBeDisabled();
  await expect(page.locator('#account-refresh')).toBeDisabled();
  await expect(page.getByLabel('模型', { exact: true })).toBeDisabled();
  await expect(page.getByLabel('API Key', { exact: true })).toHaveValue('');
  await expect(successDialog).toBeHidden();
  finishProbe(false);
  await expect(failureDialog).toBeVisible();
  await expect(failureDialog).toContainText('API Key 无效');
  await expect(page.locator('#account-status')).toHaveText(original);
  await expect(button).toBeEnabled();
  await failureDialog.getByRole('button', { name: '知道了', exact: true }).click();
  await expect(button).toBeFocused();
  await expect(page.getByLabel('模型', { exact: true })).toHaveValue('glm');
  await page.getByLabel('API Key', { exact: true }).fill('correct-test-secret');
  await button.click();
  await expect.poll(() => attempts).toBe(2);
  finishProbe(true);
  await expect(page.locator('#account-status')).toHaveText('已配置 · GLM 5.3-Flash · 思考已开启');
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

test('连接只提交供应商和密钥，刷新可恢复且清除后移除配置', async ({ page }) => {
  let connected = false;
  let submitted: unknown;
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected, provider: 'deepseek', model: 'deepseek-flash', name: 'DeepSeek Flash' } }));
  await page.route('**/api/ai/connect', route => {
    submitted = route.request().postDataJSON(); connected = true;
    return route.fulfill({ json: { ok: true } });
  });
  await page.route('**/api/ai/disconnect', route => { connected = false; return route.fulfill({ json: { ok: true } }); });
  await page.goto('/');
  await page.getByLabel('模型', { exact: true }).selectOption('deepseek');
  await page.getByLabel('API Key', { exact: true }).fill('custom-test-secret');
  await page.getByRole('button', { name: '验证并连接Enter', exact: true }).click();
  await expect(page.getByRole('dialog', { name: '连接成功', exact: true })).toBeVisible();
  expect(submitted).toEqual({ apiKey: 'custom-test-secret', provider: 'deepseek' });
  await expect(page.locator('#api-key')).toHaveValue('');
  await expect(page.locator('body')).not.toContainText('custom-test-secret');
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0]);
  await page.reload();
  await expect(page.locator('#account-status')).toHaveText('已配置 · DeepSeek Flash · 思考已开启');
  await expect(page.locator('#connected-version')).toContainText('DeepSeek V4.1-Flash-1017');
  await page.getByRole('button', { name: '清除配置', exact: true }).click();
  await expect(page.locator('#account-status')).toHaveText('未配置');
  await expect(page.getByLabel('模型', { exact: true })).toHaveValue('mimo');
  await expect(page.getByLabel('API Key', { exact: true })).toHaveValue('');
});

test('首次连接超时后保持未配置且可重试', async ({ page }) => {
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected: false } }));
  await page.route('**/api/ai/connect', route => route.fulfill({ status: 504, json: { error: { code: 'connection_failed', message: '连接验证超时，请检查网络后重试' } } }));
  await page.goto('/');
  await page.getByLabel('模型', { exact: true }).selectOption('deepseek');
  await page.getByLabel('API Key', { exact: true }).fill('test-secret');
  await page.getByRole('button', { name: '验证并连接Enter', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '连接失败', exact: true });
  await expect(dialog).toContainText('连接验证超时');
  await expect(page.locator('#account-status')).toHaveText('未配置');
  await expect(page.locator('#api-key')).toHaveValue('');
  await page.keyboard.press('Escape');
  await expect(page.locator('#key-connect')).toBeFocused();
});

test('分析失败可重试，取消后旧回复不会覆盖新分析', async ({ page }) => {
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected: true, provider: 'mimo', name: 'MiMo v2.6-Flash', model: 'mimo-v2.6-flash' } }));
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

test('真实 Go 服务完成 MiMo 配置、核算、搜索降级与来源展示', async ({ page }) => {
  try {
    await page.goto('/');
    await page.getByLabel('模型', { exact: true }).selectOption('mimo');
    await page.getByLabel('API Key', { exact: true }).fill('local-test-only-key');
    await page.getByRole('button', { name: '验证并连接Enter', exact: true }).click();
    await expect(page.getByRole('dialog', { name: '连接成功', exact: true })).toBeVisible();
    await page.getByRole('button', { name: '知道了', exact: true }).click();
    await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '2000'], ['押几付几', '押一付三']]));
    await page.getByRole('button', { name: '发送并分析' }).click();
    await expect(page.locator('.review-report')).toContainText('8000.00 元');
    await expect(page.locator('body')).not.toContainText('fixture-private-thinking');
    await expect(page.locator('body')).not.toContainText('local-search-only-key');
    await page.getByRole('link', { name: '查看日志', exact: true }).click();
    await expect(page).toHaveURL(/#\/logs\//);
    await expect(page.getByRole('heading', { name: '分析日志', exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: '精简版', exact: true })).toHaveAttribute('aria-pressed', 'true');
    const conciseText = (await page.locator('#turn-json').textContent())!;
    const concise = JSON.parse(conciseText);
    expect(concise.prompt.messages.map((message: { role: string }) => message.role)).toEqual(['system', 'user']);
    expect(concise.steps.map((step: { kind: string }) => step.kind)).toEqual(['model', 'tool', 'model', 'model', 'tool', 'model']);
    expect(concise.steps[0].response.choices[0].message.reasoning_content).toBe('fixture-private-thinking');
    expect(JSON.stringify(concise.steps[1].output)).toContain('8000.00');
    expect(concise.steps[2].response.error).toBeTruthy();
    expect(concise.steps[3].promptChanges).toBeTruthy();
    expect(concise.result.text).toBe('押一付三合计 8000.00 元。');
    expect(conciseText).not.toContain('local-test-only-key');
    expect(conciseText).not.toContain('local-search-only-key');
    await page.getByRole('button', { name: '全量版', exact: true }).click();
    const logText = await page.locator('#turn-json').textContent();
    expect(conciseText.length).toBeLessThan(logText!.length);
    const turn = JSON.parse(logText!);
    expect(turn.status).toBe('completed');
    expect(turn.provider).toBe('mimo');
    expect(turn.events.map((event: { kind: string }) => event.kind)).toEqual(['model', 'tool', 'model', 'model', 'tool', 'search', 'model']);
    expect(turn.events[0].response.choices[0].message.reasoning_content).toBe('fixture-private-thinking');
    expect(turn.events[1].output).toContain('8000.00');
    expect(turn.events[2].httpStatus).toBe(403);
    expect(turn.events[5].response.search_result[0].title).toBe('测试公示');
    expect(turn.result.text).toBe('押一付三合计 8000.00 元。');
    expect(logText).not.toContain('local-test-only-key');
    expect(logText).not.toContain('local-search-only-key');
    await expect(page.locator('.turn-stats')).toContainText('4 轮');
    await expect(page.locator('.turn-stats')).toContainText('2 次');
    expect(page.context().pages()).toHaveLength(1);
    await page.getByRole('button', { name: '精简版', exact: true }).click();
    await page.screenshot({ path: 'test-results/desktop-turn-log.png', fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: 'test-results/mobile-turn-log.png', fullPage: true });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await page.getByRole('button', { name: '返回报告' }).click();
    await expect(page).toHaveURL(/\/$/);
    await expect(page.locator('.report-warnings')).toContainText('已切换到服务端备用搜索');
    await page.locator('.report-sources summary').click();
    await expect(page.getByRole('link', { name: '测试公示' })).toHaveAttribute('href', 'https://example.com/fees');
    await page.reload();
    await expect(page.locator('#account-status')).toContainText('MiMo v2.6-Flash');
    await page.getByRole('button', { name: '清除配置', exact: true }).click();
    await expect(page.locator('#account-status')).toHaveText('未配置');
  } finally { await page.request.post('/api/ai/disconnect'); }
});

test('旧配置提示重连，来源缺少位置仍展示且不解释 HTML', async ({ page }) => {
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected: false, needsReconnect: true } }));
  await page.route('**/api/analyze', route => route.fulfill({ json: {
    text: '费用计算完成。',
    sources: [{ title: '<img src=x onerror=alert(1)>', url: 'https://example.com/safe' }, { title: '危险链接', url: 'javascript:alert(1)' }],
    warnings: ['本次搜索未完整完成，价格仍需核实。'],
  } }));
  await page.goto('/');
  await expect(page.locator('#account-status')).toContainText('请重新配置');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '2000']]));
  await page.getByRole('button', { name: '发送并分析' }).click();
  await expect(page.locator('.report-warnings')).toContainText('价格仍需核实');
  await page.locator('.report-sources summary').click();
  await expect(page.locator('.report-sources a')).toHaveCount(1);
  await expect(page.locator('.report-sources a')).toHaveText('<img src=x onerror=alert(1)>');
  await expect(page.locator('.report-sources img')).toHaveCount(0);
  await expect(page.locator('.ai-response')).toHaveText('费用计算完成。');
});
