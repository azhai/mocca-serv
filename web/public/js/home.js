// 浏览应用（首页，挂 /）。由 index.html 以 <script type="module" src="/js/home.js"> 加载。
//
// 与后台（js/app.js，挂 /admin/）的关系：
//   - 共用样式（css/styles.css 的 M3 令牌）、共用内嵌目录、共用 js/static-hash.js；
//   - 共用 localStorage 里的令牌（mocca_token），从后台跳过来不用重新登录；
//   - 只调公开/只读接口（fs/list、fs/get），所以普通用户也能用 ——
//     能看到什么由服务端的 base_path 与目录密码决定，前端不做任何过滤假设。
import m from './mithril.js';
import { staticHash } from './static-hash.js';

/* ── 图标 ────────────────────────────────────────────────
   内嵌 SVG 路径，不引图标字体也不走 CDN（断网、无外网也要能用）。 */
const I = {
  menu: 'M3 6h18v2H3V6zm0 5h18v2H3v-2zm0 5h18v2H3v-2z',
  search: 'M15.5 14h-.79l-.28-.27A6.47 6.47 0 0 0 16 9.5A6.5 6.5 0 1 0 9.5 16c1.61 0 3.09-.59 4.23-1.57l.27.28v.79l5 4.99L20.49 19l-4.99-5zm-6 0A4.5 4.5 0 1 1 14 9.5A4.5 4.5 0 0 1 9.5 14z',
  list: 'M3 13h2v-2H3v2zm0 4h2v-2H3v2zm0-8h2V7H3v2zm4 4h14v-2H7v2zm0 4h14v-2H7v2zM7 7v2h14V7H7z',
  grid: 'M3 3h8v8H3V3zm10 0h8v8h-8V3zM3 13h8v8H3v-8zm10 0h8v8h-8v-8z',
  refresh: 'M17.65 6.35A7.95 7.95 0 0 0 12 4a8 8 0 1 0 7.73 10h-2.08A6 6 0 1 1 12 6c1.66 0 3.14.69 4.22 1.78L13 11h7V4l-2.35 2.35z',
  right: 'M10 6 8.59 7.41 13.17 12l-4.58 4.59L10 18l6-6z',
  down: 'M16.59 8.59 12 13.17 7.41 8.59 6 10l6 6 6-6z',
  left: 'M15.41 7.41 14 6l-6 6 6 6 1.41-1.41L10.83 12z',
  close: 'M19 6.41 17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z',
  dir: 'M10 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V8c0-1.1-.9-2-2-2h-8l-2-2z',
  // 挂载点专用：存储设备（两层盘的机架），与文件夹明显区分 ——
  // 挂载点是「另一个存储的入口」，不是当前存储里的目录，图标不该长得一样。
  mount: 'M20 13H4c-.55 0-1 .45-1 1v6c0 .55.45 1 1 1h16c.55 0 1-.45 1-1v-6c0-.55-.45-1-1-1zM7 19c-1.1 0-2-.9-2-2s.9-2 2-2 2 .9 2 2-.9 2-2 2zM20 3H4c-.55 0-1 .45-1 1v6c0 .55.45 1 1 1h16c.55 0 1-.45 1-1V4c0-.55-.45-1-1-1zM7 9c-1.1 0-2-.9-2-2s.9-2 2-2 2 .9 2 2-.9 2-2 2z',
  video: 'M18 4l2 4h-3l-2-4h-2l2 4h-3l-2-4H8l2 4H7L5 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V4h-4z',
  audio: 'M12 3v10.55A4 4 0 1 0 14 17V7h4V3h-6z',
  image: 'M21 19V5c0-1.1-.9-2-2-2H5c-1.1 0-2 .9-2 2v14c0 1.1.9 2 2 2h14c1.1 0 2-.9 2-2zM8.5 13.5l2.5 3.01L14.5 12l4.5 6H5l3.5-4.5z',
  text: 'M14 2H6c-1.1 0-2 .9-2 2v16c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V8l-6-6zm2 16H8v-2h8v2zm0-4H8v-2h8v2zm-3-5V3.5L18.5 9H13z',
  file: 'M6 2c-1.1 0-2 .9-2 2v16c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V8l-6-6H6zm7 7V3.5L18.5 9H13z',
  zoomIn: 'M15.5 14h-.79l-.28-.27A6.47 6.47 0 0 0 16 9.5A6.5 6.5 0 1 0 9.5 16c1.61 0 3.09-.59 4.23-1.57l.27.28v.79l5 4.99L20.49 19l-4.99-5zM9.5 14A4.5 4.5 0 1 1 14 9.5A4.5 4.5 0 0 1 9.5 14zM12 10h-2v-1.5H8.5V10H7v1.5h1.5V13H10v-1.5h2z',
  zoomOut: 'M15.5 14h-.79l-.28-.27A6.47 6.47 0 0 0 16 9.5A6.5 6.5 0 1 0 9.5 16c1.61 0 3.09-.59 4.23-1.57l.27.28v.79l5 4.99L20.49 19l-4.99-5zM9.5 14A4.5 4.5 0 1 1 14 9.5A4.5 4.5 0 0 1 9.5 14zM7 9.5h5V11H7z',
  reset: 'M12 5V1L7 6l5 5V7a6 6 0 1 1-6 6H4a8 8 0 1 0 8-8z',
  // 在新标签页打开原图：预览层（浮动层）里看过后，想直接进浏览器看大图就用它
  open: 'M19 19H5V5h7V3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7h-2v7zM14 3v2h3.59l-9.3 9.29 1.42 1.42L19 6.41V10h2V3h-7z',
};

