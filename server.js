// server.js - 本地服务 (Node 版, 替代原 Go 后端)
// 承载前端页面、静态资源与全部 API, 路径与响应形状与原版保持一致

const http = require('http');
const fs = require('fs');
const fsp = fs.promises;
const path = require('path');

// ---------- 常量 (与原 Go 版 config.go 一致) ----------
const DDragonBase = 'https://ddragon.leagueoflegends.com/cdn';
const CDragonBase = 'https://raw.communitydragon.org/latest/plugins/rcp-be-lol-game-data/global/zh_cn/v1';
const DefaultVersion = '16.20.1';
const DownloadConcurrency = 6;
const TypeToDir = { champion: 'champions', item: 'items', rune: 'runes', summoner: 'summoners' };
const ImageExts = ['.png', '.jpg', '.jpeg', '.webp', '.gif'];

// ---------- 运行状态 ----------
let webDir = '';   // 前端静态目录
let assetsDir = ''; // 图片缓存根目录
let cacheVersion = DefaultVersion;
// 缓存令牌: 初始取 meta/champions.json 修改时间, 每次缓存成功后变化, 附加到图片 URL 使浏览器重新加载
let cacheToken = '0';
let cacheProgress = { running: false, stage: '', done: 0, total: 0, message: '' };
let caching = false;

function setProgress(stage, done, total, message) {
  cacheProgress = { running: true, stage, done, total, message };
}

// ---------- 通用工具 ----------

function sendJSON(res, code, obj) {
  res.writeHead(code, { 'Content-Type': 'application/json; charset=utf-8' });
  res.end(JSON.stringify(obj));
}

function sendError(res, code, msg) {
  res.writeHead(code, { 'Content-Type': 'text/plain; charset=utf-8' });
  res.end(msg);
}

// GET 请求并解析 JSON (30 秒超时, 与原版一致)
async function fetchJSON(url) {
  const res = await fetch(url, { signal: AbortSignal.timeout(30000) });
  if (!res.ok) throw new Error('HTTP ' + res.status);
  return await res.json();
}

