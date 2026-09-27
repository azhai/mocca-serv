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
  // 笔：管理员编辑媒体信息的入口按钮
  edit: 'M3 17.25V21h3.75L17.81 9.94l-3.75-3.75L3 17.25zM20.71 7.04a1 1 0 0 0 0-1.41l-2.34-2.34a1 1 0 0 0-1.41 0l-1.83 1.83 3.75 3.75 1.83-1.83z',
  // 对话气泡：评论入口（与编辑的笔分开，别让人误点是编辑）
  comment: 'M20 2H4c-1.1 0-2 .9-2 2v18l4-4h14c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2zm0 14H6l-2 2V4h16v12z',
  // 弹幕开关：字幕样式（整条横带 + 两条短杠），一眼认出是"屏上飘的字"
  dm: 'M20 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V6c0-1.1-.9-2-2-2zM4 12h4v2H4v-2zm10 6H4v-2h10v2zm6 0h-4v-2h4v2zm0-4H10v-2h10v2z',
};

// 媒体类型，取值与 models/media.go 的 MediaKind 一一对应
const KIND = { dir: 0, unknown: 1, video: 2, audio: 3, text: 4, image: 5 };

// gridPerPage 网格每页条数：网页一页 8 个（4 列 × 2 行），中屏 6、窄屏 4。
function gridPerPage() {
  const w = window.innerWidth;
  return w >= 1000 ? 8 : (w >= 760 ? 6 : 4);
}

const state = {
  token: localStorage.getItem('mocca_token') || '',
  base: location.origin + '/api',
  path: '/',
  entries: [],
  loading: false,
  err: '',
  query: '',
  view: localStorage.getItem('mocca_view') === 'grid' ? 'grid' : 'list',
  // 网格分页（仅 .index.jsonl 的媒体条目）：perPage 随视口变，网页 2×8=16、窄屏更少。
  page: 1, perPage: gridPerPage(), total: 0, totalPage: 1,
  drawer: false,
  tree: {},   // 路径 → { open, kids: null|[{name,path}], loading }
  pwd: {},    // 路径 → 该目录（或其上层）密码的静态哈希
  guest: false, // 选择「以游客浏览」后为 true：没有令牌也进主界面（能否真看到内容由服务端定）
  player: null, // { kind:'audio'|'video', url, name }
  lb: null,   // 图片预览 { imgs:[{name,path}], i, zoom, x, y, url, loading }
  toast: '', toastErr: false,
  isAdmin: false,  // /me 返回角色为管理员时为 true，据此显示媒体条目右上角的编辑钮
  uid: 0,          // /me 返回的用户 id：用来判断"这条弹幕/评论是不是我发的"
  username: '',    // /me 返回的昵称（发出去时服务端会快照进记录，这里只用于本地提示）
  dm: null,        // 弹幕态（打开视频时才有，见 openDanmaku）
  cmt: null,       // 评论弹窗态（见 openComments）：{ path, name, list, text, replyTo, ... }
  edit: null,      // 编辑弹窗 { path, name, summary, director, cast, year, region, studio, loading }
  urlOf: new Map(), // 路径 → 可直接播放/预览的地址（服务端 raw_url 口径）
  // 弹幕开关（播放器标题行上的按钮）。记住选择：看片时想不想被弹幕打扰是稳定的偏好，
  // 不该每开一个片子重设一次。默认开（弹幕是本服务的主打功能之一）。
  dmOn: localStorage.getItem('mocca_dm') !== '0',
};

// coverRev 封面版本号：封面一经更改（上传/截图）就自增，拼进海报 URL 破缓存，
// 让网格卡片与编辑弹窗里的封面在保存/重渲染后都能自动显示新图。
let coverRev = 0;
// patchSec 目录级「补充截图」的时间点，秒数或「时:分:秒」（默认第 1 秒）。
let patchSec = '00:00:01';

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

// urlStateFrom 从 URL query 恢复目录与页码：刷新/书签/编辑保存后重载都保持位置。
function urlStateFrom() {
  const qp = new URLSearchParams(location.search);
  return { path: normPath(qp.get('path') || '/'), page: Math.max(1, parseInt(qp.get('page') || '1', 10) || 1) };
}

// syncURL 把当前目录与页码写回 URL query（replaceState，不触发重载）。
function syncURL() {
  const qp = new URLSearchParams();
  if (state.path && state.path !== '/') qp.set('path', state.path);
  if (state.view === 'grid' && state.page > 1) qp.set('page', String(state.page));
  const s = qp.toString();
  history.replaceState(null, '', location.pathname + (s ? '?' + s : ''));
}

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

// listDir 列目录；page/perPage ≥1 时走网格分页（服务端按 .index.jsonl 行区间只回媒体，不含子目录/文本）。
// 目录树（loadKids）不传分页，服务端回完整列表（含子目录/文本）。
// 遇到「需要密码」时问一次，带上后重试。
async function listDir(p, page = 0, perPage = 0) {
  const body = { path: p };
  if (page >= 1 && perPage >= 1) { body.page = page; body.per_page = perPage; }
  const pw = pwdFor(p);
  if (pw) body.password = pw;
  try {
    return await api('/fs/list', { method: 'POST', body });
  } catch (e) {
    if (!/需要密码/.test(e.message)) throw e;
    const plain = prompt(`目录 ${p} 需要密码：`);
    if (!plain) throw new Error('已取消');
    const hash = await staticHash(plain);
    const retry = { path: p, password: hash };
    if (page >= 1 && perPage >= 1) { retry.page = page; retry.per_page = perPage; }
    const d = await api('/fs/list', { method: 'POST', body: retry });
    state.pwd[p] = hash;
    return d;
  }
}

// loadPage 仅网格：取当前 state.page 那一页，刷新 entries 与 total。
async function loadPage() {
  state.loading = true; state.err = ''; m.redraw();
  try {
    const d = await listDir(state.path, state.page, state.perPage);
    state.entries = d.content || [];
    state.total = d.total || state.entries.length;
    state.totalPage = state.perPage ? Math.max(1, Math.ceil(state.total / state.perPage)) : 1;
    if (state.page > state.totalPage) { state.page = state.totalPage; }
  } catch (e) {
    state.entries = [];
    state.err = e.message;
  } finally {
    state.loading = false;
    syncURL(); // 翻页后把页码写回 URL，刷新/保存后保持
    m.redraw();
  }
}

// goPage 翻页：夹到 [1, totalPage] 内再取。
function goPage(delta) {
  const np = state.page + delta;
  if (np < 1 || np > state.totalPage) return;
  state.page = np;
  guard(loadPage);
}

// goTo 跳到指定页码（夹到 [1, totalPage]），与当前页相同则忽略。
function goTo(n) {
  const np = Math.min(Math.max(1, n | 0), state.totalPage);
  if (np === state.page) return;
  state.page = np;
  guard(loadPage);
}

// goVal 分页「跳转」输入框当前值。放模块级，避免在每次渲染里重建状态。
let goVal = '';
function goSubmit() {
  const n = parseInt(goVal, 10);
  if (!isNaN(n) && n >= 1 && n <= state.totalPage) goTo(n);
  goVal = '';
  m.redraw();
}

// gridPager 网格分页条：多于一页时显示。中间常显 ≥5 个页码（以当前页为中心向
// 两边各扩 2，首尾落差处用「…」补齐），两侧是上一页/下一页，末尾带「跳转」输入框。
function gridPager() {
  if (state.view !== 'grid' || state.totalPage <= 1) return null;
  const p = state.page, t = state.totalPage;
  const win = Array.from(new Set([1,
    p - 2, p - 1, p, p + 1, p + 2, t
  ].filter(x => x >= 1 && x <= t))).sort((a, b) => a - b);

  const items = [];
  let prev = 0;
  for (const n of win) {
    if (prev && n - prev > 1) items.push(m('span.pages', '…'));
    items.push(m('button.ghost', { class: n === p ? 'cur' : '', onclick: () => goTo(n) }, n));
    prev = n;
  }

  return m('.pager', [
    m('button.ghost', { class: p <= 1 ? 'dis' : '', disabled: p <= 1, onclick: () => goPage(-1) }, '‹ 上一页'),
    items,
    m('button.ghost', { class: p >= t ? 'dis' : '', disabled: p >= t, onclick: () => goPage(1) }, '下一页 ›'),
    m('.jump', [
      m('input.pj', {
        type: 'number', min: 1, max: t, placeholder: String(t),
        value: goVal,
        oninput: ev => { goVal = ev.target.value; },
        onkeydown: ev => { if (ev.key === 'Enter') goSubmit(); },
      }),
      m('button.ghost', { onclick: goSubmit }, '跳转'),
    ]),
  ]);
}

// driveURL 取可直接播放/预览的地址。
// 用 /fs/get 的 raw_url 而不是自己拼 /d：URL 形态归服务端管（含令牌、含受保护目录）。
// 受保护目录的密码不在 raw_url 里（服务端取流要 ?password=），这里补上。
// absURL 把服务端给的 /d 地址补成绝对地址，并附上受保护目录的密码
// （服务端取流要 ?password=，而 raw_url / hls_url 里都没有）。上游直链原样返回。
function absURL(u, pw) {
  if (!u) return '';
  if (/^https?:/i.test(u)) return u;
  if (pw) u += (u.includes('?') ? '&' : '?') + 'password=' + encodeURIComponent(pw);
  return location.origin + u;
}

async function driveURL(p) {
  if (state.urlOf.has(p)) return state.urlOf.get(p);
  const body = { path: p };
  const pw = pwdFor(p);
  if (pw) body.password = pw;
  const d = await api('/fs/get', { method: 'POST', body });
  const url = absURL(d.raw_url || ('/d' + encPath(p)), pw);
  state.urlOf.set(p, url);
  return url;
}