// 媒体类型，取值与 models/media.go 的 MediaKind 一一对应
const KIND = { dir: 0, unknown: 1, video: 2, audio: 3, text: 4, image: 5 };

const state = {
  token: localStorage.getItem('mocca_token') || '',
  base: location.origin + '/api',
  path: '/',
  entries: [],
  loading: false,
  err: '',
  query: '',
  view: localStorage.getItem('mocca_view') === 'grid' ? 'grid' : 'list',
  drawer: false,
  tree: {},   // 路径 → { open, kids: null|[{name,path}], loading }
  pwd: {},    // 路径 → 该目录（或其上层）密码的静态哈希
  guest: false, // 选择「以游客浏览」后为 true：没有令牌也进主界面（能否真看到内容由服务端定）
  player: null, // { kind:'audio'|'video', url, name }
  lb: null,   // 图片预览 { imgs:[{name,path}], i, zoom, x, y, url, loading }
  toast: '', toastErr: false,
  urlOf: new Map(), // 路径 → 可直接播放/预览的地址（服务端 raw_url 口径）
};

/* ── 基础工具 ───────────────────────────────────────── */
function say(msg, isErr = false) {
  state.toast = msg; state.toastErr = !!isErr; m.redraw();
  setTimeout(() => { if (state.toast === msg) { state.toast = ''; m.redraw(); } }, 4000);
}

function normPath(p) {
  const s = String(p || '').split('/').filter(Boolean).join('/');
  return '/' + s;
}
function joinPath(dir, name) { return dir === '/' ? '/' + name : dir + '/' + name; }
function parentPath(p) {
  const i = normPath(p).lastIndexOf('/');
  return i <= 0 ? '/' : p.slice(0, i);
}
function baseName(p) { return normPath(p).split('/').filter(Boolean).pop() || '/'; }

function fmtSize(n) {
  if (!n) return '—';
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0, v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return (i === 0 ? v : v.toFixed(v < 10 ? 1 : 0)) + ' ' + u[i];
}
function fmtTime(s) {
  if (!s) return '—';
  const d = new Date(s);
  if (isNaN(d)) return s;
  const p = x => String(x).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}
function kindClass(type, isDir, isMount) {
  if (isMount) return 'mount'; // 挂载点先判：它也是目录，但要用另一个图标与配色
  if (isDir) return 'dir';
  if (type === KIND.video) return 'video';
  if (type === KIND.audio) return 'audio';
  if (type === KIND.image) return 'image';
  if (type === KIND.text) return 'text';
  return 'other';
}
function kindIcon(type, isDir, isMount) {
  if (isMount) return I.mount;
  if (isDir) return I.dir;
  if (type === KIND.video) return I.video;
  if (type === KIND.audio) return I.audio;
  if (type === KIND.image) return I.image;
  if (type === KIND.text) return I.text;
  return I.file;
}

/* ── 接口调用 ───────────────────────────────────────────
   信封约定与后台一致：HTTP 恒为 200，成败只看 code。 */