// 清理文件名中的非法字符
function sanitizeFilename(name) {
  return name.replace(/[/\\:*?"<>| ]/g, '_');
}

// 保存分类元数据到 assets/meta/{type}.json
async function saveMeta(typeDir, meta) {
  const metaDir = path.join(assetsDir, 'meta');
  await fsp.mkdir(metaDir, { recursive: true });
  await fsp.writeFile(path.join(metaDir, typeDir + '.json'), JSON.stringify(meta));
}

// ---------- 缓存下载 ----------

async function downloadImages(typeDir, keys, cnNames, urlFn) {
  const total = keys.length;
  if (total === 0) return;
  const dir = path.join(assetsDir, typeDir);
  await fsp.mkdir(dir, { recursive: true });

  let done = 0;
  let next = 0;
  async function worker() {
    while (true) {
      const i = next++;
      if (i >= total) return;
      const k = keys[i];
      const name = cnNames[k] || k;
      const dest = path.join(dir, sanitizeFilename(name) + '.png');
      try {
        const res = await fetch(urlFn(k));
        if (res.ok) {
          const buf = Buffer.from(await res.arrayBuffer());
          await fsp.writeFile(dest, buf);
        }
      } catch (e) { /* 单张失败跳过, 与原版一致 */ }
      done++;
      if (done % 5 === 0 || done === total) {
        setProgress(typeDir, done, total, `下载${typeDir} ${done}/${total}`);
      }
    }
  }
  const workers = [];
  for (let i = 0; i < DownloadConcurrency; i++) workers.push(worker());
  await Promise.all(workers);
}

// 从 Data Dragon 获取最新版本号, 失败时回退到 DefaultVersion
async function latestVersion() {
  try {
    const arr = await fetchJSON('https://ddragon.leagueoflegends.com/api/versions.json');
    if (Array.isArray(arr) && arr.length > 0 && typeof arr[0] === 'string' && arr[0]) {
      return arr[0];
    }
  } catch (e) { /* 回退默认版本 */ }
  return DefaultVersion;
}

async function step(label, fn, base) {
  try {
    await fn(base);
  } catch (e) {
    throw new Error(`${label}下载失败: ${e.message}`);
  }
}

async function cacheChampions(base) {
  setProgress('champion', 0, 0, '获取英雄列表...');
  const raw = await fetchJSON(`${base}/data/zh_CN/champion.json`);
  const data = (raw && raw.data) || {};
  const names = [];
  const cnNames = {};
  const meta = {}; // 中文名 → 职业分类 (取第一个 tag)
  for (const k of Object.keys(data)) {
    names.push(k);
    const v = data[k];
    if (!v || typeof v !== 'object') continue;
    if (typeof v.name === 'string') {
      cnNames[k] = v.name;
      if (Array.isArray(v.tags) && v.tags.length > 0 && typeof v.tags[0] === 'string') {
        meta[v.name] = v.tags[0];
      }
    }
  }
  names.sort();
  await downloadImages('champions', names, cnNames,
    (name) => `${base}/img/champion/${name}.png`);
  await saveMeta('champions', meta);
}

// 将装备官方 tags 映射为中文大类 (按优先级匹配)
function mapItemTags(tags) {
  if (!Array.isArray(tags)) return '其他';
  const has = (t) => tags.includes(t);
  // 优先级: 攻击 > 法术 > 防御 > 移动 > 辅助
  if (has('Damage') || has('ArmorPenetration') || has('CriticalStrike') ||
      has('AttackSpeed') || has('LifeSteal') || has('OnHit')) return '攻击';
  if (has('SpellDamage') || has('MagicPenetration') || has('Mana') ||
      has('ManaRegen') || has('SpellVamp')) return '法术';
  if (has('Health') || has('Armor') || has('SpellBlock') ||
      has('MagicResist') || has('HealthRegen') || has('Tenacity')) return '防御';
  if (has('Boots') || has('NonbootsMovement') || has('Movement')) return '移动';
  return '辅助';
}

async function cacheItems(base) {
  setProgress('item', 0, 0, '获取装备列表...');
  const raw = await fetchJSON(`${base}/data/zh_CN/item.json`);
  const data = (raw && raw.data) || {};
  const ids = [];
  const cnNames = {};
  const meta = {};
  for (const k of Object.keys(data)) {
    const v = data[k];
    if (!v || typeof v !== 'object') continue;
    // 仅保留召唤师峡谷(map 11)可用的装备 (排除大乱斗/无限火力/云顶/竞技场等娱乐模式)
    if (!v.maps || v.maps['11'] !== true) continue;
    // 仅保留可购买的装备 (排除任务道具/特殊物品)
    if (!v.gold || v.gold.purchasable !== true) continue;
    if (!v.image || typeof v.image.full !== 'string' || !v.image.full) continue;
    ids.push(k);
    if (typeof v.name === 'string') {
      cnNames[k] = v.name;
      meta[v.name] = mapItemTags(v.tags);
    }
  }
  ids.sort();
  await downloadImages('items', ids, cnNames,
    (id) => `${base}/img/item/${id}.png`);
  await saveMeta('items', meta);
}

async function cacheRunes(base) {
  setProgress('rune', 0, 0, '获取符文列表...');
  const raw = await fetchJSON(`${base}/data/zh_CN/runesReforged.json`);
  const trees = Array.isArray(raw) ? raw : [];
  const paths = [];
  const cnNames = {};
  const meta = {}; // 中文名 → 符文路径
  for (const t of trees) {
    if (!t || typeof t !== 'object') continue;
    const pathName = typeof t.name === 'string' ? t.name : ''; // 路径中文名 (精密/主宰/巫术/坚决/启迪)
    const slots = Array.isArray(t.slots) ? t.slots : [];
    for (const s of slots) {
      const runes = (s && Array.isArray(s.runes)) ? s.runes : [];
      for (const ru of runes) {
        if (!ru || typeof ru !== 'object') continue;
        const icon = typeof ru.icon === 'string' ? ru.icon : '';
        if (!icon) continue;
        paths.push(icon);
        if (typeof ru.name === 'string') {
          cnNames[icon] = ru.name;
          meta[ru.name] = pathName;
        }
      }
    }
  }
  await downloadImages('runes', paths, cnNames,
    (p) => `${DDragonBase}/img/${p.replace(/^\//, '')}`);
  // 属性碎片 (Stat Shards) - Data Dragon 不提供, 从 CommunityDragon 获取
  await cacheStatShards(meta);
  await saveMeta('runes', meta);
}

// 从 CommunityDragon 获取属性碎片并下载图标
async function cacheStatShards(meta) {
  setProgress('rune', 0, 0, '获取属性碎片...');
  let raw;
  try {
    raw = await fetchJSON(`${CDragonBase}/perks.json`);
  } catch (e) {
    return;
  }
  const perks = Array.isArray(raw) ? raw : [];
  const paths = [];
  const cnNames = {};
  for (const p of perks) {
    if (!p || typeof p !== 'object') continue;
    const iconPath = typeof p.iconPath === 'string' ? p.iconPath : '';
    if (!iconPath.includes('StatMods')) continue;
    const name = typeof p.name === 'string' ? p.name : '';
    if (!name) continue;
    // iconPath 形如 /lol-game-data/assets/v1/perk-images/StatMods/StatModsArmorIcon.png
    // Data Dragon 对应路径: perk-images/StatMods/StatModsArmorIcon.png
    const idx = iconPath.indexOf('perk-images/');
    if (idx < 0) continue;
    const relPath = iconPath.slice(idx);
    paths.push(relPath);
    cnNames[relPath] = name;
    meta[name] = '属性碎片';
  }
  await downloadImages('runes', paths, cnNames,
    (p) => `${DDragonBase}/img/${p}`);
}

async function cacheSummoners(base) {
  setProgress('summoner', 0, 0, '获取召唤师技能列表...');
  const raw = await fetchJSON(`${base}/data/zh_CN/summoner.json`);
  const data = (raw && raw.data) || {};
  const keys = [];
  const cnNames = {};
  for (const k of Object.keys(data)) {
    const v = data[k];
    if (!v || typeof v !== 'object') continue;
    // 仅保留经典模式(CLASSIC)可用的召唤师技能 (排除云顶/竞技场/魄罗王等娱乐模式)
    const modes = Array.isArray(v.modes) ? v.modes : [];
    if (!modes.includes('CLASSIC')) continue;
    if (!v.image || typeof v.image.full !== 'string' || !v.image.full) continue;
    keys.push(k);
    if (typeof v.name === 'string') cnNames[k] = v.name;
  }
  keys.sort();
  await downloadImages('summoners', keys, cnNames,
    (key) => `${base}/img/spell/${key}.png`);
}

// 执行完整缓存流程: 英雄 → 装备 → 符文 → 召唤师技能
async function runCache(version) {
  caching = true;
  setProgress('init', 0, 0, '开始下载...');
  const base = `${DDragonBase}/${version}`;
  try {
    await step('英雄', cacheChampions, base);
    await step('装备', cacheItems, base);
    await step('符文', cacheRunes, base);
    await step('召唤师技能', cacheSummoners, base);
    cacheProgress = { running: false, stage: 'done', done: 0, total: 0, message: '缓存完成!' };
    // 缓存成功, 更新令牌使图片 URL 变化, 强制浏览器丢弃旧缓存
    cacheToken = String(Math.floor(Date.now() / 1000));
  } catch (e) {
    cacheProgress = { running: false, stage: 'error', done: 0, total: 0, message: e.message };
  } finally {
    caching = false;
  }
}

// ---------- 静态文件 ----------

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.webp': 'image/webp',
  '.gif': 'image/gif',
  '.svg': 'image/svg+xml',
  '.ico': 'image/x-icon',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.ttf': 'font/ttf',
};

// 安全解码 URL 路径 (解码失败时使用原始路径)
function safeDecode(s) {
  try { return decodeURIComponent(s); } catch (e) { return s; }
}

async function serveFile(res, filePath) {
  let data;
  try {
    data = await fsp.readFile(filePath);
  } catch (e) {
    return false;
  }
  const ext = path.extname(filePath).toLowerCase();
  res.writeHead(200, { 'Content-Type': MIME[ext] || 'application/octet-stream' });
  res.end(data);
  return true;
}

// ---------- 图库 API ----------

function listType(typeDir) {
  const dir = path.join(assetsDir, typeDir);
  let entries;
  try {
    entries = fs.readdirSync(dir, { withFileTypes: true });
  } catch (e) {
    return [];
  }
  // 加载分类元数据 (来自官方 Data Dragon, 缓存时保存)
  let meta = {};
  try {
    meta = JSON.parse(fs.readFileSync(path.join(assetsDir, 'meta', typeDir + '.json'), 'utf8'));
  } catch (e) { /* 无元数据则分类为空 */ }
  const ver = `${cacheVersion}-${cacheToken}`;
  const items = [];
  for (const e of entries) {
    if (e.isDirectory()) continue;
    const name = e.name;
    const ext = path.extname(name).toLowerCase();
    if (!ImageExts.includes(ext)) continue;
    const cnName = name.slice(0, name.length - ext.length);
    items.push({
      name: cnName,
      url: `/assets/${typeDir}/${name}?v=${ver}`,
      category: meta[cnName] || '',
    });
  }
  items.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
  return items;
}

async function findImageFile(dir, name) {
  for (const ext of ImageExts) {
    const p = path.join(dir, name + ext);
    try {
      const st = await fsp.stat(p);
      if (st.isFile()) return p;
    } catch (e) { /* 尝试下一个扩展名 */ }
  }
  return '';
}

// ---------- 配置 API ----------

function savesPath() {
  return path.join(assetsDir, 'saves');
}

function formatModTime(d) {
  const pad = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

function invalidName(name) {
  return !name || name.includes('..') || name.includes('/') || name.includes('\\');
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    req.on('data', (c) => chunks.push(c));
    req.on('end', () => resolve(Buffer.concat(chunks)));
    req.on('error', reject);
  });
}

