// Minimal dependency-free Chrome DevTools Protocol smoke driver.
// Invoked by: go test -tags=browser ./internal/serve -run BrowserSmoke
import { spawn } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { once } from 'node:events';
import process from 'node:process';

const [baseURL, chromePath, profileDir] = process.argv.slice(2);
if (!baseURL || !chromePath || !profileDir) {
  throw new Error('usage: browser-smoke.mjs BASE_URL CHROME_PATH PROFILE_DIR');
}

const chrome = spawn(chromePath, [
  '--headless=new',
  '--remote-debugging-port=0',
  `--user-data-dir=${profileDir}`,
  '--no-first-run',
  '--no-default-browser-check',
  '--disable-background-networking',
  '--disable-component-update',
  '--disable-sync',
  'about:blank',
], { stdio: ['ignore', 'ignore', 'pipe'] });

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function retry(fn, timeoutMs, label) {
  const deadline = Date.now() + timeoutMs;
  let last;
  while (Date.now() < deadline) {
    try { return await fn(); } catch (err) { last = err; }
    await sleep(50);
  }
  throw new Error(`${label}: ${last || 'timed out'}`);
}

let ws;
let closed = false;
try {
  const active = await retry(
    async () => (await readFile(`${profileDir}/DevToolsActivePort`, 'utf8')).trim().split('\n'),
    10000,
    'Chrome did not expose DevToolsActivePort',
  );
  const port = active[0];
  const target = await retry(async () => {
    const response = await fetch(`http://127.0.0.1:${port}/json/new?${encodeURIComponent(baseURL)}`, { method: 'PUT' });
    if (!response.ok) throw new Error(`target create returned ${response.status}`);
    return response.json();
  }, 5000, 'could not create Chrome target');

  ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true });
    ws.addEventListener('error', reject, { once: true });
  });

  let nextID = 1;
  const pending = new Map();
  const eventWaiters = new Map();
  let tileLoaded = false;
  const localResponses = [];
  ws.addEventListener('message', ({ data }) => {
    const message = JSON.parse(data);
    if (message.id) {
      const waiter = pending.get(message.id);
      pending.delete(message.id);
      if (message.error) waiter.reject(new Error(message.error.message));
      else waiter.resolve(message.result);
      return;
    }
    if (message.method === 'Network.responseReceived') {
      const { url, status } = message.params.response;
      if (url.startsWith(baseURL)) localResponses.push(`${status} ${url}`);
      if (status === 200 && url.includes('/0001/') && url.endsWith('/default.jpg')) tileLoaded = true;
    }
    const waiters = eventWaiters.get(message.method) || [];
    eventWaiters.delete(message.method);
    for (const resolve of waiters) resolve(message.params);
  });
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const id = nextID++;
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params }));
  });
  const event = (method) => new Promise((resolve) => {
    eventWaiters.set(method, [...(eventWaiters.get(method) || []), resolve]);
  });
  const evaluate = async (expression) => {
    const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.text || 'browser evaluation failed');
    return result.result.value;
  };
  const navigate = async (url) => {
    const loaded = event('Page.loadEventFired');
    await send('Page.navigate', { url });
    await loaded;
  };

  await send('Page.enable');
  await send('Runtime.enable');
  await send('Network.enable');
  await navigate(baseURL);

  const searchResult = await evaluate(`(() => {
    const input = document.getElementById('catalog-search');
    input.value = 'definitely absent';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    const hidden = [...document.querySelectorAll('article.entry')].filter((e) => e.hidden).length;
    input.value = 'browser smoke manuscript';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    const visible = [...document.querySelectorAll('article.entry')].filter((e) => !e.hidden).length;
    return { hidden, visible, href: document.querySelector('article.entry a.title')?.getAttribute('href') };
  })()`);
  if (searchResult.hidden !== 1 || searchResult.visible !== 1 || !searchResult.href) {
    throw new Error(`catalogue search failed: ${JSON.stringify(searchResult)}`);
  }

  await navigate(new URL(searchResult.href, baseURL).href);
  try {
    await retry(async () => {
      const ready = await evaluate(`Boolean(document.querySelector('#mirador') && window.Mirador)`);
      if (!ready) throw new Error('Mirador not ready');
      if (!tileLoaded) throw new Error('tile not loaded');
      return true;
    }, 20000, 'viewer did not initialize and load a tile');
  } catch (err) {
    const dom = await evaluate(`({ canvases: document.querySelectorAll('canvas').length, text: document.body.innerText.slice(0, 500) })`);
    throw new Error(`${err.message}; responses=${JSON.stringify(localResponses)}; dom=${JSON.stringify(dom)}`);
  }

  const annotationResult = await evaluate(`(async () => {
    const endpoint = location.pathname.replace(/\/$/, '') + '/annotations';
    const annotation = {
      id: 'urn:browser-smoke:annotation', type: 'Annotation', motivation: 'commenting',
      body: { type: 'TextualBody', value: 'browser smoke note' },
      target: 'https://example.test/canvas/1'
    };
    const created = await fetch(endpoint, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(annotation)
    });
    const page = await (await fetch(endpoint)).json();
    return { status: created.status, found: page.items.some((item) => item.id === annotation.id) };
  })()`);
  if (annotationResult.status !== 201 || !annotationResult.found) {
    throw new Error(`browser annotation round-trip failed: ${JSON.stringify(annotationResult)}`);
  }

  process.stdout.write(JSON.stringify({ search: searchResult, tileLoaded, annotation: annotationResult }));
  await send('Browser.close');
  closed = true;
} finally {
  if (ws && ws.readyState === WebSocket.OPEN) ws.close();
  if (!closed) chrome.kill('SIGTERM');
  if (chrome.exitCode === null) {
    await Promise.race([once(chrome, 'exit'), sleep(3000)]);
    if (chrome.exitCode === null) chrome.kill('SIGKILL');
  }
}