async function api(path, { method = 'GET', body } = {}) {
  const headers = {};
  if (state.token) headers['Authorization'] = state.token;
  if (body) { headers['Content-Type'] = 'application/json'; body = JSON.stringify(body); }
  const env = await (await fetch(state.base + path, { method, headers, body })).json();
  if (env.code === 401) {
    // 令牌过期/失效：清掉并把界面切回「去后台登录」，不要留在一个永远报错的空壳里
    state.token = '';
    localStorage.removeItem('mocca_token');
    throw new Error(env.message || '登录已失效，请重新登录');
  }
  if (env.code !== 200) throw new Error(env.message || '请求失败');
  return env.data;
}

async function guard(fn) {
  try { return await fn(); }
  catch (e) { say(e.message, true); }
  finally { m.redraw(); }
}

// pwdFor 目录密码：从目标路径逐级向上找最近设过的那个。
// 服务端也是逐级向上找最近的受保护目录，所以父目录解过一次，子目录都能复用。
function pwdFor(p) {
  for (let cur = normPath(p); ; cur = parentPath(cur)) {
    if (state.pwd[cur]) return state.pwd[cur];
    if (cur === '/') return '';
  }
}

// listDir 列目录；遇到「需要密码」时问一次，带上后重试。
// 密码在前端先做静态哈希（与服务端目录密码同一套约定），明文不出浏览器。
async function listDir(p) {
  const body = { path: p };
  const pw = pwdFor(p);
  if (pw) body.password = pw;
  try {
    return await api('/fs/list', { method: 'POST', body });
  } catch (e) {
    if (!/需要密码/.test(e.message)) throw e;
    const plain = prompt(`目录 ${p} 需要密码：`);
    if (!plain) throw new Error('已取消');
    const hash = await staticHash(plain);
    const d = await api('/fs/list', { method: 'POST', body: { path: p, password: hash } });
    state.pwd[p] = hash;
    return d;
  }
}

// driveURL 取可直接播放/预览的地址。
// 用 /fs/get 的 raw_url 而不是自己拼 /d：URL 形态归服务端管（含令牌、含受保护目录）。
// 受保护目录的密码不在 raw_url 里（服务端取流要 ?password=），这里补上。
async function driveURL(p) {
  if (state.urlOf.has(p)) return state.urlOf.get(p);
  const body = { path: p };
  const pw = pwdFor(p);
  if (pw) body.password = pw;
  const d = await api('/fs/get', { method: 'POST', body });
  let url = d.raw_url || ('/d' + encPath(p));
  if (/^https?:/i.test(url)) { /* 上游直链，原样返回 */ }
  else {
    if (pw) url += (url.includes('?') ? '&' : '?') + 'password=' + encodeURIComponent(pw);
    url = location.origin + url;
  }
  state.urlOf.set(p, url);
  return url;
}

// encPath 逐段编码路径：文件名里的空格、中文、#?% 都要编码，否则取流会取错文件。
// （不整体 encodeURI：那样会连分隔符一起动。）
function encPath(p) {
  return '/' + normPath(p).split('/').filter(Boolean).map(encodeURIComponent).join('/');
}

// directURL 直接拼取流地址，只给网格里的缩略图用：一屏几十张图，
// 每张都调一次 /fs/get 太浪费。取不到就静默失败（onerror 后只留图标）。
function directURL(p) {
  const q = new URLSearchParams();
  if (state.token) q.set('token', state.token);
  const pw = pwdFor(p);
  if (pw) q.set('password', pw);
  const s = q.toString();
  return '/d' + encPath(p) + (s ? '?' + s : '');
}

/* ── 目录导航 ─────────────────────────────────────────── */
async function openDir(p) {
  const target = normPath(p);
  state.path = target;
  state.loading = true; state.err = ''; m.redraw();
  try {
    const d = await listDir(target);
    state.entries = d.content || [];
  } catch (e) {
    state.entries = [];
    state.err = e.message;
    if (!state.token) state.token = '';
  } finally {
    state.loading = false; m.redraw();
  }
  document.title = target === '/' ? 'Mocca 浏览' : baseName(target) + ' · Mocca 浏览';
  guard(() => revealPath(target));
}