// driveMedia 播放取址：除 Range 直链（raw_url）外，还取旁路 HLS 清单（hls_url）。
// **有清单就优先走 HLS，没有就仍走直链**。刻意不缓存：切分完成后重新打开即可切到 HLS。
async function driveMedia(p) {
  const body = { path: p };
  const pw = pwdFor(p);
  if (pw) body.password = pw;
  const d = await api('/fs/get', { method: 'POST', body });
  return {
    url: absURL(d.raw_url || ('/d' + encPath(p)), pw),
    hls: d.hls_url ? absURL(d.hls_url, pw) : '',
  };
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

// posterURL 拼 .mocca 海报图地址。令牌走查询串（<img> 带不了自定义头），
// StreamAuth 会从查询串读 token；受保护目录的密码也一并带上。
function posterURL(p) {
  const q = new URLSearchParams();
  q.set('path', normPath(p));
  if (state.token) q.set('token', state.token);
  const pw = pwdFor(p);
  if (pw) q.set('password', pw);
  if (coverRev) q.set('rev', String(coverRev)); // 封面换过就带上版本号，破浏览器缓存
  return '/meta/poster?' + q.toString();
}

// stripNameExt 去掉文件扩展名（.mp4 之类），只留「可不含扩展名编辑」的部分。
function stripNameExt(n) {
  const i = n.lastIndexOf('.');
  return i > 0 ? n.slice(0, i) : n;
}

/* ── 管理员编辑 ────────────────────────────────────── */
// openEdit 打开编辑弹窗，先取现有元数据回填（避免白改覆盖）。
// 表单字段随媒体类型变化：图片只留简介；音频用主唱/伴唱/演奏 + 语言；视频用导演/主演等。
//
// prefill 可选：已在手边的刮削结果（检索候选 + apply 的回包）。
//
// 为什么要有 prefill：光靠"落盘后再读一次 /fs/info"来回填并不可靠 —— 那一环要先把文件
// 解析出 sha1 才读得到 .mocca，解析不到就整片空白。apply 的回包**本身就是刮削结果**，
// 所以它必须能独立把表单填起来，不依赖任何二次请求。
//
// 顺序因此很关键：**prefill 先同步落进表单**，`/fs/info` 只负责用设备侧已有内容去覆盖。
// （早先的写法是把 prefill 放在 `await api('/fs/info')` 之后的 try 里，那一环一抛错就跳进
// catch、prefill 整包丢掉 —— 兜底恰好在它要兜的场景里不生效，表现就是"刮了却什么都没填"。）
// 取值顺序：已落盘的 .mocca > prefill > 空（刮削是补齐不是重置）。
//
// **文件名刻意不碰**：它就是重命名入口，填成 TMDB 片名会让一次"保存"顺手改名，
// 而改名会动到存储上的真实文件（也可能破坏别人的硬链接/种子做种），所以只由用户手改。
async function openEdit(e, prefill) {
  const p = e.path || joinPath(state.path, e.name);
  const pre = prefill || {};
  state.edit = {
    path: p, kind: e.type || KIND.video,
    name: stripNameExt(e.name),
    summary: pre.overview || '',
    director: pre.director || '',
    // 主演两边都可能是数组（.mocca 与 apply 回包），统一用 / 连接
    cast: (pre.cast || []).join('/'),
    year: pre.year || '',
    region: pre.region || '', studio: pre.studio || '',
    language: pre.language || '',
    lead: '', backing: '', instrument: '',
    posterTS: 0, shotSec: '1', loading: true,
    // TMDB 刮削：scrapeKey 是可改的检索词（留空由后端按文件名猜）。
    // 片名填进检索框：下次再刮可以直接用这个关键词，不用重新猜。
    // scrape 存这次检索到的候选与选中项，供"同名电影换一部"，见 scrapeUI。
    scrapeKey: pre.title || '', scrape: null, scrapeBusy: false,
  };
  m.redraw();
  try {
    const body = { path: p };
    const pw = pwdFor(p);
    if (pw) body.password = pw;
    const d = await api('/fs/info', { method: 'POST', body });
    const meta = (d && d.meta) || {};
    if (state.edit && state.edit.path === p) {
      // 设备侧 .mocca 优先，逐字段 `||` 兜回表单里已有的 prefill：
      // 设备上有值就覆盖，没有就保留刚填进去的刮削结果。
      // 文件名不接候选片名：它是重命名入口，只保留设备上真实的文件名。
      state.edit.name = meta.title || stripNameExt(e.name);
      state.edit.scrapeKey = meta.title || pre.title || '';
      state.edit.summary = meta.summary || state.edit.summary;
      state.edit.director = meta.director || state.edit.director;
      state.edit.cast = (meta.cast && meta.cast.length ? meta.cast : (pre.cast || [])).join('/');
      state.edit.year = meta.year || state.edit.year;
      state.edit.region = meta.region || state.edit.region;
      state.edit.studio = meta.studio || state.edit.studio;
      state.edit.language = meta.language || state.edit.language;
      state.edit.lead = (meta.lead || []).join(', ');
      state.edit.backing = (meta.backing || []).join(', ');
      state.edit.instrument = (meta.instrument || []).join(', ');
    }
  } catch (err) {
    // 读设备侧信息失败只影响"覆盖"这一步，已经填进表单的刮削结果原样保留。
    say(err.message, true);
  } finally {
    if (state.edit) state.edit.loading = false;
  }
  m.redraw();
}

// saveEdit 提交编辑：改名 + 附加信息，成功后刷新当前目录。
// 后端按 kind 落库，这里只把相关字段带上（后端会忽略无关的）。
async function saveEdit() {
  const ed = state.edit;
  if (!ed || ed.loading) return;
  ed.loading = true; m.redraw();
  try {
    await api('/fs/edit', { method: 'POST', body: {
      path: ed.path,
      name: ed.name.trim(),
      summary: ed.summary,
      director: ed.director.trim(),
      cast: ed.cast.split(/[,/]/).map(s => s.trim()).filter(Boolean),
      year: parseInt(ed.year, 10) || 0,
      region: ed.region.trim(),
      studio: ed.studio.trim(),
      language: ed.language.trim(),
      lead: ed.lead.split(',').map(s => s.trim()).filter(Boolean),
      backing: ed.backing.split(',').map(s => s.trim()).filter(Boolean),
      instrument: ed.instrument.split(',').map(s => s.trim()).filter(Boolean),
    } });
    state.edit = null;
    // 只刷新当前页（保留页码），不要 openDir —— 它会重置回第一页
    await loadPage();
  } catch (err) { if (state.edit) state.edit.loading = false; say(err.message, true); m.redraw(); }
}

// uploadCover 上传替换封面：直接吞图片字节 POST /fs/cov，成功后刷新预览。
async function uploadCover(ed, file) {
  if (!file) return;
  try {
    const buf = await file.arrayBuffer();
    const q = new URLSearchParams({ path: normPath(ed.path) });
    const res = await fetch('/api/fs/cov?' + q, {
      method: 'POST', body: buf,
      headers: state.token ? { Authorization: state.token } : {},
    });
    const j = await res.json().catch(() => ({}));
    if (!j || j.code !== 200) throw new Error((j && j.message) || '上传失败');
    coverRev++;       // 封面变了，全局版本号自增，海报 URL 变新
    ed.posterTS = Date.now(); // 让海报预览强制重新加载新图
    say('封面已更新');
  } catch (err) { say(err.message, true); }
  m.redraw();
}

// removeCover 删除封面：POST /fs/uncov，成功后刷新预览（预览会取到 404，即"没有封面"）。
// 与上传/截图相对：那两条是替换，这条是清空 —— 清掉之后再刮削会重新取 TMDB 海报。
async function removeCover(ed) {
  if (ed.loading) return;
  const sure = window.confirm('确定删除当前封面吗？删除后可以重新上传、截图或再刮削一次。');
  if (!sure) return;
  ed.loading = true; m.redraw();
  try {
    const d = await api('/fs/uncov', { method: 'POST', body: { path: ed.path } });
    coverRev++;                // 封面没了也要换 URL，否则浏览器还显示缓存里的旧图
    ed.posterTS = Date.now();  // 同理：强制预览重新请求
    say(d && d.removed === false ? '本来就没有封面' : '封面已删除');
  } catch (err) { say(err.message, true); }
  ed.loading = false; m.redraw();
}

// doShot 用 FFmpeg 截取视频指定时间点的帧作封面，成功后刷新预览。
// 时间点接受秒数或「时:分:秒」（如 1:30 或 1:02:30），由后端 normalizeShotSec 统一交给 ffmpeg。
async function doShot(ed) {
  if (ed.loading) return;
  const sec = (ed.shotSec || '').trim() || '1';
  ed.loading = true; m.redraw();
  try {
    await api('/fs/shot', { method: 'POST', body: { path: ed.path, sec } });
    coverRev++;       // 封面变了，全局版本号自增，海报 URL 变新
    ed.posterTS = Date.now(); // 让封面预览强制重新加载新图
    say('封面已从截图更新');
  } catch (err) { say(err.message, true); }
  ed.loading = false; m.redraw();
}
/* ── TMDB 刮削（视频） ───────────────────────────────── */
// formSnapshot 记下"刮削前这份文件长什么样"，供候选里的「原始数据」还原。
//
// 为什么需要：刮削是**直接覆盖 .mocca** 的，而同名电影很容易连点错几条 —— 总得有路回到原点。
// 地区/出品方/语言虽然不在表单上显示，openEdit 也把它们存进了 state.edit，一并带上：
// /fs/edit 是按提交内容整体写入的，漏了哪项就等于把哪项清空。
function formSnapshot(ed) {
  return {
    summary: ed.summary || '', director: ed.director || '', cast: ed.cast || '',
    year: ed.year || '', region: ed.region || '', studio: ed.studio || '',
    language: ed.language || '', lead: ed.lead || '', backing: ed.backing || '',
    instrument: ed.instrument || '',
  };
}

// scrapeState 把这次的检索结果与"原始数据"合成一份界面状态。
// pickedId 是当前选中的那张卡：'__raw'（原始数据）或某条候选的 TMDB id。
// original 只在**首次检索**时确定，之后一律沿用 —— 否则"原始数据"会变成"上一条候选"。
function scrapeState(prev, pickedId, list, fileYear, original, res) {
  prev = prev || {};
  // keepCover：还原时要不要动封面。封面本来就是这次刮削加上去的（poster_kept 为假）就得删掉
  // 才算回到原样；这个判定只累积不重置（一旦为假就保持为假），否则再检索一次就把
  // "还原时该删封面"这条信息丢了，点「原始数据」会留下一张多出来的封面。
  let keepCover = prev.keepCover !== false;
  if (res) keepCover = keepCover && !!res.poster_kept;
  return {
    list, pickedId, fileYear,
    original: original || prev.original || null,
    keepCover,
  };
}

// applyCandidate 把选中的那条候选装进文件，并把结果拆进表单。
//
// 只在用户**点了候选卡片**时调用（scrapeSearch 只检索、不写盘）：所以一律整部替换
// （keep 传 `{}` = 全不保留），否则上一次刚填进去的资料会把新的一条全挡住。
// opts.list / opts.fileYear：挂回界面用，同名电影方便继续换一部（见 scrapeUI 的候选卡片）。
// opts.fallback：apply 失败时用来兜底填表单的详情（后端顺带带回的第一条）。
// opts.scrape：沿用上一次的界面状态（「原始数据」快照与封面判定，见 scrapeState）。
async function applyCandidate(ctx, picked, opts = {}) {
  const { list = [], fileYear = 0, fallback = null, scrape = null } = opts;
  let res = null, applyErr = '';
  try {
    res = await api('/fs/scrape/apply', { method: 'POST', body: {
      path: ctx.path, tmdb_id: picked.id, keep: {},
    } });
    // 只有真换了封面才动版本号：posterURL 里带着它，一改会让整页卡片的封面全部重拉一次。
    if (!res.poster_kept) coverRev++;
  } catch (err) { applyErr = err.message; }

  // 重建编辑态并把结果拆进对应字段（文件名始终不动，那是重命名入口）。
  await openEdit({ path: ctx.path, name: ctx.name, type: ctx.kind },
    scrapeToFields(res || fallback || picked, picked));
  // openEdit 会重建 state.edit，候选列表与选中态要在它之后再挂上去（否则被清掉）
  const ed = state.edit;
  const got = res || fallback || picked;
  if (ed && ed.path === ctx.path) {
    ed.scrape = scrapeState(scrape, picked.id, list, fileYear, 0, res);
    ed.scrapeBusy = false;
  }

  // 提示统一放最后说：提示条只有一个槽位，openEdit 里 /fs/info 的报错会先 say 一次，
  // 先说的那条（"已刮削：xxx"）会被它冲掉，用户就不知道封面到底换没换。
  const title = got.title || picked.title;
  if (applyErr) {
    say(`刮削写入失败：${applyErr}（已把检索到的信息填入表单，可手动保存）`, true);
  } else if (res && res.poster_kept) {
    // 只补空缺：已有内容与封面都原样保留，明确说一句，免得以为刮削没生效。
    say(`已刮削：${title}（已有内容与封面均保留）`);
  } else if (res) {
    say(res.poster ? `已刮削：${title}` : `已刮削：${title}（封面未更新：${res.poster_error}）`, !res.poster);
  }
  // 同名电影最常踩的坑：年份对不上就明确点出来，别让人以为刮对了
  if (fileYear && got.year && Math.abs(Number(got.year) - Number(fileYear)) > 1) {
    say(`注意：检索到的是 ${got.year} 年的《${title}》，与文件名里的 ${fileYear} 不符，可在下方换一部`, true);
  }
}

// scrapeSearch 刮削第一步：**只检索**，把候选列出来，默认选中「原始数据」。
//
// 刻意不自动选一条灌进文件：同名电影（翻拍、续集、译名撞车）里猜错的概率不低，而刮削是
// 直接覆盖 .mocca 的 —— 猜错就等于把原有资料覆盖掉。所以默认停在原样，由人点一下候选
// 才真正写入（点击走 scrapePickAgain，整部替换）。检索本身不写盘（见后端 ScrapeSearch）。
// 一条候选都没有、或都不是那部时，改上面的片名（可带年份）再刮一次。
async function scrapeSearch(ed) {
  if (ed.scrapeBusy) return;
  ed.scrapeBusy = true; m.redraw();
  const ctx = { path: ed.path, kind: ed.kind, name: ed.name };
  // original 是"刮削前长什么样"的快照（供候选里的「原始数据」还原），要在重建编辑态之前取走。
  const original = formSnapshot(ed);
  try {
    const d = await api('/fs/scrape', { method: 'POST', body: {
      path: ctx.path, keyword: (ed.scrapeKey || '').trim(),
    } });
    const list = d.candidates || [];
    if (!list.length) {
      say(`没找到「${d.keyword}」的候选，换个关键词再试`);
      ed.scrapeBusy = false; m.redraw();
      return;
    }
    // 只挂候选、不动表单：默认选中「原始数据」这张卡（pickedId 见 scrapeUI 的高亮判定）
    ed.scrape = scrapeState(ed.scrape, '__raw', list, d.year, original, null);
    say(`找到 ${list.length} 个候选，默认保留原始数据；点候选卡片即可替换`);
  } catch (err) { say(err.message, true); }
  ed.scrapeBusy = false; m.redraw();
}

// restoreOriginal 还原成刮削前的资料 —— 候选里那张「原始数据」卡片。
//
// 刮削是直接覆盖 .mocca 的，同名电影又容易连点错几条，这里给一条回到原点的路。
// 复用「保存」那条通道（/fs/edit 整体写入），所以地区/出品方/语言等不在表单上的项
// 也在快照里带着，不会被顺手清空；封面若本来就是这次刮削加上去的，一并删掉才算回到原样。
async function restoreOriginal(ed) {
  const sc = ed.scrape || {};
  const o = sc.original;
  if (!o) { say('没有可还原的原始资料', true); return; }
  const split = (s) => String(s || '').split(/[,/]/).map(x => x.trim()).filter(Boolean);
  const ctx = { path: ed.path, kind: ed.kind, name: ed.name };
  ed.loading = true; m.redraw();
  let err = '';
  try {
    await api('/fs/edit', { method: 'POST', body: {
      path: ctx.path,
      name: '',   // 空 = 不改名；改名只走表单里那个入口
      summary: o.summary, director: o.director, year: parseInt(o.year, 10) || 0,
      cast: split(o.cast), region: o.region, studio: o.studio, language: o.language,
      lead: split(o.lead), backing: split(o.backing), instrument: split(o.instrument),
    } });
    if (!sc.keepCover) {
      try {
        await api('/fs/uncov', { method: 'POST', body: { path: ctx.path } });
      } catch (e) { /* 删封面失败不影响文字还原，下面照样读回设备侧内容 */ }
      coverRev++;
    }
  } catch (e) { err = e.message; }

  // 重建编辑态，让表单显示还原后的内容（/fs/info 会读回刚写进去的 .mocca）
  await openEdit(ctx, {
    overview: o.summary, director: o.director, cast: split(o.cast),
    year: parseInt(o.year, 10) || 0, region: o.region, studio: o.studio, language: o.language,
  });
  const cur = state.edit;
  if (cur && cur.path === ctx.path) {
    cur.scrape = { ...sc, pickedId: '__raw' };
    cur.scrapeBusy = false;
  }
  say(err ? `还原失败：${err}` : '已还原为刮削前的资料', !!err);
}

// rawTip 「原始数据」卡片的 hover tip：把要还原成什么写清楚，省得点下去才发现是空的。
function rawTip(o) {
  return [
    '还原成刮削前的资料',
    `简介：${o.summary || '（空）'}`,
    `导演：${o.director || '（空）'}　年份：${o.year || '（空）'}`,
    `主演：${o.cast || '（空）'}`,
  ].join('\n');
}

// candTip 候选的 hover tip：卡片上只放封面和主演 —— 同名时片名一模一样，
// 真正能认出是哪一部的就只有这两样；其余信息（片名、年份、原名、评分、简介）全放这里。
function candTip(c) {
  const head = `${c.title}${c.year ? `（${c.year}）` : ''}`;
  const meta = [
    c.original_title && c.original_title !== c.title ? `原名 ${c.original_title}` : '',
    c.vote_average ? `TMDB ${Number(c.vote_average).toFixed(1)}` : '',
  ].filter(Boolean).join(' · ');
  // 空行分隔：原生 tooltip 不换行排版，靠空行把标题区和简介分开才有可读性
  return [head, meta, c.overview].filter(Boolean).join('\n');
}

// scrapePickAgain 换一部：用户明确点了这条候选（首次写入也走这里），所以**整部替换**
// （applyCandidate 里 keep 传 `{}` = 全不保留），而不是只补空缺 —— 否则上一次刚填进去的
// 资料会把新的一条全挡住。
async function scrapePickAgain(ed, id) {
  if (ed.scrapeBusy) return;
  const sc = ed.scrape || {};
  const picked = (sc.list || []).find(c => c.id === id);
  if (!picked) { say('该候选已失效，请重新刮削', true); return; }
  ed.scrapeBusy = true; m.redraw();
  const ctx = { path: ed.path, kind: ed.kind, name: ed.name };
  try {
    // scrape: sc —— 沿用原有的「原始数据」快照与封面判定，
    // 否则换一部之后"原始"就变成"上一条候选"了。
    await applyCandidate(ctx, picked, { list: sc.list, fileYear: sc.fileYear, scrape: sc });
  } catch (err) { say(err.message, true); }
  ed.scrapeBusy = false; m.redraw();
}

// scrapeToFields 把一处刮削结果**拆成表单字段**。
//
// 两个来源的字段名并不一样，混在一起最容易填错位，所以只在这一个函数里定映射：
//   - 候选（/fs/scrape 的 candidates）：title / year / overview（短说明）
//   - apply 回包（/fs/scrape/apply）：title / year / summary / director / cast / region / studio / language
// 回包优先（它是刚落盘、最权威的那份），候选兜底（回包拿不到时至少还有片名/年份/简介）。
function scrapeToFields(d, picked) {
  d = d || {}; picked = picked || {};
  const cast = (d.cast && d.cast.length ? d.cast : picked.cast) || [];
  return {
    title: d.title || picked.title,          // → 刮削检索框（不碰文件名）
    year: d.year || picked.year,             // → 年份
    overview: d.summary || picked.overview,  // → 简介
    director: d.director,                    // → 导演
    cast: cast,                              // → 主演（数组，openEdit 里用 / 连接）
    region: d.region, studio: d.studio, language: d.language,
  };
}

async function deleteFile() {
  const ed = state.edit;
  if (!ed || ed.loading) return;
  // 二次确认：明确告知不可恢复（目录会递归删除，这里只删文件路径，必为单个文件）
  const sure = window.confirm(`确定删除「${ed.name}」吗？此操作不可恢复。`);
  if (!sure) return;
  ed.loading = true; m.redraw();
  try {
    await api('/fs/remove', { method: 'POST', body: { path: ed.path } });
    state.edit = null;
    // 只刷新当前页（保留页码）；删除后 loadPage 会自动夹回有效页
    await loadPage();
    say('已删除');
  } catch (err) { if (state.edit) state.edit.loading = false; say(err.message, true); m.redraw(); }
}

// patchCovers 目录级「补充截图」：给当前目录下缺封面的视频在指定时间点批量抽帧作封面。
async function patchCovers() {
  const sec = patchSec.trim() || '1';
  const d = await api('/fs/patch', { method: 'POST', body: { path: state.path, sec } });
  coverRev++;                 // 封面可能批量更新，破缓存
  say(`补充封面 ${d.done} 个（共 ${d.covered}，失败 ${d.failed}）`);
  await loadPage();
}

/* ── 目录导航 ─────────────────────────────────────────── */
async function openDir(p, opts = {}) {
  const target = normPath(p);
  state.path = target;
  // 进新目录回到第一页；keepPage（刷新/编辑保存后的重载）保留当前页码
  if (!opts.keepPage) state.page = 1;
  state.loading = true; state.err = ''; m.redraw();
  try {
    // 网格走分页（只回媒体）；列表/树走完整列表
    const grid = state.view === 'grid';
    const d = await listDir(target, grid ? state.page : 0, grid ? state.perPage : 0);
    state.entries = d.content || [];
    state.total = d.total || state.entries.length;
    state.totalPage = state.perPage ? Math.max(1, Math.ceil(state.total / state.perPage)) : 1;
  } catch (e) {
    state.entries = [];
    state.err = e.message;
    if (!state.token) state.token = '';
  } finally {
    state.loading = false; m.redraw();
  }
  document.title = target === '/' ? 'Mocca 浏览' : baseName(target) + ' · Mocca 浏览';
  syncURL();
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
    // 目录自己是不是挂载点（根挂存储时根其实是个挂载点）也要记下来，
    // 树的根节点才能据此画成挂载点形态。
    n.mount = !!d.is_mount;
    // 树只放目录：文件在右侧列表里看。mount 一路带下去，树里也才能用挂载点图标。
    // 挂载点与真实目录重名时后端把展示名改成「～xxx」并另给 path（真实路径），
    // 这里直接用 e.path，点进去才不会跑到一个带「～」的错误目录。
    n.kids = (d.content || []).filter(e => e.is_dir)
      .map(e => ({ name: e.name, path: e.path || joinPath(p, e.name), mount: !!e.mount }));
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
  // 地址走 /fs/get：raw_url 是 Range 直链，hls_url 是旁路 m3u8 清单（可能没有）。
  // 取不到就退回 /d 直链（与列表同一路径口径，列表能看则直链大概率可播）。
  let url = location.origin + directURL(p), hls = '';
  try { ({ url, hls } = await driveMedia(p)); } catch (e) { /* 用上面的直链兜底 */ }
  state.player = { kind: entry.type === KIND.video ? 'video' : 'audio', url, hls, name: entry.name, path: p };
  m.redraw();
  // 弹幕只跟视频走：它是"对着画面某一刻"的发言，音频没有画面可对。
  if (entry.type === KIND.video) await openDanmaku(p);
}
function closePlayer() {
  closeDanmaku();
  state.player = null;
  m.redraw();
}

// ── 播放进度续播 ───────────────────────────────────────────
// 位置存 localStorage：浏览应用对游客（未登录）也开放，没有稳定的服务端账号可挂靠，
// 存本地最简单，也不占后端表。键按文件路径，换设备不同步（要跨设备得存服务端）。
const POS_PREFIX = 'mocca:pos:';
const POS_MIN = 10;    // 不足 10 秒不值得续播
const POS_TAIL = 10;   // 距结尾不足 10 秒视为已看完
const POS_EVERY = 5;   // 播放中每 5 秒落一次盘

function loadPos(path) {
  try { return parseFloat(localStorage.getItem(POS_PREFIX + path)) || 0; } catch (e) { return 0; }
}
function savePos(path, t) {
  if (!path || !isFinite(t) || t < 1) return;
  try { localStorage.setItem(POS_PREFIX + path, String(Math.floor(t))); } catch (e) { /* 隐私模式写入失败：忽略 */ }
}
function clearPos(path) {
  try { localStorage.removeItem(POS_PREFIX + path); } catch (e) { /* 同上 */ }
}

// attachMedia 给 <video>/<audio> 挂上「续播 + 记录进度」：
//   - 元数据就绪后跳到上次位置（太靠前或接近结尾则从头）；
//   - 播放中每 POS_EVERY 秒落一次，暂停/移除时再落一次；
//   - 播完清掉记录，下次从头开始。
function attachMedia(el, path) {
  const saved = loadPos(path);
  const jump = () => {
    if (saved >= POS_MIN && (!isFinite(el.duration) || saved < el.duration - POS_TAIL)) el.currentTime = saved;
  };
  if (el.readyState >= 1) jump(); else el.addEventListener('loadedmetadata', jump, { once: true });

  let last = 0;
  el.addEventListener('timeupdate', () => {
    if (Math.abs(el.currentTime - last) < POS_EVERY) return;
    last = el.currentTime;
    savePos(path, el.currentTime);
  });
  el.addEventListener('pause', () => savePos(path, el.currentTime));
  el.addEventListener('ended', () => clearPos(path));
}

// 关页/切后台兜底落一次：timeupdate 未必刚好走到下一个 5 秒
window.addEventListener('pagehide', () => {
  const el = document.querySelector('.playbox video, .playerbar audio');
  if (el && state.player) savePos(state.player.path, el.currentTime);
});

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
          onclick: () => { state.view = 'list'; localStorage.setItem('mocca_view', 'list'); guard(() => openDir(state.path)); },
        }, '列表'),
        m('button', {
          class: state.view === 'grid' ? 'on' : '',
          onclick: () => { state.view = 'grid'; localStorage.setItem('mocca_view', 'grid'); guard(() => openDir(state.path)); },
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
            m('.kind.tiny', { class: kindClass(0, true, root.mount) },
              m('svg', { viewBox: '0 0 24 24' }, m('path', { d: kindIcon(0, true, root.mount) }))),
            m('button.tname', { title: root.mount ? '根目录 /（挂载点）' : '根目录 /', onclick: () => go('/') }, '根目录 /'),
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
      // 补充截图：给本目录缺封面的视频批量生成封面（管理员）；时间点接受秒或「时:分:秒」
      state.isAdmin
        ? m('span.patchbar', [
            m('input.patch-sec', { title: '截图时间点（秒数或 时:分:秒）', placeholder: '00:00:01',
              value: patchSec, oninput: e => { patchSec = e.target.value; } }),
            m('button.btn.ghost', { onclick: () => guard(patchCovers),
              title: '给当前目录下缺封面的视频批量抽帧生成封面' }, '补充截图'),
          ])
        : null,
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
  // 重名的挂载点展示名带「～」，这里必须用后端给的 path，不然会跳到错误目录
  const p = entry.path || joinPath(state.path, entry.name);
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
      return m('.files', [
        m('.cards', list.map(e => {
        const p = e.path || joinPath(state.path, e.name);
        // 统一卡片：上部整幅封面（视频/音频用 .mocca 海报 4:3，图片用原图缩略），
        // 下部正文（标题去扩展名 + 导演·年份 + 主演 + 简介沉底）。图标垫底，封面加载失败时露出。
        const media = e.type === KIND.video || e.type === KIND.audio;
        const cover = media
          ? m('img.poster-img', { src: posterURL(p), alt: e.name, loading: 'lazy', onerror: ev => { ev.target.style.display = 'none'; } })
          : (e.type === KIND.image
              ? m('img.thumb-img', { src: directURL(p), alt: e.name, loading: 'lazy', onerror: ev => { ev.target.style.display = 'none'; } })
              : null);
        const body = [
          // 名称行：标题不显示扩展名（事件/播放仍用原文件名）
          m('.cn', [
            m('span.nc', stripNameExt(e.name)),
            state.isAdmin && !e.is_dir
              ? m('button.edit-btn.inline', { title: '编辑信息', onclick: ev => { ev.stopPropagation(); guard(() => openEdit(e)); } },
                  m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.edit })))
              : null,
            // 评论（排在编辑之后）：**所有媒体条目都有**，不是管理员专属
            !e.is_dir
              ? m('button.edit-btn.inline', { title: '评论', onclick: ev => { ev.stopPropagation(); openComments(e); } },
                  m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.comment })))
              : null,
          ]),
          // 主演一行：主演在左、年份贴最右（两者都无则不占位）；导演不在网格显示
          (e.cast && e.cast.length) || e.year
            ? m('.cast-line', [
                e.cast && e.cast.length
                  ? m('span.cst', { title: e.cast.join('/') }, `主演：${e.cast.join('/')}`)
                  : null,
                e.year ? m('span.yr', `年份：${e.year}`) : null,
              ])
            : null,
          // 简介（summary）沉到卡片最底（悬停时原生 title 显示全文）
          e.summary ? m('.cd', { title: e.summary }, e.summary) : null,
        ];
        return m('.card', { key: e.name, onclick: () => onOpen(e), title: e.name }, [
          m('.thumb', [
            m('.kind.big', { class: kindClass(e.type, e.is_dir, e.mount) },
              m('svg', { viewBox: '0 0 24 24' }, m('path', { d: kindIcon(e.type, e.is_dir, e.mount) }))),
            cover,
          ]),
          ...body,
        ]);
      })),
      // 网格分页条：多于一页才显示
      gridPager(),
      ]);
    }
    // 操作列只要有**任意一个媒体条目**就得留出来 —— 评论按钮对所有媒体条目都有
    // （编辑才限管理员），所以不能再用 state.isAdmin 决定这一列是否存在。
    const hasOps = list.some(e => !e.is_dir);
    return m('.files', m('table.filetable', [
      // colgroup 固定列宽：名称自适应剩余宽度，大小/修改时间定宽。
      // table-layout: fixed + colgroup 是表头与表体严格对齐的最稳做法；
      // 列宽给足，日期(如 2026-09-17 14:30)与大小(如 1.2 GB)不会溢出到相邻列。
      // 末尾「操作」列放按钮：编辑（管理员）+ 评论（所有人），评论排在编辑之后。
      m('colgroup', [
        m('col'),
        m('col.hide-sm', { style: 'width:130px' }),
        m('col.hide-sm', { style: 'width:190px' }),
        hasOps ? m('col.op-col', { style: 'width:96px' }) : null,
      ]),
      m('thead', m('tr', [
        m('th', '名称'),
        m('th.num.hide-sm', '大小'),
        m('th.num.hide-sm', '修改时间'),
        hasOps ? m('th.op', '操作') : null,
      ])),
      m('tbody', list.map(e => m('tr.row', { key: e.name, onclick: () => onOpen(e), title: e.name }, [
        m('td', m('.fname', [
          m('.kind', { class: kindClass(e.type, e.is_dir, e.mount) },
            m('svg', { viewBox: '0 0 24 24' }, m('path', { d: kindIcon(e.type, e.is_dir, e.mount) }))),
          m('span.n', e.name),
        ])),
        m('td.num.hide-sm', e.is_dir ? '—' : fmtSize(e.size)),
        m('td.num.hide-sm', fmtTime(e.modified)),
        // 操作列：目录没有可评论/可编辑的东西，留空
        hasOps
          ? m('td.op', e.is_dir ? null : [
              state.isAdmin
                ? m('button.edit-btn.small', { title: '编辑信息', onclick: ev => { ev.stopPropagation(); guard(() => openEdit(e)); } },
                    m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.edit })))
                : null,
              m('button.edit-btn.small', { title: '评论', onclick: ev => { ev.stopPropagation(); openComments(e); } },
                m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.comment }))),
            ])
          : null,
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
      m('audio', {
        src: p.url, controls: true, autoplay: true,
        oncreate: v => attachMedia(v.dom, p.path),
        onbeforeremove: v => savePos(p.path, v.dom.currentTime),
      }),
      m('button.iconbtn.solid', { title: '关闭', onclick: closePlayer },
        m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.close }))),
    ]);
  },
};

