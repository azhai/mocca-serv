// 管理后台地基：共享状态、接口封装、目录树、上传、批量操作。
//
// 为什么单独一个模块：目录 Tab 与 媒体 Tab 必须看**同一棵树、同一个选中目录**
// （在目录里选中一层，切到媒体就是这层的文件），所以 state 只能有一份，定义在这里。
// app.js（外壳 + 用户/挂载点/设置）与 admin-dirs.js（目录/媒体）都从这儿取。
import m from './mithril.js';
import { staticHash } from './static-hash.js';
import { I } from './icons.js';

// 媒体类型，取值与 models/media.go 的 MediaKind 一一对应（与 home.js 同一份口径）
export const KIND = { dir: 0, unknown: 1, video: 2, audio: 3, text: 4, image: 5 };

export const state = {
  token: localStorage.getItem('mocca_token') || '',
  me: null,
  base: location.origin + '/api',
  page: 'users',
  toast: '', toastErr: false, toastAt: 0,
  login: { user: '', pwd: '' },

  // 用户 / 挂载点 / 设置
  users: [], userForm: null,
  storages: [], storageForm: null, scanList: null, scanning: false,
  settings: [],

  // 目录树（目录 Tab 与 媒体 Tab 共用）
  tree: {},        // path -> {open, kids:null|[{name,path,mount}], loading, err, real}
  sel: '/',        // 当前选中的目录
  listing: null,   // 选中目录的条目：{path, entries, counts, err, virtual}
  busy: '',        // 正在跑的批量操作名（非空时相关按钮禁用）
  folderPwd: [],   // **直接**设过密码的目录清单（继承自己按祖先链判）
  pwd: {},         // path -> 密码静态哈希（读受保护目录时缓存，父级解过子级复用）
  pwdInput: '',    // 目录面板里「新密码」输入框
  pwdAsk: null,    // 正在等口令的层：{path, resolve, reject}（见 requestPwd）
  noCover: {},     // path -> true：拉海报拿到过 404 = 本就没有封面，不再反复请求刷控制台
  dirsSeen: new Map(), // 发现过的目录 path -> name：给「用户·专属目录」当候选
  social: null,    // 弹幕/评论审核弹层：{path, name, err, loading, counts, danmaku, comments}

  // 媒体 Tab
  mediaFilter: 'all', // all | 2 | 3 | 5 | 4（按 KIND 值过滤）
  mediaSel: {},       // 文件路径 -> true
  fileState: {},      // 文件路径 -> {text, cls, busy}：行内操作结果与行自己的忙碌位（身份要稳定）
  shotSec: '00:00:01',// 抽帧时间点（与 home.js 同名默认），秒或 时:分:秒

  // 上传（挂在选中目录上，目录/媒体两个 Tab 共用一条队列）
  upload: { files: [], busy: false },
};

/* ── 提示 ─────────────────────────────────────────────── */
// 提示会自动消失（4.5s）：后台点完一个操作要去点下一个，常驻的旧提示会误导「上一步的结果」。
export function say(msg, isErr = false) {
  state.toast = msg; state.toastErr = isErr; state.toastAt = Date.now();
  const at = state.toastAt;
  setTimeout(() => { if (state.toastAt === at) { state.toast = ''; m.redraw(); } }, 4500);
  m.redraw();
}

// 统一信封：业务错误也返回 HTTP 200，只看 code。
export async function api(path, { method = 'GET', body, token = state.token } = {}) {
  const headers = {};
  if (token) headers['Authorization'] = token;
  if (body && typeof body === 'object' && !(body instanceof FormData)) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(body);
  }
  const env = await (await fetch(state.base + path, { method, headers, body })).json();
  if (env.code !== 200) throw new Error(env.message || '请求失败');
  return env.data;
}

// 统一收口异步操作：捕获错误写成提示，结束后补一次重绘。
//
// 这一句 m.redraw() 是必需的：这里用的是 fetch，而 mithril 只会自动重绘
// m.request —— fetch 的 promise 落定不会触发重绘。少了它，异步拿到数据后界面不刷新。
export async function guard(fn) {
  try {
    return await fn();
  } catch (e) {
    say(e.message, true);
  } finally {
    m.redraw();
  }
}