// go 只负责改 hash，真正的加载交给 hashchange —— 这样刷新、前进后退、深链都是同一条路径。
function go(p) {
  const target = normPath(p);
  state.query = '';
  state.drawer = false;
  if (location.hash.replace(/^#/, '') === target) { guard(() => openDir(target)); return; }
  location.hash = target;
}

function pathFromHash() {
  let h = location.hash.replace(/^#/, '');
  try { h = decodeURI(h); } catch { /* 非法编码就按原样用 */ }
  return h.startsWith('/') ? normPath(h) : '/';
}

/* ── 目录树（懒加载：点开哪层才拉哪层）────────────────── */
function node(p) {
  if (!state.tree[p]) state.tree[p] = { open: false, kids: null, loading: false };
  return state.tree[p];
}

async function loadKids(p) {
  const n = node(p);
  if (n.kids || n.loading) return;
  n.loading = true; m.redraw();
  try {
    const d = await listDir(p);
    // 树只放目录：文件在右侧列表里看。mount 一路带下去，树里也才能用挂载点图标。
    n.kids = (d.content || []).filter(e => e.is_dir)
      .map(e => ({ name: e.name, path: joinPath(p, e.name), mount: !!e.mount }));
  } catch (e) {
    n.kids = [];
    say(e.message, true);
  } finally {
    n.loading = false; m.redraw();
  }
}

async function toggleNode(p) {
  const n = node(p);
  n.open = !n.open;
  m.redraw();
  if (n.open) await loadKids(p);
}

// revealPath 展开当前路径的各级祖先，让树里能看到自己在哪。
// 不展开目标目录本身：它的内容已经在右侧列表里了，再拉一次纯属浪费。
// 最多只展开两层：根目录（第 0 层）+ 第一层（自动展开）+ 第二层（可见但不再展开、也不显示展开图标）。
// 更深的目录不再自动摊开 —— 否则导航到深处时整棵目录树会一路展开，既难看又拖慢。
// 需要看更深层时走面包屑或右侧列表进入即可（树里第二层以下不提供展开入口）。
async function revealPath(p) {
  const segs = normPath(p).split('/').filter(Boolean);
  node('/').open = true;
  await loadKids('/');
  let cur = '/';
  const maxExpand = 1; // 只自动展开到「第一层」(depth 1)，从而让第二层(depth 2)可见；更深层不再展开
  for (let d = 0; d < Math.min(segs.length - 1, maxExpand); d++) {
    cur = joinPath(cur, segs[d]);
    node(cur).open = true;
    await loadKids(cur);
  }
  m.redraw();
}

/* ── 媒体播放与图片预览 ───────────────────────────────── */
async function play(p, entry) {
  const url = await driveURL(p);
  state.player = { kind: entry.type === KIND.video ? 'video' : 'audio', url, name: entry.name };
  m.redraw();
}
function closePlayer() {
  state.player = null;
  m.redraw();
}

// 图片预览：左/右切换只在**当前目录的图片**里循环，切换时按需取地址。
async function viewImage(p) {
  const imgs = state.entries.filter(e => e.type === KIND.image)
    .map(e => ({ name: e.name, path: joinPath(state.path, e.name) }));
  const i = imgs.findIndex(x => x.path === p);
  state.lb = { imgs, i: i < 0 ? 0 : i, zoom: 1, x: 0, y: 0, url: '', loading: true };
  m.redraw();
  await loadLB();
}
async function loadLB() {
  const lb = state.lb;
  if (!lb || !lb.imgs.length) return;
  lb.loading = true; lb.zoom = 1; lb.x = 0; lb.y = 0; m.redraw();
  try { lb.url = await driveURL(lb.imgs[lb.i].path); }
  catch (e) { say(e.message, true); }
  finally { lb.loading = false; m.redraw(); }
}
function lbStep(d) {
  const lb = state.lb;
  if (!lb || lb.imgs.length < 2) return;
  lb.i = (lb.i + d + lb.imgs.length) % lb.imgs.length;
  guard(loadLB);
}
function lbZoom(d) {
  const lb = state.lb;
  if (!lb) return;
  lb.zoom = Math.min(5, Math.max(1, +(lb.zoom + d).toFixed(2)));
  if (lb.zoom === 1) { lb.x = 0; lb.y = 0; }
  m.redraw();
}
function closeLB() { state.lb = null; m.redraw(); }

// 拖拽平移（鼠标与触摸共用一套状态）：只在放大后生效
let drag = null;
function dragStart(x, y) {
  if (!state.lb || state.lb.zoom <= 1) return false;
  drag = { x, y, ox: state.lb.x, oy: state.lb.y };
  return true;
}
function dragMove(x, y) {
  if (!drag || !state.lb) return;
  state.lb.x = drag.ox + (x - drag.x);
  state.lb.y = drag.oy + (y - drag.y);
  m.redraw();
}
function dragEnd() { drag = null; }

/* ── 视图 ─────────────────────────────────────────────── */
const TopBar = {
  view() {
    return m('.topbar', [
      m('button.hamburger', { onclick: () => { state.drawer = !state.drawer; m.redraw(); }, 'aria-label': '目录树' },
        m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.menu }))),
      m('.brand', [
        m('img', { src: 'logo-64.png', alt: 'Mocca' }),
        m('b', 'Mocca 浏览'),
        // 与后台互指：两边是同目录下的静态页、共用同一份令牌，来回跳不用重新登录
        m('a.browse-link', { href: 'admin/index.html', title: '管理后台：挂载点、用户、元数据、上传' }, '管理后台'),
      ]),
      // 搜索框：回车时若以 / 开头就按路径跳转，否则实时过滤当前目录
      m('.search', [
        m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.search })),
        m('input[type=search]', {
          placeholder: '搜索本目录（输入 / 开头的路径可直接跳转）',
          value: state.query,
          oninput: e => { state.query = e.target.value; },
          onkeydown: e => {
            if (e.key !== 'Enter') return;
            const q = state.query.trim();
            if (q.startsWith('/')) go(q);
          },
        }),
      ]),
      m('.viewtoggle', [
        m('button', {
          class: state.view === 'list' ? 'on' : '',
          onclick: () => { state.view = 'list'; localStorage.setItem('mocca_view', 'list'); },
        }, '列表'),
        m('button', {
          class: state.view === 'grid' ? 'on' : '',
          onclick: () => { state.view = 'grid'; localStorage.setItem('mocca_view', 'grid'); },
        }, '网格'),
      ]),
      m('button.iconbtn.solid', { title: '刷新', onclick: () => guard(() => { state.urlOf.clear(); return openDir(state.path); }) },
        m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.refresh }))),
    ]);
  },
};