// seekVid 视频快进/快退：按住前先钳到 [0, duration]。
function seekVid(delta) {
  const v = document.querySelector('.playbox video');
  if (!v || isNaN(v.duration)) return;
  v.currentTime = Math.min(Math.max(0, v.currentTime + delta), v.duration);
}

// ── HLS 播放（旁路清单优先，没有则回退 Range 直链） ──────────
// hls.js 约 600KB，只在真的播 HLS 时才按需加载；它是本地文件（/js/hls.min.js），
// 不走 CDN —— 局域网 http 与断网场景都要能播。
let hlsLoading = null;
let videoHls = null; // 当前视频的 Hls 实例：换片/关闭必须 destroy，否则后台还在拉分片

function ensureHls() {
  if (window.Hls) return Promise.resolve(window.Hls);
  if (!hlsLoading) {
    hlsLoading = new Promise((res, rej) => {
      const s = document.createElement('script');
      s.src = '/js/hls.min.js';
      s.onload = () => res(window.Hls);
      s.onerror = () => { hlsLoading = null; rej(new Error('hls.js 加载失败')); };
      document.head.appendChild(s);
    });
  }
  return hlsLoading;
}

function stopVideo() {
  if (videoHls) { videoHls.destroy(); videoHls = null; }
}

// startVideo 挂上续播，有旁路清单时切到 HLS；HLS 用不了就退回 Range 直链。
function startVideo(el, p) {
  attachMedia(el, p.path);
  if (!p.hls) return;
  ensureHls().then(Hls => {
    if (!Hls || !Hls.isSupported()) { el.src = p.hls; return; } // Safari 原生支持 HLS
    // 清单里的分片是相对地址，相对解析会把查询串丢掉 → 分片请求会 401。
    // 所以从清单地址里抽出取流凭证，交给 xhrSetup 补到**每一条**请求（清单与分片）上。
    const [base, query] = p.hls.split('?');
    const q = query ? '?' + query : '';
    const h = new Hls({
      xhrSetup: (xhr, url) => { xhr.open('GET', (!q || url.includes('?')) ? url : url + q, true); },
    });
    h.on(Hls.Events.ERROR, (_evt, data) => {
      if (!data || !data.fatal) return;
      stopVideo();
      say('HLS 播放失败，已切回直链');
      el.src = p.url; // 回退到 Range 直链
    });
    h.loadSource(base);
    h.attachMedia(el);
    videoHls = h;
  }).catch(() => { el.src = p.url; }); // hls.js 都拉不到，退回直链
}