// ---------- 路由 ----------

async function route(req, res, pathname, query) {
  // ---- 图库 API ----
  if (pathname === '/api/library') {
    return sendJSON(res, 200, {
      champion: listType('champions'),
      item: listType('items'),
      rune: listType('runes'),
      summoner: listType('summoners'),
    });
  }

  if (pathname.startsWith('/api/library/')) {
    if (req.method !== 'DELETE') return sendError(res, 405, 'method not allowed');
    const rest = pathname.slice('/api/library/'.length);
    const parts = rest.split('/');
    if (parts.length !== 2) return sendError(res, 400, 'bad path');
    const dir = TypeToDir[parts[0]];
    if (!dir) return sendError(res, 400, 'unknown type');
    const name = parts[1];
    if (name.includes('..') || name.includes('/') || name.includes('\\')) {
      return sendError(res, 400, 'invalid name');
    }
    const p = await findImageFile(path.join(assetsDir, dir), name);
    if (!p) return sendError(res, 404, 'not found');
    try {
      await fsp.rm(p);
    } catch (e) {
      return sendError(res, 500, e.message);
    }
    return sendJSON(res, 200, { ok: true });
  }

  // ---- 缓存 API ----
  if (pathname === '/api/version') {
    return sendJSON(res, 200, { version: cacheVersion });
  }

  if (pathname === '/api/cache/status') {
    return sendJSON(res, 200, cacheProgress);
  }

  if (pathname === '/api/cache/start') {
    if (caching) return sendError(res, 409, '缓存正在进行中');
    let version = query.get('version') || '';
    if (!version) {
      // 未指定版本时自动取官方最新版本, 避免硬编码默认值过期
      version = await latestVersion();
    }
    cacheVersion = version;
    runCache(version); // 异步执行, 不等待
    return sendJSON(res, 200, { status: 'started' });
  }

  if (pathname === '/api/cache/clear') {
    if (caching) return sendError(res, 409, '缓存正在进行中, 无法清空');
    const dirs = ['champions', 'items', 'runes', 'summoners', 'meta'];
    let deleted = 0;
    for (const d of dirs) {
      try {
        await fsp.rm(path.join(assetsDir, d), { recursive: true, force: true });
        deleted++;
      } catch (e) { /* 忽略单个目录失败 */ }
    }
    return sendJSON(res, 200, {
      status: 'ok', deleted, message: `已清空 ${deleted} 个目录`,
    });
  }

  // ---- 配置保存/加载 API ----
  if (pathname === '/api/config/list') {
    let entries;
    try {
      entries = await fsp.readdir(savesPath(), { withFileTypes: true });
    } catch (e) {
      return sendJSON(res, 200, []);
    }
    const list = [];
    for (const e of entries) {
      if (e.isDirectory()) continue;
      if (!e.name.toLowerCase().endsWith('.json')) continue;
      try {
        const st = await fsp.stat(path.join(savesPath(), e.name));
        list.push({
          name: e.name.slice(0, -'.json'.length),
          size: st.size,
          modTime: formatModTime(st.mtime),
        });
      } catch (err) { /* 跳过读取失败的条目 */ }
    }
    list.sort((a, b) => (a.modTime < b.modTime ? 1 : a.modTime > b.modTime ? -1 : 0));
    return sendJSON(res, 200, list);
  }

  if (pathname === '/api/config/load') {
    const name = query.get('name') || '';
    if (!name) return sendError(res, 400, 'name required');
    if (invalidName(name)) return sendError(res, 400, 'invalid name');
    let data;
    try {
      data = await fsp.readFile(path.join(savesPath(), name + '.json'));
    } catch (e) {
      return sendError(res, 404, 'not found');
    }
    res.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8' });
    return res.end(data);
  }

  if (pathname === '/api/config/save') {
    if (req.method !== 'POST') return sendError(res, 405, 'method not allowed');
    const name = query.get('name') || '';
    if (!name) return sendError(res, 400, 'name required');
    if (invalidName(name)) return sendError(res, 400, 'invalid name');
    const body = await readBody(req);
    try {
      JSON.parse(body.toString('utf8'));
    } catch (e) {
      return sendError(res, 400, 'invalid json');
    }
    await fsp.mkdir(savesPath(), { recursive: true });
    await fsp.writeFile(path.join(savesPath(), name + '.json'), body);
    return sendJSON(res, 200, { ok: true, name });
  }

  if (pathname === '/api/config/delete') {
    if (req.method !== 'DELETE') return sendError(res, 405, 'method not allowed');
    const name = query.get('name') || '';
    if (!name) return sendError(res, 400, 'name required');
    if (invalidName(name)) return sendError(res, 400, 'invalid name');
    const p = path.join(savesPath(), name + '.json');
    try {
      await fsp.access(p);
    } catch (e) {
      return sendError(res, 404, 'not found');
    }
    try {
      await fsp.rm(p, { force: true });
    } catch (e) {
      return sendError(res, 500, e.message);
    }
    return sendJSON(res, 200, { ok: true });
  }

  // ---- 静态资源 ----
  if (pathname.startsWith('/assets/')) {
    if (pathname.includes('..')) return sendError(res, 404, '404 page not found');
    const rel = pathname.slice('/assets/'.length);
    // 防止路径穿越
    const root = path.resolve(assetsDir);
    const file = path.resolve(root, rel);
    if (file !== root && !file.startsWith(root + path.sep)) {
      return sendError(res, 404, '404 page not found');
    }
    if (await serveFile(res, file)) return;
    return sendError(res, 404, '404 page not found');
  }

  // 前端页面
  if (pathname.includes('..')) return sendError(res, 404, '404 page not found');
  const rel = pathname === '/' ? 'index.html' : pathname.slice(1);
  if (await serveFile(res, path.join(webDir, rel))) return;
  sendError(res, 404, '404 page not found');
}