const TreeNode = {
  view(vnode) {
    const { name, path: p, mount, depth = 1 } = vnode.attrs;
    const n = node(p);
    const hasKids = n.kids === null || n.kids.length > 0;
    // 最多展开到第二层：depth 0/1 显示展开图标并允许展开，depth ≥ 2 不再提供展开入口（也不显示图标）。
    // 用 visibility:hidden 的占位按钮顶住 22px 宽度，保证有/无图标的节点名称左对齐。
    const canExpand = hasKids && depth < 2;
    return m('li', [
      m('.tnode', { class: state.path === p ? 'on' : '' }, [
        m('button.tcaret', {
          class: canExpand ? '' : 'empty',
          title: canExpand ? (n.open ? '收起' : '展开') : '',
          disabled: !canExpand,
          onclick: canExpand ? (e => { e.stopPropagation(); guard(() => toggleNode(p)); }) : null,
        }, m('svg', { viewBox: '0 0 24 24' }, m('path', { d: n.open ? I.down : I.right }))),
        // 挂载点与普通目录在树里也要一眼分开：图标不同、配色不同
        m('.kind.tiny', { class: kindClass(0, true, mount) },
          m('svg', { viewBox: '0 0 24 24' }, m('path', { d: kindIcon(0, true, mount) }))),
        m('button.tname', { title: mount ? `${name}（挂载点）` : name, onclick: () => go(p) }, name),
      ]),
      n.open ? m('ul', (n.kids || []).map(k =>
        m(TreeNode, { key: k.path, name: k.name, path: k.path, mount: k.mount, depth: depth + 1 }))) : null,
    ]);
  },
};