/* ── 弹幕与评论 ─────────────────────────────────────────
   数据都在**设备侧**：<存储根>/.mocca/xx/xx/<sha1>.danmaku.jsonl（见后端 mediaindex/danmaku.go），
   不进数据库 —— 弹幕跟着盘走，换机器/重装服务/把硬盘拔下来带走都还在。
   评论就是「时间点为 0 的弹幕」，所以两者共用一份存储与一套接口。
   前端只说 path，path → sha1 的换算在服务端做。 */
const DM_LIMIT = 50;    // 与后端 mediaindex.MaxDanmakuLength 对齐
const DM_TRACKS = 6;    // 同屏轨道数：再多就挤成一片，再少会互相压住
const DM_LIFE = 8;      // 一条弹幕飞过屏幕的秒数（CSS 的 --dm-life 必须与它一致）
const DM_CLEAR = 2;     // 秒：上一条飞出右侧入口、这条轨道算「让开」的时间

// nextTrack 挑一条上屏轨道：**从上往下**找第一条让开的，全忙时挑最久没用的。
//
// 别用 `seq % DM_TRACKS` 轮转：那只飘着一条弹幕时，下一条也会被排到中间
// 偏上的轨道去 —— 看起来就是「弹幕没出现在最上面」。而且 seq 的自增时机
// 两处写法不一致（发弹幕那条是先自增再取模），起点还差一格。
//
// nowMs 用**视频时间**（毫秒）：active 里的 until 就是按它算的，同一把尺子才比得了。
function nextTrack(d, nowMs) {
  const born = new Map();   // 轨道 → 该轨最近一条弹幕的上屏时刻
  for (const a of d.active) {
    const at = a.until - DM_LIFE * 1000;
    const prev = born.get(a.track);
    if (prev === undefined || at > prev) born.set(a.track, at);
  }
  for (let t = 0; t < DM_TRACKS; t++) {
    const at = born.get(t);
    if (at === undefined || nowMs - at >= DM_CLEAR * 1000) return t;
  }
  // 全满（弹幕很密）：用最久没用的那条，尽量不压住刚上去的
  let lru = 0;
  for (let t = 1; t < DM_TRACKS; t++) {
    if (born.get(t) < born.get(lru)) lru = t;
  }
  return lru;
}