/* ── 路径工具 ─────────────────────────────────────────── */
export function normPath(p) {
  return '/' + String(p || '').split('/').filter(Boolean).join('/');
}
export function joinPath(dir, name) { return normPath(dir) === '/' ? '/' + name : normPath(dir) + '/' + name; }
export function parentPath(p) {
  const i = normPath(p).lastIndexOf('/');
  return i <= 0 ? '/' : p.slice(0, i);
}
export function baseName(p) { return normPath(p).split('/').filter(Boolean).pop() || '/'; }

export function fmtSize(n) {
  if (!n) return '—';
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0, v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return (i === 0 ? v : v.toFixed(v < 10 ? 1 : 0)) + ' ' + u[i];
}
export function fmtTime(s) {
  if (!s) return '—';
  const d = new Date(s);
  if (isNaN(d)) return s;
  const p = x => String(x).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

// kindIcon / kindClass 条目图标与配色类名（挂载点先判：它也是目录，但得一眼看出是另一回事）
export function kindIcon(e) {
  if (e.is_dir) return e.mount ? I.mount : I.dir;
  if (e.type === KIND.video) return I.video;
  if (e.type === KIND.audio) return I.audio;
  if (e.type === KIND.image) return I.image;
  if (e.type === KIND.text) return I.text;
  return I.file;
}
export function kindClass(e) {
  if (e.mount) return 'mount';
  if (e.is_dir) return 'dir';
  if (e.type === KIND.video) return 'video';
  if (e.type === KIND.audio) return 'audio';
  if (e.type === KIND.image) return 'image';
  if (e.type === KIND.text) return 'text';
  return 'other';
}
// isHlsAsset 旁路 HLS 自己的清单/分片：不该再出现在「视频」里被切第二遍
export function isHlsAsset(name) { return /\.(m3u8|ts|m4s)$/i.test(name || ''); }

/* ── 基础数据 ─────────────────────────────────────────── */
export async function loadStorages() { state.storages = await api('/storage/list') || []; }
export async function loadUsers() { state.users = await api('/user/list') || []; }
export async function loadSettings() { state.settings = await api('/setting/list') || []; }
export async function loadFolderPasswords() {
  const d = await api('/folder/passwords');
  state.folderPwd = (d && d.dirs) || [];
}

/* ── 目录密码 ─────────────────────────────────────────── */
// pwdOf 该目录（或其某层祖先）已解开的密码哈希；与服务端「逐级向上找最近的受保护目录」同口径。
export function pwdOf(p) {
  for (let cur = normPath(p); ; cur = parentPath(cur)) {
    if (state.pwd[cur]) return state.pwd[cur];
    if (cur === '/') return '';
  }
}

// pwdRuleOf 该目录的密码状态：{level:'self'|'inherit'|'none', dir}
// 判据是 /folder/passwords 的清单（直接设过密的目录）+ 祖先链，服务端不回哈希。
export function pwdRuleOf(p) {
  for (let cur = normPath(p); ; cur = parentPath(cur)) {
    if (state.folderPwd.includes(cur)) {
      return { level: cur === normPath(p) ? 'self' : 'inherit', dir: cur };
    }
    if (cur === '/') return { level: 'none', dir: '' };
  }
}

// requestPwd 向页面要一次口令：把请求挂在那里，等目录面板里的输入框提交后回调。
//
// 为什么不用 prompt()：prompt 在嵌入式/自动化浏览器里直接抛「prompt() is not supported」，
// 而后台本来就有页内输入框（设密码那一栏），读密码用同一套 UI 才能做到「哪里不对就在哪里修」。
export function requestPwd(p) {
  return new Promise((resolve, reject) => {
    state.pwdAsk = { path: p, v: '', resolve, reject };
    m.redraw();
  });
}

export async function answerPwd(plain) {
  const ask = state.pwdAsk;
  if (!ask) return;
  state.pwdAsk = null;
  if (!plain) { reject0(ask, new Error('已取消')); return; }
  const hash = await staticHash(plain);
  state.pwd[ask.path] = hash;
  ask.resolve(hash);
  m.redraw();
}

export function cancelPwd() {
  const ask = state.pwdAsk;
  if (!ask) return;
  state.pwdAsk = null;
  reject0(ask, new Error('已取消'));
  m.redraw();
}
function reject0(ask, e) { if (ask.reject) ask.reject(e); }

// isRealDir 这一层是不是「真的存储目录」：只要落在某个挂载点下（含挂载点本身）就是真的。
//
// 不能用 /fs/list 的 is_mount 判：它只说「这一层自己是不是挂载点」，
// /media/movies 这种挂载点里的子目录是假=False，但它完全有索引可重建、有文件可上传。
// 真正的虚拟层是「不在任何挂载点里的中间节点」（例如没配挂载点的根 /）。
export function isRealDir(p) {
  p = normPath(p);
  return (state.storages || []).some(s => {
    const mnt = normPath(s.mount_path);
    return mnt === p || p.startsWith(mnt + '/');
  });
}

// listDir 列一层目录；撞上「目录 X 需要密码」时向页面要一次口令，静态哈希后重试
// （不写 prompt：见 requestPwd）。解开的哈希按路径缓存，父级解过子级复用。
//
// hls:true 让服务端顺带探出这一层哪些片子**已经有旁路清单**（条目上的 hls 字段）：
// 媒体 Tab 据此把「切分」入口收掉，不再对着切过的片子提供第二次切分。
// social:true 同理探每个媒体的弹幕/评论条数（条目上的 social 字段），媒体 Tab 据此挂
// 计数徽标与审核入口。
// 两个探测都是默认关的：只有后台要这个信息，所以由这里显式打开（APP 不传 = 不付读成本）。
export async function listDir(p) {
  const body = { path: p, hls: true, social: true };
  const pw = pwdOf(p);
  if (pw) body.password = pw;
  try {
    return await api('/fs/list', { method: 'POST', body });
  } catch (e) {
    if (!/需要密码/.test(e.message)) throw e;
    const hash = await requestPwd(p);
    return api('/fs/list', { method: 'POST', body: { path: p, password: hash, hls: true, social: true } });
  }
}

/* ── 目录树（挂载点 → 逐层展开） ───────────────────────── */
export function node(p) {
  p = normPath(p);
  if (!state.tree[p]) state.tree[p] = { open: false, kids: null, loading: false, err: '', real: false };
  return state.tree[p];
}

// entriesToKids 把一层的条目拆成「子目录（含虚拟挂载点）」与类型计数，树与面板共用。
// dir 是这一层的路径：挂载点条目的真实路径由服务端给（path），普通子目录按 dir 拼。
function entriesToKids(entries, dir) {
  const kids = [];
  const counts = emptyCounts();
  for (const e of entries || []) {
    if (e.is_dir) {
      counts.dir++;
      kids.push({ name: e.name, path: e.path || joinPath(dir, e.name), mount: !!e.mount });
      continue;
    }
    counts[e.type === KIND.video && !isHlsAsset(e.name) ? 'video'
      : e.type === KIND.audio ? 'audio'
      : e.type === KIND.image ? 'image'
      : e.type === KIND.text ? 'text' : 'other']++;
  }
  counts.files = counts.video + counts.audio + counts.image + counts.text + counts.other;
  return { kids, counts };
}

// loadKids 展开一层：读条目、记下「发现过的目录」（给专属目录候选用）、算出计数。
// virtual 由 isRealDir 判：不在任何挂载点里的层（例如没配挂载点的根）
// 没有索引可重建、没有文件可上传，动作条要按实际能力只留能做的。
export async function loadKids(p) {
  const n = node(p);
  n.loading = true; n.err = '';
  try {
    const d = await listDir(p);
    const { kids, counts } = entriesToKids(d.content, p);
    n.kids = kids;
    n.open = true;
    n.real = !!d.is_mount; // 这一层自己是不是一个挂载点（图标/面包屑用）
    for (const k of kids) state.dirsSeen.set(k.path, k.name);
    return { entries: d.content || [], counts, virtual: !isRealDir(p) };
  } finally {
    n.loading = false;
  }
}

// selectDir 选中并加载一层，目录/媒体两个 Tab 都调它（切 Tab 不重新拉，用同一份 listing）
export async function selectDir(p) {
  p = normPath(p);
  state.sel = p;
  syncURL(); // 选中层一改变就写地址：刷新后要回得来
  state.mediaSel = {};
  state.fileState = {};
  state.upload = { files: [], busy: false };
  state.social = null; // 换层就把审核弹层收掉：它还挂在上一层那个文件上
  state.listing = null;
  m.redraw();
  try {
    const r = await loadKids(p);
    state.listing = { path: p, entries: r.entries, counts: r.counts, virtual: r.virtual, err: '' };
  } catch (e) {
    state.listing = { path: p, entries: [], counts: emptyCounts(), virtual: false, err: e.message };
  }
  m.redraw();
}

export function emptyCounts() {
  return { dir: 0, video: 0, audio: 0, image: 0, text: 0, other: 0, files: 0 };
}

/* ── URL 状态：刷新 / 收藏后回到原位 ───────────────────── */
// 后台原本把 Tab、选中目录、媒体筛选只存在内存里：按一次 F5 就全丢，人回到默认 Tab、
// 树折叠在根。这里沿用 home.js 的做法（?path=&page= + replaceState）把这三样写进地址。
// 只在**变更点**调 syncURL：浏览器对 replaceState 有频率限制（Firefox 是 30 秒 100 次），
// 放进 view 里每次重绘都写一次会被掐掉。
export function syncURL() {
  const qp = new URLSearchParams();
  if (state.page !== 'users') qp.set('tab', state.page);
  if (state.sel && state.sel !== '/') qp.set('dir', state.sel);
  if (state.mediaFilter !== 'all') qp.set('kind', String(state.mediaFilter));
  const s = qp.toString();
  history.replaceState(null, '', location.pathname + (s ? '?' + s : ''));
}

// restoreURL 启动时读回地址。Tab 名由调用方把 PAGES 的键传进来判定，不在这里
// 另抄一份清单（抄一份就会与 PAGES 漂移）；非法值一律忽略：宁可回到默认 Tab，也不能白屏。
export function restoreURL(tabs) {
  const qp = new URLSearchParams(location.search);
  const tab = qp.get('tab');
  if (tab && tabs.includes(tab)) state.page = tab;
  const dir = qp.get('dir');
  if (dir) state.sel = normPath(dir);
  // 筛选值只认四类媒体（与 admin-dirs 的 FILTERS 同一口径）：其他值会让列表空掉，
  // 看起来像「这个目录没文件」，不如直接忽略回到「全部」。
  const kind = Number(qp.get('kind'));
  if ([KIND.video, KIND.audio, KIND.image, KIND.text].includes(kind)) state.mediaFilter = kind;
}

// goTab / setKindFilter：改状态并同步地址。直接给 state.page 赋值的地方，刷新后就丢了。
// goTab 切页时一并收掉弹幕审核弹层：它是媒体页的临时视图，state.social 不清的话，
// 不关面板直接切走、再切回来时它会自己弹回来（和 selectDir 里那条是同一条意图：
// 面板挂在哪个文件上，就得在那个文件那一层里关掉）。
export function goTab(key) { state.page = key; state.social = null; syncURL(); }
export function setKindFilter(v) { state.mediaFilter = v; syncURL(); }

// ensureSelVisible 把 URL 里带回来的深层目录上面的层逐层展开：不展开的话树停在
// 折叠的根上，选中的那层看不见。只在开机第一次选层时跑一次，
// 之后用户点树 / 点筛选都不应该再重复扫盘。
let selRestored = false;
export async function ensureSelVisible() {
  if (selRestored) return;
  selRestored = true;
  const parts = normPath(state.sel || '/').split('/').filter(Boolean);
  parts.pop(); // 目标层自己由 selectDir 负责
  let cur = '/';
  for (const name of parts) {
    cur = joinPath(cur, name);
    if (node(cur).kids) continue;
    try { await loadKids(cur); }
    catch { return; } // 中途要口令或读不了：停在能展开的位置，selectDir 会接着问
  }
}

// mediaEntries 选中目录里的媒体文件（旁路 HLS 自己的清单/分片不算视频），可按类型过滤。
// 视频排在最前：后台最常见的一屏就是「先找到那个片子」。
export function mediaEntries(filter = state.mediaFilter) {
  const all = (state.listing?.entries || []).filter(e => !e.is_dir);
  const isVid = e => e.type === KIND.video && !isHlsAsset(e.name);
  const vids = all.filter(isVid);
  const rest = all.filter(e => !isVid(e));
  const list = vids.concat(rest);
  if (filter === 'all') return list;
  if (filter === KIND.video) return vids;
  return list.filter(e => e.type === filter);
}

export function fullPath(name) { return joinPath(state.sel, name); }

/* ── 批量执行 ─────────────────────────────────────────── */
// runBatch 并发跑一批任务，全程占用 state.busy（按钮据此禁用），每完成一条重绘一次。
// 并发数按「IO 为主、再多只把磁盘打满」的经验取 2~3。worker 自己写行内状态；
// 抛出来的异常交给 onErr（没给就只记一条提示，不让整批中断）。
export async function runBatch(label, items, worker, { conc = 2, onErr } = {}) {
  if (!items.length) return;
  state.busy = label;
  const queue = items.slice();
  const slot = async () => {
    while (queue.length) {
      const it = queue.shift();
      try { await worker(it); }
      catch (e) { if (onErr) onErr(it, e); else say(`${label}：部分失败 — ${e.message}`, true); }
      m.redraw();
    }
  };
  await Promise.all(Array.from({ length: Math.min(conc, queue.length) }, slot));
  state.busy = '';
  m.redraw();
}

// solo 单独一个耗时操作（不是一批）也占用 state.busy：按钮据此置灰，
// 顶栏那条「正在跑…」也能看见 —— 切目录/换 Tab 期间重复点同一个操作会互相踩。
export async function solo(label, fn) {
  if (state.busy) return;
  state.busy = label;
  m.redraw();
  try {
    return await fn();
  } finally {
    state.busy = '';
    m.redraw();
  }
}

// mark 写一条行内状态（媒体 Tab 的「状态」列）。
// 必须**原地改字段**，不能 `{...旧, text, cls}` 换成新对象：withBusy 抓的是行对象的引用，
// 对象被替换后它 finally 里的 `busy = false` 写在孤儿对象上，渲染读到的新对象 busy 永远是 true
// —— 表现就是这一行的按钮做完一次操作就永久置灰，只能「重新列出」才能救（冒烟里实测到了）。
export function mark(path, text, cls = 'muted') {
  const st = state.fileState[path] || (state.fileState[path] = { text: '', cls: 'muted', busy: false });
  st.text = text;
  st.cls = cls;
}

/* ── 上传 ─────────────────────────────────────────────── */
// 必须用 XHR：fetch 拿不到上传进度。一个文件一个请求，各自一条进度条。
export function uploadOne(file, dir) {
  return new Promise((resolve, reject) => {
    const fd = new FormData();
    fd.append('file', file.raw);
    const xhr = new XMLHttpRequest();
    xhr.open('POST', `${state.base}/fs/put?path=${encodeURIComponent(dir)}`);
    if (state.token) xhr.setRequestHeader('Authorization', state.token);
    xhr.upload.onprogress = e => {
      if (e.lengthComputable) { file.pct = Math.round(e.loaded / e.total * 100); m.redraw(); }
    };
    xhr.onload = () => {
      try {
        const env = JSON.parse(xhr.responseText);
        env.code === 200 ? resolve(env.data) : reject(new Error(env.message || '上传失败'));
      } catch { reject(new Error('响应解析失败')); }
    };
    xhr.onerror = () => reject(new Error('网络错误'));
    xhr.send(fd);
  });
}

// startUpload 把 state.upload.files 送进 state.sel。上传完刷新这一层，让新文件立刻出现在树与列表里。
export async function startUpload() {
  const s = state.upload;
  if (!s.files.length) return;
  s.busy = true;
  const dir = state.sel;
  await runBatch('上传中', s.files, async f => {
    f.status = 'uploading';
    try { await uploadOne(f, dir); f.status = 'done'; f.pct = 100; }
    catch (e) { f.status = 'error'; f.err = e.message; }
  }, { conc: 3 });
  s.busy = false;
  await selectDir(dir); // 刷新：新文件、新计数
  say(`上传完成：${s.files.filter(f => f.status === 'done').length} / ${s.files.length}`);
}

export function pickFiles(fileList) {
  state.upload = {
    files: [...fileList].map(f => ({ raw: f, name: f.name, size: f.size, pct: 0, status: 'pending' })),
    busy: false,
  };
}

/* ── 数据迁移：打包下载 ───────────────────────────────── */
// exportPaths 把若干媒体在 .mocca 里的数据打包成 tar.gz 下载。
//
// 不能走 api()：那条路把响应体当 JSON 解析，而这里的响应是二进制流。
// 成败靠 Content-Type 区分 —— 失败仍是业务信封（HTTP 200 + JSON），得先认出来再抛错。
export async function exportPaths(paths) {
  if (!paths.length) throw new Error('先勾选要导出的条目');
  const resp = await fetch(state.base + '/fs/export', {
    method: 'POST',
    headers: { 'Authorization': state.token, 'Content-Type': 'application/json' },
    body: JSON.stringify({ paths }),
  });
  if ((resp.headers.get('Content-Type') || '').includes('json')) {
    const env = await resp.json();
    throw new Error(env.message || '导出失败');
  }
  const blob = await resp.blob();
  const cd = resp.headers.get('Content-Disposition') || '';
  const fm = /filename="([^"]+)"/.exec(cd);
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = fm ? fm[1] : `mocca-export-${paths.length}.tar.gz`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