const Tree = {
  view() {
    const root = node('/');
    root.open = true;
    return m('.sidebar', { class: state.drawer ? 'open' : '' }, [
      m('h2', '目录'),
      m('ul.tree', [
        m('li', [
          m('.tnode', { class: state.path === '/' ? 'on' : '' }, [
            m('button.tcaret', {
              title: root.open ? '收起' : '展开',
              onclick: e => { e.stopPropagation(); guard(() => toggleNode('/')); },
            }, m('svg', { viewBox: '0 0 24 24' }, m('path', { d: root.open ? I.down : I.right }))),
            m('.kind.tiny', { class: kindClass(0, true, false) },
              m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.dir }))),
            m('button.tname', { onclick: () => go('/') }, '根目录 /'),
          ]),
          root.open ? m('ul', (root.kids || []).map(k => m(TreeNode, { key: k.path, name: k.name, path: k.path, depth: 1 }))) : null,
        ]),
      ]),
      root.loading && !root.kids ? m('p.muted', { style: 'padding:0 10px' }, '加载中…') : null,
      root.kids && root.kids.length === 0 && !state.loading && state.path === '/'
        ? m('p.muted', { style: 'padding:0 10px' }, '还没有配置挂载点')
        : null,
    ]);
  },
};

const Crumbs = {
  view() {
    const segs = state.path.split('/').filter(Boolean);
    const nodes = [{ name: '根目录', path: '/' }];
    let cur = '/';
    for (const s of segs) { cur = joinPath(cur, s); nodes.push({ name: s, path: cur }); }
    return m('.crumbsrow', [
      m('.crumbs', nodes.flatMap((n, i) => {
        const isLast = i === nodes.length - 1;
        const el = isLast
          ? m('span.cur', n.name)
          : m('button', { onclick: () => go(n.path) }, n.name);
        return i === 0 ? [el] : [m('span.sep', '/'), el];
      })),
      m('span.muted', { style: 'margin-left:auto' },
        state.loading ? '加载中…' : (state.query && !state.query.startsWith('/')
          ? `${visible().length} / ${state.entries.length} 项`
          : `${state.entries.length} 项`)),
    ]);
  },
};

// visible 当前目录下的条目（搜索框只过滤这一层，不递归 —— 服务端没有递归搜索接口）
function visible() {
  const q = state.query.trim().toLowerCase();
  if (!q || q.startsWith('/')) return state.entries;
  return state.entries.filter(e => e.name.toLowerCase().includes(q));
}

function onOpen(entry) {
  const p = joinPath(state.path, entry.name);
  if (entry.is_dir) return go(p);
  if (entry.type === KIND.video || entry.type === KIND.audio) return guard(() => play(p, entry));
  if (entry.type === KIND.image) return guard(() => viewImage(p));
  say('这个类型暂不支持预览，可直接下载');
}

const Files = {
  view() {
    const list = visible();
    if (state.err) {
      return m('.files', m('.empty', [
        m('p.err', state.err),
        m('button.ghost', { onclick: () => guard(() => openDir(state.path)) }, '重试'),
      ]));
    }
    if (!list.length) {
      return m('.files', m('.empty', state.loading ? '加载中…'
        : (state.query ? `本目录没有匹配「${state.query}」的条目` : '这个目录是空的')));
    }
    if (state.view === 'grid') {
      return m('.files', m('.cards', list.map(e => {
        const p = joinPath(state.path, e.name);
        return m('.card', { key: e.name, onclick: () => onOpen(e), title: e.name }, [
          m('.thumb', e.type === KIND.image
            // 图片直接用取流地址当缩略图；取不到就回落到图标（不弹错）
            ? m('img', { src: directURL(p), alt: e.name, loading: 'lazy', onerror: ev => { ev.target.style.display = 'none'; } })
            : m('div.kind.big', { class: kindClass(e.type, e.is_dir, e.mount) },
                m('svg', { viewBox: '0 0 24 24' }, m('path', { d: kindIcon(e.type, e.is_dir, e.mount) })))),
          m('.cn', e.name),
          m('.cs', e.is_dir ? '目录' : fmtSize(e.size)),
        ]);
      })));
    }
    return m('.files', m('table.filetable', [
      // colgroup 固定列宽：名称自适应剩余宽度，大小/修改时间定宽。
      // table-layout: fixed + colgroup 是表头与表体严格对齐的最稳做法；
      // 列宽给足，日期(如 2026-09-17 14:30)与大小(如 1.2 GB)不会溢出到相邻列。
      m('colgroup', [
        m('col'),
        m('col.hide-sm', { style: 'width:130px' }),
        m('col.hide-sm', { style: 'width:190px' }),
      ]),
      m('thead', m('tr', [
        m('th', '名称'),
        m('th.num.hide-sm', '大小'),
        m('th.num.hide-sm', '修改时间'),
      ])),
      m('tbody', list.map(e => m('tr.row', { key: e.name, onclick: () => onOpen(e), title: e.name }, [
        m('td', m('.fname', [
          m('.kind', { class: kindClass(e.type, e.is_dir, e.mount) },
            m('svg', { viewBox: '0 0 24 24' }, m('path', { d: kindIcon(e.type, e.is_dir, e.mount) }))),
          m('span.n', e.name),
        ])),
        m('td.num.hide-sm', e.is_dir ? '—' : fmtSize(e.size)),
        m('td.num.hide-sm', fmtTime(e.modified)),
      ]))),
    ]));
  },
};