const avatarURL = key => '/static/avatars/' + (key || '01') + '.png';

// openDanmaku 打开视频时准备弹幕：先拉一次全量，再开 SSE 收新的。
// 连接建立**之前**发的那几条不靠 SSE 补 —— 它们本来就该在这次全量里。
//
// 播放器里**不放评论**（用户要求）：看片时画面越干净越好，评论是"看完再聊"。
// 评论有独立入口（媒体条目上的评论按钮，见 openComments）。
async function openDanmaku(mediaPath) {
  // 直接换片（正在看 A 又点开 B）时会再次进来：**先把上一个收掉**，
  // 否则那条 SSE 会一直连着，还往已经没人看的状态里写。
  closeDanmaku();
  const d = {
    path: mediaPath, rows: [], active: [],
    cursor: 0, last: -1, seq: 0, paused: false,
    text: '', busy: false, es: null,
  };
  state.dm = d;
  startDmTick();
  try {
    const rows = await api('/danmaku?path=' + encodeURIComponent(mediaPath));
    if (state.dm !== d) return;   // 拉的过程中换片/关掉了，丢弃
    d.rows = rows || [];
    m.redraw();
  } catch (e) { say(e.message, true); }
  d.es = openEntryStream(mediaPath, e => {
    if (state.dm !== d || e.type !== 1) return;   // 播放器只吃弹幕
    if (d.rows.some(x => x.id === e.id)) return;  // 自己刚发的：本地已经排进去了
    d.rows.push(e);
    d.rows.sort((a, b) => a.offset - b.offset);
    m.redraw();
  });
}

