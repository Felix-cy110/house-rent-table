import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync } from 'node:fs';
import { createServer } from 'node:net';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const windows = process.platform === 'win32';
const children = new Set();
let stopping = false;

function start(command, args, name) {
  const child = spawn(command, args, {
    cwd: root,
    stdio: ['ignore', 'inherit', 'inherit'],
    windowsHide: true,
    // Unix process groups let us also stop build tools and Vite's children.
    detached: !windows,
  });
  children.add(child);
  child.done = new Promise((resolve, reject) => {
    child.once('error', (error) => {
      children.delete(child);
      reject(new Error(`${name}启动失败：${error.message}`));
    });
    child.once('exit', (code, signal) => {
      children.delete(child);
      resolve({ code, signal });
    });
  });
  return child;
}

async function run(command, args, name) {
  const { code, signal } = await start(command, args, name).done;
  if (stopping) return;
  if (code !== 0) throw new Error(`${name}失败（${signal ?? `退出码 ${code}`}）`);
}

function killTree(child, force = false) {
  if (!child.pid) return;
  if (windows) {
    if (child.exitCode !== null || child.signalCode !== null) return;
    spawnSync('taskkill.exe', ['/PID', String(child.pid), '/T', '/F'], {
      stdio: 'ignore',
      windowsHide: true,
      timeout: 5000,
    });
  } else {
    try {
      process.kill(-child.pid, force ? 'SIGKILL' : 'SIGINT');
    } catch (error) {
      if (error.code !== 'ESRCH') console.error(error.message);
    }
  }
}

async function stop(code) {
  if (stopping) return;
  stopping = true;
  console.log('\n[dev-gui] 正在停止前后端……');
  const running = [...children];
  for (const child of running) killTree(child);
  await Promise.race([
    Promise.allSettled(running.map((child) => child.done)),
    delay(5000),
  ]);
  for (const child of running) killTree(child, true);
  process.exit(code);
}

function fail(error) {
  if (stopping) return;
  console.error(`[dev-gui] ${error.message}`);
  void stop(1);
}

function serve(command, args, name) {
  const child = start(command, args, name);
  void child.done.then(({ code, signal }) => {
    fail(new Error(`${name}已退出（${signal ?? `退出码 ${code}`}）`));
  }, fail);
}

function checkPort(port) {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once('error', (error) => {
      reject(new Error(error.code === 'EADDRINUSE'
        ? `端口 ${port} 已被占用，请先停止占用该端口的服务。`
        : `无法使用端口 ${port}：${error.message}`));
    });
    server.listen({ host: '127.0.0.1', port, exclusive: true }, () => server.close(resolve));
  });
}

async function waitReady(url) {
  const deadline = Date.now() + 30000;
  while (!stopping && Date.now() < deadline) {
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(1000) });
      await response.body?.cancel();
      if (response.ok) return;
    } catch {
      // A service may take a moment to start listening.
    }
    await delay(200);
  }
  if (!stopping) throw new Error(`等待服务就绪超时：${url}`);
}

async function main() {
  await checkPort(8080);
  await checkPort(5173);
  if (stopping) return;

  const vite = join(root, 'web', 'node_modules', 'vite', 'bin', 'vite.js');
  if (!existsSync(vite)) {
    console.log('[dev-gui] 首次运行，正在安装前端依赖……');
    if (windows) {
      // npm is a .cmd script on Windows; keep the shell command static.
      await run(process.env.ComSpec || 'cmd.exe', ['/d', '/s', '/c', 'npm --prefix web ci'], '前端依赖安装');
    } else {
      await run('npm', ['--prefix', 'web', 'ci'], '前端依赖安装');
    }
  }
  if (stopping) return;

  const buildDir = join(root, '.work', 'dev-gui');
  mkdirSync(buildDir, { recursive: true });
  const backend = join(buildDir, windows ? 'server.exe' : 'server');
  console.log('[dev-gui] 正在编译后端……');
  // Run the compiled server directly so stopping it cannot orphan a go-run child.
  await run('go', ['build', '-o', backend, './cmd/server'], '后端编译');
  if (stopping) return;

  serve(backend, [], '后端');
  await waitReady('http://127.0.0.1:8080/api/health');
  if (stopping) return;
  serve(process.execPath, [
    vite, join(root, 'web'), '--host', '127.0.0.1', '--port', '5173', '--strictPort', '--clearScreen', 'false',
  ], '前端');
  await waitReady('http://127.0.0.1:5173');
  if (!stopping) {
    console.log('\n[dev-gui] 启动完成：http://127.0.0.1:5173');
    console.log('[dev-gui] 按 Ctrl+C 同时停止前后端。');
  }
}

process.on('SIGINT', () => void stop(0));
process.on('SIGTERM', () => void stop(0));
process.on('SIGHUP', () => void stop(0));
main().catch(fail);