const PlayerBar = {
  view() {
    const p = state.player;
    if (!p || p.kind !== 'audio') return null;
    return m('.playerbar', [
      m('span.pt', { title: p.name }, `♪ ${p.name}`),
      m('audio', { src: p.url, controls: true, autoplay: true }),
      m('button.iconbtn.solid', { title: '关闭', onclick: closePlayer },
        m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.close }))),
    ]);
  },
};

const VideoOverlay = {
  view() {
    const p = state.player;
    if (!p || p.kind !== 'video') return null;
    return m('.overlay', { onclick: e => { if (e.target === e.currentTarget) closePlayer(); } },
      m('.playbox', [
        m('video', { src: p.url, controls: true, autoplay: true, playsinline: true }),
        m('.pbar', [
          m('span.pt', { title: p.name }, p.name),
          m('button.iconbtn.solid', { title: '关闭（Esc）', onclick: closePlayer },
            m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.close }))),
        ]),
      ]));
  },
};

const Lightbox = {
  view() {
    const lb = state.lb;
    if (!lb) return null;
    const cur = lb.imgs[lb.i];
    return m('.overlay.lightbox', { onclick: e => { if (e.target === e.currentTarget) closeLB(); } }, [
      // 舞台：滚轮缩放、按住拖动平移（放大后）
      m('.lb-stage', {
        onwheel: e => { e.preventDefault(); lbZoom(e.deltaY < 0 ? .25 : -.25); },
        onmousedown: e => { if (dragStart(e.clientX, e.clientY)) e.preventDefault(); },
        onmousemove: e => dragMove(e.clientX, e.clientY),
        onmouseup: () => dragEnd(),
        onmouseleave: () => dragEnd(),
        ontouchstart: e => {
          const t = e.touches[0];
          lb.touch = dragStart(t.clientX, t.clientY) ? { x: t.clientX, y: t.clientY } : { x: t.clientX, y: t.clientY, swipe: true };
        },
        ontouchmove: e => {
          const t = e.touches[0];
          if (lb.touch && lb.touch.swipe) return; // 未放大时留给左右滑动切换
          dragMove(t.clientX, t.clientY);
        },
        ontouchend: e => {
          const t = e.changedTouches[0];
          if (lb.touch && lb.touch.swipe && t) {
            const dx = t.clientX - lb.touch.x;
            if (Math.abs(dx) > 60) lbStep(dx < 0 ? 1 : -1);
          }
          lb.touch = null;
          dragEnd();
        },
      }, lb.url
        ? m('img', {
            src: lb.url, alt: cur.name, class: lb.zoom > 1 ? 'zoomable' : '',
            style: `transform: translate(${lb.x}px, ${lb.y}px) scale(${lb.zoom})`,
            ondblclick: () => lbZoom(lb.zoom > 1 ? -10 : 1), // 双击在「复位」与「放大」之间切换
          })
        : m('.kind.big', m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.image }))),
      ),
      lb.imgs.length > 1 ? m('button.lb-arrow.prev', { title: '上一张（←）', onclick: () => lbStep(-1) },
        m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.left }))) : null,
      lb.imgs.length > 1 ? m('button.lb-arrow.next', { title: '下一张（→）', onclick: () => lbStep(1) },
        m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.right }))) : null,
      m('.lb-bar', [
        m('span.nm', { title: cur.name }, `${lb.i + 1} / ${lb.imgs.length} · ${cur.name}`),
        m('button.iconbtn', { title: '缩小（-）', disabled: lb.zoom <= 1, onclick: () => lbZoom(-.25) },
          m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.zoomOut }))),
        m('span.pct', Math.round(lb.zoom * 100) + '%'),
        m('button.iconbtn', { title: '放大（+）', disabled: lb.zoom >= 5, onclick: () => lbZoom(.25) },
          m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.zoomIn }))),
        m('button.iconbtn', { title: '复位（0）', disabled: lb.zoom === 1, onclick: () => { lb.zoom = 1; lb.x = 0; lb.y = 0; } },
          m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.reset }))),
        // 在浮动层里预览够了；想直接用浏览器看原图（下载/全尺寸）就新开一个标签页
        m('button.iconbtn', {
          title: '在新标签页打开原图', disabled: !lb.url,
          onclick: () => { if (lb.url) window.open(lb.url, '_blank', 'noopener'); },
        }, m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.open }))),
        m('button.iconbtn.solid', { title: '关闭（Esc）', onclick: closeLB },
          m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.close }))),
      ]),
    ]);
  },
};