async function handler(req, res) {
  try {
    const u = new URL(req.url, 'http://127.0.0.1');
    const pathname = safeDecode(u.pathname);
    await route(req, res, pathname, u.searchParams);
  } catch (e) {
    if (!res.headersSent) {
      sendError(res, 500, String((e && e.message) || e));
    } else {
      res.end();
    }
  }
}

// 启动服务, 返回实际端口 (优先 8765, 被占用时使用随机端口)
function startServer(opts) {
  webDir = opts.webDir;
  assetsDir = opts.assetsDir;
  if (!webDir || !assetsDir) throw new Error('startServer 需要 webDir 与 assetsDir 参数');
  // 用元数据文件修改时间作为初始 token, 保证重启后已更新的图片 URL 不同于旧缓存
  try {
    const st = fs.statSync(path.join(assetsDir, 'meta', 'champions.json'));
    cacheToken = String(Math.floor(st.mtimeMs / 1000));
  } catch (e) {
    cacheToken = '0';
  }

  const server = http.createServer(handler);
  const listenOn = (port) => new Promise((resolve, reject) => {
    const onError = (err) => {
      if (port !== 0) {
        resolve(listenOn(0)); // 回退到随机端口
      } else {
        reject(err);
      }
    };
    server.once('error', onError);
    server.listen(port, '127.0.0.1', () => {
      server.removeListener('error', onError);
      resolve(server.address().port);
    });
  });
  return listenOn(8765);
}

module.exports = { startServer };