// closeDanmaku 关播放器时收摊：SSE 必须显式 close（EventSource 不会因为页面元素没了就自己断），
// 否则换个片子看，旧的流还在后台连着、还在往已经废弃的状态里写。
function closeDanmaku() {
  stopDmTick();
  if (state.dm && state.dm.es) { try { state.dm.es.close(); } catch (_) { /* 已断开 */ } }
  state.dm = null;
}

// openEntryStream 订阅某媒体的新弹幕/新评论（SSE），返回 EventSource，由调用方负责 close。
//
// 同一个流里两类都推（弹幕与评论共用一个发布通道），调用方按需要过滤。
// EventSource 自带断线重连，所以**出错时不要 close**，一 close 就再也不重连了。
function openEntryStream(mediaPath, onEntry) {
  if (!window.EventSource) return null;   // 老浏览器：退化成"打开时拉一次"
  const q = new URLSearchParams({ path: mediaPath });
  if (state.token) q.set('token', state.token);   // EventSource 带不了自定义请求头
  const es = new EventSource(state.base + '/danmaku/stream?' + q.toString());
  es.addEventListener('danmaku', ev => {
    let e;
    try { e = JSON.parse(ev.data); } catch (_) { return; }
    onEntry(e);
  });
  return es;
}

// mergeComment 把一条评论并进列表。列表本身是**最新在最上面**（后端已按撰写时间倒序），
// 所以顶层评论插到最前面；回复追加到父评论的回复末尾（回复保持正序，读起来才像对话）。
// 父评论不在了（别人刚删）就丢掉 —— 与后端"孤儿回复不展示"保持一致。
function mergeComment(list, e) {
  if (list.some(t => t.id === e.id)) return false;
  if (!e.parent_id) {
    list.unshift({ ...e, replies: [] });
    return true;
  }
  const parent = list.find(t => t.id === e.parent_id);
  if (!parent) return false;
  parent.replies = (parent.replies || []).concat(e);
  return true;
}

// 弹幕心跳：每 200ms 按当前播放位置把弹幕放上屏、把飞出去的清掉。
//
// 为什么不用 <video> 的 timeupdate：它不均匀（解码卡一下会攒一堆），弹幕会一顿一顿地冒出来。
// 用定时器读 currentTime，出来才是匀速的。
let dmTimer = null;
function startDmTick() {
  stopDmTick();
  dmTimer = setInterval(() => {
    const d = state.dm;
    const v = document.querySelector('.playbox video');
    if (!d || !v) return;
    const now = v.currentTime * 1000;
    let changed = false;

    if (d.last >= 0 && now < d.last - 1200) { d.cursor = 0; changed = true; }  // 往回拖：重排游标
    d.last = now;
    // 往前跳到没看过的位置：跳过那些"已经过去"的弹幕，不补放（补放等于一次刷屏）
    while (d.cursor < d.rows.length && d.rows[d.cursor].offset < now - 1000) d.cursor++;
    if (state.dmOn) {
      // 已经到点的弹幕放上屏
      while (d.cursor < d.rows.length && d.rows[d.cursor].offset <= now + 250) {
        const row = d.rows[d.cursor++];
        d.active.push({ ...row, key: 'dm' + (++d.seq), track: nextTrack(d, now), until: now + DM_LIFE * 1000 });
        changed = true;
      }
    } else if (d.active.length) {
      // 关着的时候清空在飘的：否则重新打开会看到一堆"穿越"过来的旧弹幕。
      // 游标上面照常推进，所以重新打开是从当前时刻接着放，不是补放。
      d.active = []; changed = true;
    }
    // 暂停时不清屏：弹幕停在原处更符合直觉（CSS 也会跟着暂停动画）
    if (d.paused !== v.paused) { d.paused = v.paused; changed = true; }
    if (!v.paused) {
      const keep = d.active.filter(a => a.until > now);
      if (keep.length !== d.active.length) { d.active = keep; changed = true; }
    }
    if (changed) m.redraw();
  }, 200);
}
function stopDmTick() {
  if (dmTimer) { clearInterval(dmTimer); dmTimer = null; }
}

// toggleDm 弹幕开关（播放器标题行上的按钮）。
//
// 只影响「上屏」：SSE 订阅不断、已收下的弹幕不丢，所以关掉再打开能立刻接着放。
// 关掉的那一刻把在飘的清掉，打开时由 tick 从当前播放位置接着走 —— 不补放旧弹幕。
function toggleDm() {
  state.dmOn = !state.dmOn;
  localStorage.setItem('mocca_dm', state.dmOn ? '1' : '0');
  if (!state.dmOn && state.dm) state.dm.active = [];
  m.redraw();
}

// sendDanmaku 发弹幕：时间点取**当前播放位置** —— "边看边发"就该锚在这一刻。
async function sendDanmaku() {
  const d = state.dm;
  if (!d || d.busy) return;
  const text = (d.text || '').trim();
  if (!text) return;
  const v = document.querySelector('.playbox video');
  const now = v ? v.currentTime * 1000 : 0;
  d.busy = true;
  m.redraw();
  try {
    const e = await api('/danmaku', {
      method: 'POST',
      body: { path: d.path, offset: Math.max(0, Math.round(now)), content: text },
    });
    d.text = '';
    if (e && !d.rows.some(x => x.id === e.id)) {
      d.rows.push(e);
      d.rows.sort((a, b) => a.offset - b.offset);
      // 自己发的立刻上屏（SSE 也会推回来一条，靠 id 去重）
      d.active.push({ ...e, key: 'dm' + (++d.seq), track: nextTrack(d, now), until: now + DM_LIFE * 1000 });
    }
    say('弹幕已发送');
  } catch (e) { say(e.message, true); }
  d.busy = false;
  m.redraw();
}

/* ── 评论（独立弹窗）────────────────────────────────────
   入口是媒体条目上的「评论」按钮（在「编辑」之后）。填写框放在**最上面**，
   列表按撰写时间**倒序**（最新在最上面）—— 与看帖习惯一致，进来先看到最新的。 */
async function openComments(entry) {
  const p = entry.path || joinPath(state.path, entry.name);
  const c = {
    path: p, name: stripNameExt(entry.name),
    list: [], text: '', replyTo: '', busy: false, loading: true, es: null,
  };
  // 换了条目就先收掉上一条的流（同 openDanmaku 的道理）
  if (state.cmt && state.cmt.es) { try { state.cmt.es.close(); } catch (_) { /* 已断开 */ } }
  state.cmt = c;
  m.redraw();
  try {
    const list = await api('/comments?path=' + encodeURIComponent(p));
    if (state.cmt !== c) return;   // 关掉或换条目了，丢弃
    c.list = list || [];
  } catch (e) { say(e.message, true); }
  c.loading = false;
  m.redraw();
  c.es = openEntryStream(p, e => {
    if (state.cmt !== c || e.type !== 0) return;   // 评论弹窗只吃评论
    if (mergeComment(c.list, e)) m.redraw();
  });
}

function closeComments() {
  if (state.cmt && state.cmt.es) { try { state.cmt.es.close(); } catch (_) { /* 已断开 */ } }
  state.cmt = null;
  m.redraw();
}

// sendComment 发评论；c.replyTo 非空时是回复某条评论（回复只支持一层）。
async function sendComment() {
  const c = state.cmt;
  if (!c || c.busy) return;
  const text = (c.text || '').trim();
  if (!text) return;
  c.busy = true;
  m.redraw();
  try {
    const e = await api('/comments', {
      method: 'POST',
      body: { path: c.path, content: text, parent_id: c.replyTo || '' },
    });
    c.text = '';
    c.replyTo = '';
    mergeComment(c.list, e);   // 新评论会插到列表最前面
    say('评论已发表');
  } catch (e) { say(e.message, true); }
  c.busy = false;
  m.redraw();
}

// delComment 删除自己的评论（管理员能删任何人的，由服务端判定）。
async function delComment(id) {
  const c = state.cmt;
  if (!c || c.busy) return;
  if (!confirm('删除这条评论？')) return;
  c.busy = true;
  m.redraw();
  try {
    await api('/comments', { method: 'DELETE', body: { path: c.path, id } });
    c.list = c.list.filter(t => t.id !== id);
    c.list.forEach(t => { t.replies = (t.replies || []).filter(r => r.id !== id); });
    if (c.replyTo === id) c.replyTo = '';
    say('已删除');
  } catch (e) { say(e.message, true); }
  c.busy = false;
  m.redraw();
}

