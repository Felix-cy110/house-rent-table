import { expect, test } from '@playwright/test';
import { file } from './xlsx';

test.beforeEach(async ({ page }) => {
  await page.route('**/api/ai/config', route => route.fulfill({ json: { connected: false } }));
});

test('设置展示版本号，支持关闭、Esc 和焦点恢复', async ({ page }) => {
  await page.goto('/');
  const settings = page.getByRole('button', { name: '设置', exact: true });
  const dialog = page.getByRole('dialog', { name: '设置', exact: true });
  await expect(dialog).toBeHidden();
  await settings.click();
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('button', { name: '版本号 1.0.0' })).toHaveText('1.0.0');
  await expect(dialog.getByText('未开启', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '关闭设置' })).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(dialog.getByRole('button', { name: '版本号 1.0.0' })).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(page.getByRole('button', { name: '关闭设置' })).toBeFocused();
  await page.screenshot({ path: 'test-results/settings-desktop.png' });
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(settings).toBeFocused();
  await settings.click();
  await page.getByRole('button', { name: '关闭设置' }).click();
  await expect(dialog).toBeHidden();
  await expect(settings).toBeFocused();
});

test('单击不切换，双击切换开发者模式并在刷新后保留', async ({ page }) => {
  await page.goto('/');
  await page.evaluate(() => localStorage.setItem('unrelated-setting', 'keep'));
  const settings = page.getByRole('button', { name: '设置', exact: true });
  const version = page.getByRole('button', { name: '版本号 1.0.0' });
  await settings.click();
  await version.click();
  await expect(version).toHaveAttribute('aria-pressed', 'false');
  await expect(page.getByText('未开启', { exact: true })).toBeVisible();
  await version.dblclick();
  await expect(page.getByText('已开启', { exact: true })).toBeVisible();
  await expect(version).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('html')).toHaveAttribute('data-developer-mode', 'true');
  await page.getByRole('button', { name: '关闭设置' }).click();
  await settings.click();
  await expect(page.getByText('已开启', { exact: true })).toBeVisible();
  await page.reload();
  await settings.click();
  await expect(page.getByText('已开启', { exact: true })).toBeVisible();
  await version.dblclick();
  await expect(page.getByText('未开启', { exact: true })).toBeVisible();
  await expect(page.locator('html')).toHaveAttribute('data-developer-mode', 'false');
  await page.reload();
  await settings.click();
  await expect(version).toHaveAttribute('aria-pressed', 'false');
  expect(await page.evaluate(() => localStorage.getItem('unrelated-setting'))).toBe('keep');
});

test('键盘可以切换模式，已导入的表格保留', async ({ page }) => {
  await page.goto('/');
  await page.getByLabel('选择 Excel 文件').setInputFiles(file([['月租', '2800']]));
  await expect(page.getByRole('cell', { name: '2800', exact: true })).toBeVisible();
  await page.getByRole('button', { name: '设置', exact: true }).click();
  const version = page.getByRole('button', { name: '版本号 1.0.0' });
  await version.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByText('已开启', { exact: true })).toBeVisible();
  await page.keyboard.press('Space');
  await expect(page.getByText('未开启', { exact: true })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('cell', { name: '2800', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '发送并分析' })).toBeEnabled();
});

test('浏览器存储不可用时仍能打开设置和切换模式', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.addInitScript(() => {
    Object.defineProperty(window, 'localStorage', { get() { throw new DOMException('Storage disabled', 'SecurityError'); } });
  });
  await page.goto('/');
  await page.getByRole('button', { name: '设置', exact: true }).click();
  const version = page.getByRole('button', { name: '版本号 1.0.0' });
  await version.dblclick();
  await expect(page.getByText('已开启', { exact: true })).toBeVisible();
  await expect(page.getByText('当前浏览器无法保存设置，本次切换仅在当前页面生效。')).toBeVisible();
  await version.dblclick();
  await expect(page.getByText('未开启', { exact: true })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByLabel('选择 Excel 文件')).toBeAttached();
  expect(errors).toEqual([]);
});

test.describe('窄屏和触屏设置', () => {
  test.use({ isMobile: true, hasTouch: true });

  for (const width of [320, 390]) {
    test(`${width}px 下设置和模板入口可用，连续轻点版本可切换模式`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 });
      await page.goto('/');
      await expect(page.getByRole('link', { name: '下载空白模板' })).toBeInViewport();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await page.getByRole('button', { name: '设置', exact: true }).tap();
      const version = page.getByRole('button', { name: '版本号 1.0.0' });
      await version.tap();
      await expect(version).toHaveAttribute('aria-pressed', 'false');
      await version.tap();
      await expect(page.getByText('已开启', { exact: true })).toBeVisible();
      await expect(page.getByRole('button', { name: '关闭设置' })).toBeInViewport();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await page.screenshot({ path: `test-results/settings-mobile-${width}.png` });
      await page.getByRole('button', { name: '关闭设置' }).tap();
      await expect(page.getByRole('dialog', { name: '设置', exact: true })).toBeHidden();
    });
  }
});