const LoginGate = {
  view() {
    return m('.overlay', m('.panel.narrow', [
      m('img', { src: 'logo.png', width: 64, height: 64, alt: 'Mocca' }),
      m('h1', 'Mocca 浏览'),
      m('p.muted', '浏览应用与管理后台共用同一份令牌。请先登录，再回到本页。'),
      m('div', { style: 'display:flex;gap:8px;flex-wrap:wrap' }, [
        m('a.browse-link', { href: 'admin/index.html', style: 'height:40px;padding:0 24px' }, '去登录'),
        m('button.ghost', { onclick: () => guard(init) }, '我已登录，重试'),
        // 服务端 allow_guest 默认是开的，此时不登录也能看（能看到什么由服务端决定）。
        // 关掉时点进来只会得到一句「未登录不可浏览」，不会白屏。
        m('button.ghost', { onclick: () => { state.guest = true; guard(() => openDir('/')); } }, '以游客浏览'),
      ]),
    ]));
  },
};

const App = {
  view() {
    if (!state.token && !state.guest) return m(LoginGate);
    return [
      state.drawer ? m('.scrim', { onclick: () => { state.drawer = false; } }) : null,
      m('.shell', [
        m(TopBar),
        m(Tree),
        m('.main', [
          m(Crumbs),
          m(Files),
          // 音频播放条是 fixed 的，直接压在列表上会盖住最后几行；留一块等高占位
          state.player && state.player.kind === 'audio' ? m('.playerspace') : null,
        ]),
      ]),
      m(PlayerBar),
      m(VideoOverlay),
      m(Lightbox),
      state.toast
        ? m('p.toast', {
            class: state.toastErr ? 'err' : '',
            style: 'position:fixed;left:50%;transform:translateX(-50%);bottom:24px;z-index:90;margin:0',
          }, state.toast)
        : null,
    ];
  },
};

/* ── 键盘快捷键 ───────────────────────────────────────── */
window.addEventListener('keydown', e => {
  if (e.key === 'Escape') {
    if (state.lb) return closeLB();
    if (state.player) return closePlayer();
    if (state.drawer) { state.drawer = false; return m.redraw(); }
    return;
  }
  if (!state.lb) return;
  if (e.key === 'ArrowLeft') lbStep(-1);
  else if (e.key === 'ArrowRight') lbStep(1);
  else if (e.key === '+' || e.key === '=') lbZoom(.25);
  else if (e.key === '-') lbZoom(-.25);
  else if (e.key === '0') { state.lb.zoom = 1; state.lb.x = 0; state.lb.y = 0; m.redraw(); }
});

// 屏幕旋转/窗口变宽后抽屉要收起来，否则会一直盖在内容上
window.addEventListener('resize', () => {
  if (state.drawer && window.innerWidth > 900) { state.drawer = false; m.redraw(); }
});

window.addEventListener('hashchange', () => guard(() => openDir(pathFromHash())));

/* ── 启动 ─────────────────────────────────────────────── */
async function init() {
  // 每次都重新读一次 localStorage：从后台登录后跳回本页（或「我已登录，重试」）时，
  // 手里的 state.token 还是旧的空值。
  state.token = localStorage.getItem('mocca_token') || '';
  if (state.token) {
    try { await api('/me'); } catch { /* 令牌无效：api 已清掉 token，界面切到登录引导 */ }
  }
  m.redraw();
  if (state.token || state.guest) await openDir(pathFromHash());
}

m.mount(document.getElementById('app'), App);
guard(init);