// canDelete 我能不能删这条：自己的（uid 相同）或管理员。
function canDelete(item) {
  return state.isAdmin || (item.user_id && item.user_id === state.uid);
}

// fmtTime 弹幕时刻（毫秒）→ 便于阅读的时间轴位置。
function fmtOffset(ms) {
  const s = Math.max(0, Math.floor(ms / 1000));
  const m = Math.floor(s / 60);
  return m + ':' + String(s % 60).padStart(2, '0');
}

// fmtWhen 记录时间 → 相对"现在"的粗略说法。
function fmtWhen(iso) {
  const t = Date.parse(iso);
  if (!isFinite(t)) return '';
  const diff = (Date.now() - t) / 1000;
  if (diff < 60) return '刚刚';
  if (diff < 3600) return Math.floor(diff / 60) + ' 分钟前';
  if (diff < 86400) return Math.floor(diff / 3600) + ' 小时前';
  return new Date(t).toLocaleDateString();
}

// CommentItem 一条评论（顶层带它的回复）。作者昵称/头像直接用记录里的**快照**，
// 不再查用户表 —— 用户后来改名换头像，历史评论仍显示当时的样子。
function CommentItem(c, isReply) {
  const cm = state.cmt;
  return m('.cmt' + (isReply ? '.reply' : ''), { key: c.id }, [
    m('img.cmt-av', { src: avatarURL(c.user_avatar), alt: c.user_name || '?', loading: 'lazy' }),
    m('.cmt-main', [
      m('.cmt-head', [
        m('span.cmt-name', c.user_name || '匿名'),
        m('span.cmt-meta', fmtWhen(c.created_at)),
        m('.cmt-ops', [
          !isReply ? m('button.link', {
            onclick: () => { cm.replyTo = c.id; m.redraw(); },
          }, '回复') : null,
          canDelete(c) ? m('button.link', { onclick: () => guard(() => delComment(c.id)) }, '删除') : null,
        ]),
      ]),
      m('.cmt-text', c.content),
      !isReply && (c.replies || []).length
        ? m('.cmt-replies', c.replies.map(r => CommentItem(r, true)))
        : null,
    ]),
  ]);
}

// CommentModal 评论弹窗：**填写框在最上面**，下面是倒序的评论列表。
// 用户要求"评论另外弄个按钮"（在编辑之后）、"按撰写时间倒序"、"最上面提供填写按钮"。
//
// 写成 `{view}` 对象而不是 `function`：`m(CommentModal)` 只认带 view 的组件，
// 传函数会被当成 closure component（那要求返回 {view}），返回 vnode 就会在渲染时
// 报 "Cannot read properties of undefined (reading 'apply')"。全项目的弹窗都是这个写法。
const CommentModal = {
  view() {
  const c = state.cmt;
  if (!c) return null;
  const target = c.replyTo ? c.list.find(t => t.id === c.replyTo) : null;
  const total = c.list.reduce((n, t) => n + 1 + (t.replies || []).length, 0);
  return m('.overlay.scrim-edit', {
    key: 'cmtmodal',
    onclick: e => { if (e.target === e.currentTarget) closeComments(); },
  }, m('.panel.cmt-panel', [
    m('button.iconbtn.close-edit', { title: '关闭（Esc）', onclick: closeComments },
      m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.close }))),
    m('h2', [
      '评论',
      m('span.muted.tiny', { title: c.path }, `（${total}）${c.name}`),
    ]),
    // 填写框放最上面：进来就能写，不用先滚到列表底部
    m('.cmt-form', [
      target ? m('.cmt-chip', [
        m('span', '回复 @' + (target.user_name || '匿名')),
        m('button.link', { onclick: () => { c.replyTo = ''; m.redraw(); } }, '取消'),
      ]) : null,
      m('textarea', {
        name: 'comment-content', id: 'fld-comment-content', rows: 3, maxlength: 500,
        placeholder: target ? '回复…（Enter 发表，Shift+Enter 换行）' : '写评论…（Enter 发表，Shift+Enter 换行）',
        value: c.text,
        oninput: e => { c.text = e.target.value; },
        onkeydown: e => {
          if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); guard(sendComment); }
        },
      }),
      m('button.btn', { disabled: c.busy || !(c.text || '').trim(), onclick: () => guard(sendComment) },
        c.busy ? '发送中…' : '发表评论'),
    ]),
    c.loading
      ? m('p.muted.tiny', '加载中…')
      : (c.list.length
          ? m('.cmt-list', c.list.map(t => CommentItem(t, false)))
          : m('p.muted.tiny', '还没有评论，来说第一句')),
  ]));
  },
};

const VideoOverlay = {
  view() {
    const p = state.player;
    if (!p || p.kind !== 'video') return null;
    const d = state.dm;
    return m('.overlay', { onclick: e => { if (e.target === e.currentTarget) closePlayer(); } },
      m('.playbox', { key: 'playbox' }, [
        // 视频与弹幕层同属一个定位容器：弹幕要**盖在画面上**，而且不能挡住播放控件
        m('.vwrap', { key: 'vwrap' }, [
          m('video', {
            // 有旁路清单时不设 src，交给 hls.js 接管；否则直接用 Range 直链。
            // key 用路径：换片时强制重建元素，否则 mithril 复用同一个 <video>，
            // oncreate 不再触发，旧的 hls 实例会赖着不放。
            key: p.path,
            src: p.hls ? undefined : p.url,
            controls: true, autoplay: true, playsinline: true,
            // 续播：元数据就绪即跳到上次位置；关闭/卸载时再落一次，避免只靠 5 秒节流丢进度
            oncreate: v => startVideo(v.dom, p),
            onbeforeremove: v => { savePos(p.path, v.dom.currentTime); stopVideo(); },
          }),
          d && state.dmOn ? m('.dm-layer', {
            key: 'dmlayer',
            class: d.paused ? 'paused' : '',
          }, d.active.map(a => m('span.dm', {
            key: a.key,
            style: { top: (a.track * (100 / DM_TRACKS)) + '%' },
          }, a.content))) : null,
        ].filter(Boolean)),
        // 与上面的 <video> 同属一个片段：mithril 要求片段内 vnode 要么全有 key、
        // 要么全没有，只给 video 加 key 会直接报「In fragments, vnodes must either
        // all have keys or none have keys」而整块渲染不出来。条件分支留下的 null
        // 也算一个「不带 key 的位置」，所以数组都 `.filter(Boolean)` 把洞去掉，
        // 保证有内容时每个位置都带 key。
        m('.pbar', { key: 'pbar' }, [
          m('button.btn.ghost', { title: '后退 10 秒', onclick: () => seekVid(-10) }, '-10s'),
          m('button.btn.ghost', { title: '前进 10 秒', onclick: () => seekVid(10) }, '+10s'),
          // 弹幕开关：带图标，关掉时整颗按钮变灰（.off），一眼能看出当前状态
          m('button.btn.ghost.dm-toggle', {
            class: state.dmOn ? '' : 'off',
            title: state.dmOn ? '关闭弹幕' : '打开弹幕',
            onclick: toggleDm,
          }, [
            m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.dm })),
            m('span', state.dmOn ? '弹幕' : '弹幕关'),
          ]),
          m('span.pt', { title: p.name }, p.name),
          m('button.iconbtn.solid', { title: '关闭（Esc）', onclick: closePlayer },
            m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.close }))),
        ]),
        // 发弹幕：时间点由"当前播放位置"自动带上，所以这里只要一个输入框
        d ? m('.dm-form', { key: 'dmform' }, [
          m('input', {
            type: 'text', maxlength: DM_LIMIT, autocomplete: 'off',
            placeholder: `发一条弹幕…（Enter 发送，最多 ${DM_LIMIT} 字）`,
            value: d.text,
            oninput: e => { d.text = e.target.value; },
            onkeydown: e => { if (e.key === 'Enter') { e.preventDefault(); guard(sendDanmaku); } },
          }),
          m('button.btn', { disabled: d.busy, onclick: () => guard(sendDanmaku) }, '发送'),
        ]) : null,
      ].filter(Boolean)));
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