/* ── 其它共用动作 ─────────────────────────────────────── */
// reindexPaths 重新索引：索引按目录一份，一次请求带上全部路径由后端按目录归拢
// （逐个文件发请求会让同一个目录被重写 N 遍，见 handlers.FsReindex）。
export async function reindexPaths(paths) {
  return api('/fs/reindex', { method: 'POST', body: { paths } });
}

/* ── 封面（海报）直链 ─────────────────────────────────── */
// posterRev 封面一变就自增：/meta/poster 的 URL 不带版本号时浏览器会一直给缓存里的旧图，
// 换完封面看上去像「没生效」（与 home.js 的 coverRev 同招）。
export let posterRev = 0;
export function bumpPosterRev() { posterRev++; }
export function posterURL(p) {
  const q = new URLSearchParams({ path: normPath(p), token: state.token, r: String(posterRev) });
  const pw = pwdOf(p);
  if (pw) q.set('password', pw);
  return '/meta/poster?' + q;
}

// driveURL 取某文件的播放/预览地址：让服务端拼（raw_url 口径归它管），受保护目录再补 ?password=
export async function driveURL(p) {
  const body = { path: p };
  const pw = pwdOf(p);
  if (pw) body.password = pw;
  const d = await api('/fs/get', { method: 'POST', body });
  let u = d.raw_url || ('/d' + encPath(p));
  if (pw) u += (u.includes('?') ? '&' : '?') + 'password=' + encodeURIComponent(pw);
  return location.origin + u;
}

// encPath 逐段编码路径：文件名里的空格、中文、#?% 都要编码，否则取流会取错文件。
export function encPath(p) {
  return '/' + normPath(p).split('/').filter(Boolean).map(encodeURIComponent).join('/');
}

export function roleName(r) {
  return ({ 0: '普通用户', 1: '游客', 2: '管理员' })[r] || String(r);
}