const EditModal = {
  view() {
    const ed = state.edit;
    if (!ed) return null;
    // 每个控件都带上 name/id，名字就是它在 state.edit 里的字段名（year、director、cast…）。
    // 值走 JS 绑定、不一定有 form 提交，但有了 name/id 才能按选择器定位/自动填表，
    // 光靠"第几个输入框"或 type="number" 这种特征去认太脆。
    // 统一 autocomplete="off"：这些是媒体元数据，浏览器自动填充只会塞进
    // 用户名/电话之类完全无关的值，把字段"自动填错"，比不填更烦人。
    const field = (label, key, extra = {}) => m('label.fld', [
      m('span', label),
      m('input', { name: key, id: 'fld-' + key, autocomplete: 'off', value: ed[key],
        oninput: e => { ed[key] = e.target.value; }, ...extra }),
    ]);
    const textarea = m('label.fld.tarea', [
      m('span', '简介'),
      m('textarea.slim', { name: 'summary', id: 'fld-summary', autocomplete: 'off',
        value: ed.summary, rows: 4,
        oninput: e => { ed.summary = e.target.value; } }),
    ]);

    // 封面：音/视频可上传替换；视频另外可用 FFmpeg 按秒数截图做封面。
    // 上下摆放：封面图在上、上传按钮、FFmpeg 截图（输入+按钮）在下。
    const coverUI = (ed.kind === KIND.video || ed.kind === KIND.audio)
      ? m('.cover', { id: 'fld-cover' }, [
          m('img.cov', { name: 'cover', id: 'cover-img',
            src: posterURL(ed.path) + (ed.posterTS ? '&t=' + ed.posterTS : ''), alt: '当前封面' }),
          m('.cover-tools', [
            // 删除封面放在上传封面左边：先"去掉不要的"，再"换成想要的"
            m('button.btn.ghost.danger', {
              disabled: ed.loading, title: '删除当前封面（之后可重新上传、截图或再刮削一次）',
              onclick: () => guard(() => removeCover(ed)),
            }, '删除封面'),
            m('label.btn.ghost', { title: '选择图片上传替换封面' },
              '上传封面',
              m('input.file-in', { name: 'cover-file', id: 'fld-cover-file', type: 'file', accept: 'image/*',
                onchange: e => guard(() => uploadCover(ed, e.target.files[0])) })),
            ed.kind === KIND.video
              ? m('.shot', [
                    m('input.shot-sec', { name: 'shot-sec', id: 'fld-shot-sec', autocomplete: 'off',
                      type: 'text', placeholder: '0:01 或 1:30',
                      value: ed.shotSec || '1', oninput: e => { ed.shotSec = e.target.value; } }),
                    m('button.btn.ghost', { disabled: ed.loading,
                      onclick: () => guard(() => doShot(ed)), title: '用 FFmpeg 截取指定时间点的帧作封面' },
                      'FFmpeg 截图'),
                  ])
              : null,
          ]),
        ])
      : null;

    // TMDB 刮削：一个可改的检索词 + 一个按钮，点下去直接落盘并回填表单（含封面）。
    // 只给视频——TMDB 是影视库，音频/图片刮不出东西（后端也会拒）。
    //
    // 同名电影的选择：每部候选占一列，列里**两张图各占一行** ——
    // 上排是横版剧照（16:9，封面取的就是它），下排是竖版海报（2:3，带片名文字）；
    // 下面一行是主演。同名时片名一模一样，这三样才是认出"是哪一部"的依据；
    // 其余信息（片名/年份/原名/评分/简介）全部走 hover tip（见 candTip）。
    // 点一条即**整部替换**（见 scrapePickAgain），当前生效的高亮 —— 两张图同属一个按钮，
    // 点哪张都一样，不会出现"点了图却选到别的候选"。
    //
    // 第一张固定是**「原始数据」**：刮削是直接覆盖 .mocca 的，点错几条之后要靠它回到原点
    // （见 restoreOriginal）。它跟候选并排放在同一个可滚动区里，所以位置固定、一眼能看见。
    // thumb 一张候选缩略图；cls 是 land（横版）/ port（竖版），决定这行的高度与裁法（见 CSS）。
    const thumb = (cls, url, emptyText) => m('.cand-thumb.' + cls, url
      ? m('img', { src: url, alt: '', loading: 'lazy',
          onerror: ev => { ev.target.style.display = 'none'; } })
      : m('span.ph', emptyText));
    const cards = [];
    if (ed.scrape) {
      const o = ed.scrape.original;
      if (o) {
        cards.push(m('button.cand.raw', {
          key: '__raw', disabled: ed.scrapeBusy,
          class: ed.scrape.pickedId === '__raw' ? 'on' : '',
          title: rawTip(o),
          onclick: () => guard(() => restoreOriginal(ed)),
          // 只有一条竖排文字：这一列越窄，候选能分到的宽度越多（CSS 里是 36px）
        }, m('.cand-c', '原始数据')));
      }
      ed.scrape.list.slice(0, 3).forEach(c => cards.push(m('button.cand', {
        key: c.id, disabled: ed.scrapeBusy,
        class: c.id === ed.scrape.pickedId ? 'on' : '',
        title: candTip(c),
        onclick: () => guard(() => scrapePickAgain(ed, c.id)),
      }, [
        thumb('land', c.backdrop, '无剧照'),
        thumb('port', c.poster, '无封面'),
        m('.cand-c', (c.cast || []).slice(0, 3).join(' / ') || '（暂无主演）'),
      ])));
    }
    // 只有一条候选、又没有可还原的原始资料时不摆 —— 表单已经就是它了
    const cands = cards.length > 1 ? m('.cands', cards) : null;
    const scrapeUI = ed.kind === KIND.video
      ? m('.scrape', [
          m('.scrape-head', [
            m('input.scrape-key', {
              name: 'scrape-key', id: 'fld-scrape-key', autocomplete: 'off',
              placeholder: '片名（可带年份，如 无间道 2002）', value: ed.scrapeKey || '',
              oninput: e => { ed.scrapeKey = e.target.value; },
            }),
            m('button.btn.ghost', {
              disabled: ed.scrapeBusy,
              title: '用 TMDB 检索候选（只检索，不写盘；默认仍用原始数据）；' +
                '点下方候选卡片才写入本文件，仍不对就改上面的片名（带年份更准）再刮',
              onclick: () => guard(() => scrapeSearch(ed)),
            }, ed.scrapeBusy ? '处理中…' : 'TMDB 刮削'),
          ]),
          cands,
        ])
      : null;

    // 字段随类型收敛：图片无附加信息；音频只有语言/作者；视频只要 导演/年份/主演/简介。
    //
    // 视频刻意**不给**地区、出品方、语言：刮削虽会带回这些值，但界面上不放 ——
    // 放在表单里只会让几十条无关信息挤满一屏，而这几项日常几乎没人改。
    let fields;
    if (ed.kind === KIND.image) {
      fields = [];
    } else if (ed.kind === KIND.audio) {
      fields = [
        field('语言', 'language'),
        field('作者', 'cast'),
      ];
    } else { // 视频
      fields = [
        m('.fld-row', [
          field('导演', 'director'),
          // 年份刻意不用 type="number"：它带上下箭头、还会被浏览器当数字框做校验，
          // 而这里只是个年份文本。数字键盘靠 inputmode 提示即可。
          field('年份', 'year', { inputmode: 'numeric', placeholder: '如 2014' }),
        ]),
        field('主演', 'cast', { placeholder: '用逗号或斜线分隔，空格自动去除' }),
        textarea,
      ];
    }

    return m('.overlay.scrim-edit', [
      m('.panel.edit-panel', [
        // 右上角主动关闭；点遮罩不退出，避免误触丢编辑
        m('button.iconbtn.close-edit', { title: '关闭', onclick: () => { state.edit = null; } },
          m('svg', { viewBox: '0 0 24 24' }, m('path', { d: I.close }))),
        coverUI,
        // 刮削紧跟封面、在文件名**之上**：它是"填表"的起点（检索 → 选中 → 自动填好导演/年份/主演/简介），
        // 摆在表单最上方才顺手；原先放在文件名后面，长表单里一滚就看不见了。
        // 检索到的候选也在这里展开：一行横版剧照、一行竖版海报（见上面的 thumb）。
        scrapeUI,
        field('文件名', 'name', { placeholder: '不含扩展名' }),
        ...fields,
        // 最下面一行：最左边删除、最右边保存（删除已含二次确认）
        m('.edit-foot', [
          m('button.ghost.danger', { disabled: ed.loading, onclick: () => guard(deleteFile) }, '删除'),
          m('button.solid', { disabled: ed.loading, onclick: () => guard(saveEdit) },
            ed.loading ? '保存中…' : '保存'),
        ]),
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
      // 浮动层/弹窗叠在最外层：编辑弹窗在最上
      state.edit ? m(EditModal) : null,
      state.cmt ? m(CommentModal) : null,
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
    // 编辑弹窗不响应 Esc：内容改动多，误触 Esc 会丢已填信息，关闭走右上角按钮
    if (state.edit) return;
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

// 屏幕旋转/窗口变宽后抽屉要收起来，否则会一直盖在内容上；网格视口变化重算每页条数
window.addEventListener('resize', () => {
  if (state.drawer && window.innerWidth > 900) { state.drawer = false; m.redraw(); }
  if (state.view === 'grid' && state.perPage !== gridPerPage()) {
    state.perPage = gridPerPage();
    if (!state.loading) guard(loadPage);
    m.redraw();
  }
});

window.addEventListener('hashchange', () => guard(() => openDir(pathFromHash())));

/* ── 启动 ─────────────────────────────────────────────── */
async function init() {
  // 每次都重新读一次 localStorage：从后台登录后跳回本页（或「我已登录，重试」）时，
  // 手里的 state.token 还是旧的空值。
  state.token = localStorage.getItem('mocca_token') || '';
  if (state.token) {
    try {
      const me = await api('/me');
      // 角色 2 = 管理员（models.RoleAdmin）。据此给媒体条目右上角开编辑钮。
      state.isAdmin = me && me.role === 2;
      state.uid = (me && me.id) || 0;
      state.username = (me && me.username) || '';
      state.avatar = (me && me.avatar) || '01';
    } catch { /* 令牌无效：api 已清掉 token，界面切到登录引导 */ }
  }
  m.redraw();
  if (state.token || state.guest) {
    // URL 带 ?path=&page= 时按它进入（刷新/书签保持位置）；否则退回 hash 路由。
    //
    // **path 与 page 必须各自独立解析**：根目录时 syncURL 刻意不写 path（地址保持干净），
    // 所以"有没有 path"绝不能当作"要不要恢复 page"的前提 —— 否则在根目录翻页后刷新，
    // page 被直接忽略，紧接着这次加载的 syncURL 又把它从 URL 里抹掉，页码彻底找不回来。
    const st = urlStateFrom();
    const hasPath = new URLSearchParams(location.search).has('path');
    const startPath = hasPath ? st.path : pathFromHash();
    state.path = startPath;
    state.page = st.page;   // 没有 page 参数时默认为 1，不需要额外判断
    await openDir(startPath, { keepPage: true });
  }
}

m.mount(document.getElementById('app'), App);
guard(init);
