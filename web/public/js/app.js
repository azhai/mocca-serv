// 管理后台前端（挂 /admin/）。由 admin/index.html 以 <script type="module" src="/js/app.js"> 加载。
//
// mithril 改为走本地 ./mithril.js：后台要在局域网 http（非安全上下文）
// 下打开，不能依赖 CDN —— 断网或 CDN 挂掉就等于白屏。
import m from './mithril.js';

// 与服务端约定一致：口令先做静态哈希再传输。实现放在 static-hash.js，
// 因为浏览应用（home.js）要用同一份哈希 —— 两个副本迟早会算出不同的值。
import { staticHash } from './static-hash.js';

const state = {
  token: localStorage.getItem('mocca_token') || '',
  me: null,
  base: location.origin + '/api',
  page: 'storages',
  toast: '', toastErr: false,
  login: { user: '', pwd: '' },
  storages: [], storageForm: null,
  users: [], userForm: null,
  meta: { path: '', loaded: null, authors: [] },
  cover: null, // 封面图制作面板的编辑态；null = 收起
  folder: { path: '/media/', pwd: '' },
  upload: { dir: '/', files: [], busy: false },
};

function say(msg, isErr = false) { state.toast = msg; state.toastErr = isErr; m.redraw(); }

// 统一信封：业务错误也返回 HTTP 200，只看 code
async function api(path, { method = 'GET', body, token = state.token } = {}) {
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

// apiForm 与 api 同源，走 multipart：封面图是二进制，塞不进 JSON。
// 不要设 Content-Type —— 浏览器要自己补 boundary，写死会让服务端解析不出表单。
async function apiForm(path, fd) {
  const headers = {};
  if (state.token) headers['Authorization'] = state.token;
  const env = await (await fetch(state.base + path, { method: 'POST', headers, body: fd })).json();
  if (env.code !== 200) throw new Error(env.message || '请求失败');
  return env.data;
}

// 统一收口异步操作：捕获错误，并在结束后补一次重绘。
//
// 这一句 m.redraw() 是必需的：这里用的是 fetch，而 mithril 只会自动重绘
// m.request —— fetch 的 promise 落定不会触发重绘。少了它，异步拿到数据后
// 界面不刷新，表现为「登录成功要点两次才跳转」（第二次点击才顺带触发重绘）。
async function guard(fn) {
  try {
    await fn();
  } catch (e) {
    say(e.message, true);
  } finally {
    m.redraw();
  }
}

/* ── 登录 ───────────────────────────────────────────── */
const Login = {
  view: () => m('.panel.narrow', [
    m('.login-head', [
      m('img.logo', { src: '../logo.png', alt: 'Mocca', width: 68, height: 68 }),
      m('h1', 'Mocca 管理后台'),
      m('p.muted', { style: 'margin: 6px 0 0' }, '首次部署时注册的第一个账号会自动成为管理员。'),
    ]),
    // 登录失败必须显示出来：guard 把错误写进 state.toast，而这里是登录页
    // 唯一能显示文字的位置。早先这行读的是另一个从未被赋值的字段，
    // 于是任何失败都表现为「点按钮毫无反应、一个提示都没有」。
    state.toast ? m('p.err', state.toast) : null,
    m('label', [m('span', '用户名'),
      m('input[type=text]', { value: state.login.user, oninput: e => state.login.user = e.target.value })]),
    m('label', [m('span', '密码'),
      m('input[type=password]', { autocomplete: 'current-password',
        value: state.login.pwd, oninput: e => state.login.pwd = e.target.value })]),
    m('button.block', {
      onclick: () => guard(async () => {
        state.toast = ''; // 清掉上一次的报错，避免重试时留着旧提示
        const d = await api('/auth/login/hash', {
          method: 'POST', token: '',
          body: { username: state.login.user, password: await staticHash(state.login.pwd) },
        });
        state.token = d.token;
        localStorage.setItem('mocca_token', d.token);
        state.me = await api('/me');
        // 必须 await：否则 guard 会在数据回来之前就补完重绘，列表要等下次交互才出现
        await loadStorages();
        await loadUsers();
      }),
    }, '登录'),
  ]),
};

/* ── 挂载点管理 ─────────────────────────────────────── */
const DRIVERS = [
  { v: 'local', label: '本地目录 (Local)' },
  { v: 'smb',   label: 'Samba / SMB' },
];

async function loadStorages() { state.storages = await api('/storage/list') || []; }

function additionOf(driver, f) {
  return driver === 'smb'
    ? { address: f.address, username: f.username, password: f.password,
        share_name: f.share_name, root_folder_path: f.root_folder_path }
    : { root_folder_path: f.root_folder_path };
}

const Storages = {
  oninit: () => guard(loadStorages),
  view() {
    const f = state.storageForm;
    return m('.panel', [
      m('h2', '挂载点'),
      m('p.muted', '对外路径（mount_path）是 APP 里的根，驱动私有配置放在 addition 里。'),
      m('table', [
        m('thead', m('tr', [m('th', '挂载点'), m('th', '驱动'), m('th', '状态'), m('th.actions', '操作')])),
        m('tbody', state.storages.map(s => m('tr', [
          m('td', s.mount_path),
          m('td', s.driver || 'local'),
          m('td', s.disabled ? m('span.tag.off', '已停用') : m('span.tag', '启用')),
          m('td.actions', m('span.acts', [
            m('button.ghost', { onclick: () => editStorage(s) }, '编辑'),
            m('button.danger', {
              onclick: () => guard(async () => {
                if (!confirm(`确认删除挂载点 ${s.mount_path}？`)) return;
                await api('/storage/delete', { method: 'POST', body: { id: s.id } });
                await loadStorages(); say('已删除');
              }),
            }, '删除'),
          ])),
        ]))),
      ]),
      f
        ? m('.form-block', [
            m('h2', f.id ? '编辑挂载点' : '新增挂载点'),
            m('.grid', [
              m('label', [m('span', '挂载点（如 /media）'),
                m('input[type=text]', { value: f.mount_path, oninput: e => f.mount_path = e.target.value })]),
              m('label', [m('span', '驱动'),
                m('select', { value: f.driver, onchange: e => f.driver = e.target.value },
                  DRIVERS.map(d => m('option', { value: d.v }, d.label)))]),
              f.driver === 'smb' ? m('label', [m('span', '地址（host 或 host:445）'),
                m('input[type=text]', { value: f.address, oninput: e => f.address = e.target.value })]) : null,
              f.driver === 'smb' ? m('label', [m('span', '共享名 share_name'),
                m('input[type=text]', { value: f.share_name, oninput: e => f.share_name = e.target.value })]) : null,
              f.driver === 'smb' ? m('label', [m('span', '用户名'),
                m('input[type=text]', { value: f.username, oninput: e => f.username = e.target.value })]) : null,
              f.driver === 'smb' ? m('label', [m('span', '密码'),
                m('input[type=password]', { value: f.password, oninput: e => f.password = e.target.value })]) : null,
              m('label', [m('span', f.driver === 'smb' ? '共享内根目录' : '本地根目录'),
                m('input[type=text]', { value: f.root_folder_path, oninput: e => f.root_folder_path = e.target.value })]),
            ]),
            m('.form-actions', [
              m('button', {
                onclick: () => guard(async () => {
                  const body = {
                    id: f.id || 0, mount_path: f.mount_path, driver: f.driver,
                    addition: JSON.stringify(additionOf(f.driver, f)),
                  };
                  await api(f.id ? '/storage/update' : '/storage/create', { method: 'POST', body });
                  state.storageForm = null;
                  await loadStorages(); say('已保存');
                }),
              }, '保存'),
              m('button.ghost', { onclick: () => state.storageForm = null }, '取消'),
            ]),
          ])
        : m('button', { onclick: () => newStorage() }, '新增挂载点'),
    ]);
  },
};

function newStorage() {
  state.storageForm = { id: 0, mount_path: '', driver: 'local', address: '', share_name: '',
    username: '', password: '', root_folder_path: '' };
}
function editStorage(s) {
  let a = {};
  try { a = JSON.parse(s.addition || '{}'); } catch {}
  state.storageForm = {
    id: s.id, mount_path: s.mount_path, driver: (s.driver || 'local').toLowerCase(),
    address: a.address || '', share_name: a.share_name || '',
    username: a.username || '', password: a.password || '',
    root_folder_path: a.root_folder_path || '',
  };
}

/* ── 用户管理 ───────────────────────────────────────── */
// 角色取值与 APP 的 Session.role 对齐：0 普通用户 / 1 游客 / 2 管理员
const ROLES = [{ v: 0, label: '普通用户' }, { v: 1, label: '游客' }, { v: 2, label: '管理员' }];
const AVATARS = Array.from({ length: 12 }, (_, i) => String(i + 1).padStart(2, '0'));

// 头像图片地址：后端按 key 现画 SVG，路由挂在根上的 /static（不在 /api 之下）。
// 与 models.AvatarPresets 的取值一一对应，改那边要同步这里。
function avatarUrl(key) { return '/static/avatars/' + (key || '01') + '.png'; }

async function loadUsers() { state.users = await api('/user/list') || []; }

const Users = {
  oninit: () => guard(loadUsers),
  view() {
    const f = state.userForm;
    return m('.panel', [
      m('h2', '用户'),
      m('table', [
        m('thead', m('tr', [m('th', '头像'), m('th', '用户名'), m('th', '角色'), m('th', '专属目录'), m('th.actions', '操作')])),
        m('tbody', state.users.map(u => m('tr', [
          m('td', m('img.avatar-sm', { src: avatarUrl(u.avatar), alt: u.username, title: u.avatar || '01' })),
          m('td', u.username),
          m('td', roleName(u.role)),
          m('td', u.base_path || '—'),
          m('td.actions', m('span.acts', [
            m('button.ghost', { onclick: () => editUser(u) }, '编辑'),
            m('button.danger', {
              onclick: () => guard(async () => {
                if (!confirm(`确认删除用户 ${u.username}？`)) return;
                await api('/user/delete', { method: 'POST', body: { id: u.id } });
                await loadUsers(); say('已删除');
              }),
            }, '删除'),
          ])),
        ]))),
      ]),
      f
        ? m('.form-block', [
            m('h2', f.id ? '编辑用户' : '新增用户'),
            m('.form-section', [
              m('h3', '账号'),
              m('.grid', [
                m('label', [m('span', '用户名'),
                  m('input[type=text]', { value: f.username, oninput: e => f.username = e.target.value })]),
                m('label', [m('span', f.id ? '新密码（留空不改）' : '密码'),
                  // autocomplete=new-password：别让浏览器把已存的登录口令自动填进来。
                  // 用户编辑时若被自动填充、再顺手点保存，会**静默改掉那个账号的口令**。
                  m('input[type=password]', { autocomplete: 'new-password',
                    value: f.password, oninput: e => f.password = e.target.value })]),
              ]),
            ]),
            m('.form-section', [
              m('h3', '权限与目录'),
              m('.grid', [
                m('label', [m('span', '角色'),
                  m('select', { value: f.role, onchange: e => f.role = Number(e.target.value) },
                    ROLES.map(r => m('option', { value: r.v }, r.label)))]),
                m('label', [m('span', '专属目录 base_path'),
                  m('input[type=text]', { value: f.base_path, oninput: e => f.base_path = e.target.value })]),
              ]),
              m('p.form-hint', '专属目录留空 = 不受限；填了就只让这个用户看到该目录（如 /media/home）。'),
            ]),
            m('.form-section', [
              m('h3', '头像'),
              m('.avatars', AVATARS.map(a => m('button.avatar-pick', {
                class: (f.avatar || '01') === a ? 'on' : '',
                title: '头像 ' + a,
                onclick: () => { f.avatar = a; },
              }, m('img', { src: avatarUrl(a), alt: a })))),
            ]),
            m('.form-actions', [
              m('button', {
                onclick: () => guard(async () => {
                  // 新建走 create（密码必填），编辑走 update（密码留空不改）
                  if (!f.id && !f.password) throw new Error('请填写密码');
                  // 密码**必须**先做静态哈希再发：服务端 SetPassword 只做 bcrypt，
                  // 而登录是拿 staticHash(明文) 去比对的。这里若发明文，库里存的就是
                  // bcrypt(明文)，该账号将**永远登录不了**，而且不报任何错。
                  const { password, ...rest } = f;
                  const body = { ...rest, id: f.id || 0,
                    password: password ? await staticHash(password) : '' };
                  await api(f.id ? '/user/update' : '/user/create', { method: 'POST', body });
                  state.userForm = null;
                  await loadUsers(); say('已保存');
                }),
              }, '保存'),
              m('button.ghost', { onclick: () => state.userForm = null }, '取消'),
            ]),
          ])
        : m('button', { onclick: () => newUser() }, '新增用户'),
    ]);
  },
};

function roleName(r) { return (ROLES.find(x => x.v === r) || { label: r }).label; }
function newUser() {
  state.userForm = { id: 0, username: '', password: '', role: 1, base_path: '', avatar: '01' };
}
function editUser(u) {
  state.userForm = { id: u.id, username: u.username, password: '', role: u.role,
    base_path: u.base_path || '', avatar: u.avatar || '01' };
}

/* ── 元数据 ─────────────────────────────────────────── */
// 取值必须与 APP 端 MediaKind 逐一对齐（models/media.go）：video=2 / audio=3 / image=5。
// 这里曾经按 1/2/3 顺排，结果是「保存元数据」永远返回「媒体类型不合法」——
// 因为 1 在契约里是 unknown，而图片是 5。错位不报错，只是存不进去。
const KINDS = [{ v: 2, label: '视频' }, { v: 3, label: '音频' }, { v: 5, label: '图片' }];

const Meta = {
  view() {
    const s = state.meta;
    return m('.panel', [
      m('h2', '媒体元数据'),
      m('p.muted', '路径用 APP 里的完整路径（如 /media/电影/a.mp4）。图片只存路径/大小/标题。'),
      m('.row', [
        m('input[type=text]', {
          placeholder: '/media/a.mp4', value: s.path,
          oninput: e => s.path = e.target.value,
        }),
        m('button.ghost', {
          onclick: () => guard(async () => {
            const d = await api('/meta?path=' + encodeURIComponent(s.path));
            s.loaded = d.meta; s.authors = (d.authors || []).join(', ');
            say('已载入');
          }),
        }, '载入'),
        m('button.ghost', {
          onclick: () => { s.loaded = { path: s.path, kind: 2 }; s.authors = ''; },
        }, '新建'),
      ]),
      s.loaded ? m('div', [
        m('.grid', [
          m('label', [m('span', '类型'),
            m('select', { value: s.loaded.kind, onchange: e => s.loaded.kind = Number(e.target.value) },
              KINDS.map(k => m('option', { value: k.v }, k.label)))]),
          m('label', [m('span', '标题（可空）'),
            m('input[type=text]', { value: s.loaded.title || '', oninput: e => s.loaded.title = e.target.value })]),
          m('label', [m('span', '时长（毫秒，仅音视频）'),
            m('input[type=number]', { value: s.loaded.duration || 0,
              oninput: e => s.loaded.duration = Number(e.target.value) })]),
          m('label', [m('span', '封面（隐藏目录内相对路径）'),
            m('input[type=text]', { value: s.loaded.cover || '', oninput: e => s.loaded.cover = e.target.value })]),
          m('label', [m('span', '作者（英文逗号分隔，可多个）'),
            m('input[type=text]', { value: s.authors, oninput: e => s.authors = e.target.value })]),
        ]),
        m('label', [m('span', '简介'),
          m('input[type=text]', { value: s.loaded.description || '',
            oninput: e => s.loaded.description = e.target.value })]),
        CoverMaker.view(),
        m('button', {
          onclick: () => guard(async () => {
            await api('/meta/save', { method: 'POST', body: {
              path: s.path, kind: s.loaded.kind, size: s.loaded.size || 0,
              title: s.loaded.title || '', duration: s.loaded.duration || 0,
              cover: s.loaded.cover || '', description: s.loaded.description || '',
              authors: s.authors.split(',').map(x => x.trim()).filter(Boolean),
            }});
            say('已保存');
          }),
        }, '保存元数据'),
      ]) : null,
    ]);
  },
};

/* ── 封面图制作 ─────────────────────────────────────── */
// 封面图是**在浏览器里画出来的**：canvas 用得上系统字体，中文标题才能正常排版，
// 服务端只负责把画好的 PNG 落进 <数据目录>/.mocca/covers/（handlers/cover_admin.go）。
// 「制作封面图」因此天然属于管理后台的能力：纯 API 构建里既没有这块画布，
// 也没有接收它的接口（同名处理器在 -tags noweb 下只回一句说明）。
const COVER_W = 1280, COVER_H = 720; // 画布尺寸即落盘尺寸，16:9 是媒体库封面的通用比例

// 与预设头像同一套低饱和配色（handlers/avatar.go 的 avatarColors）：
// 两个界面来回看时才不会像两个项目。
const COVER_COLORS = [
  ['#6E8B3D', '#33421B'], ['#5C7A6B', '#2B3A34'], ['#6B7A8F', '#313A46'],
  ['#8F7A9A', '#41364A'], ['#A2685A', '#4C2C25'], ['#96A55C', '#47502A'],
];

let coverCanvas = null;  // oncreate 时赋值；输入变化后重画就画在它上面
let coverBgImage = null; // 用户挑的背景图，可选

const CoverMaker = {
  view() {
    const s = state.meta;
    if (!state.cover) {
      return m('.form-section', [
        m('h3', '封面图'),
        m('.row', [
          m('button.ghost', { onclick: openCover }, '制作封面图'),
          m('span.muted', s.loaded.cover ? '当前：' + s.loaded.cover : '还没有封面'),
        ]),
      ]);
    }
    const c = state.cover;
    return m('.form-section', [
      m('h3', '封面图（1280×720）'),
      m('canvas.cover-canvas', {
        width: COVER_W, height: COVER_H,
        // 用 oncreate 而不是 onupdate：画布建好之后的重画由输入事件直接调 drawCover()，
        // 不必让 mithril 在每次重绘时再画一遍。
        oncreate: v => { coverCanvas = v.dom; drawCover(); },
      }),
      m('.grid', [
        m('label', [m('span', '标题'),
          m('input[type=text]', { value: c.title,
            oninput: e => { c.title = e.target.value; drawCover(); } })]),
        m('label', [m('span', '副标题（可空）'),
          m('input[type=text]', { value: c.subtitle,
            oninput: e => { c.subtitle = e.target.value; drawCover(); } })]),
      ]),
      m('.row', [
        m('label', [m('span', '背景图（可选）'),
          m('input[type=file][accept=image/*]', { onchange: pickCoverBg })]),
        m('button.ghost', {
          onclick: () => { coverBgImage = null; drawCover(); },
        }, '去掉背景'),
        m('button', { onclick: () => guard(saveCover) }, '保存封面'),
        m('button.ghost', {
          onclick: () => { state.cover = null; coverCanvas = null; },
        }, '收起'),
      ]),
    ]);
  },
};

function openCover() {
  const s = state.meta;
  const base = (s.path || '').split('/').pop() || '';
  coverBgImage = null;
  state.cover = {
    // 标题默认取元数据里的，其次取文件名（去掉扩展名）：多数情况下一次都不用改
    title: s.loaded.title || base.replace(/\.[^.]+$/, '') || '未命名',
    subtitle: s.authors || '',
  };
}

function pickCoverBg(e) {
  const file = e.target.files && e.target.files[0];
  e.target.value = ''; // 清空才能连续选同一个文件
  if (!file) return;
  const img = new Image();
  img.onload = () => { coverBgImage = img; drawCover(); };
  img.onerror = () => say('这张图片读不出来', true);
  img.src = URL.createObjectURL(file);
}

// drawCover 把当前编辑态画到画布上：所有输入变化都走它，保证「所见即所存」。
function drawCover() {
  const c = state.cover;
  if (!coverCanvas || !c) return;
  const ctx = coverCanvas.getContext('2d');
  const [top, bottom] = COVER_COLORS[hashText(c.title) % COVER_COLORS.length];

  const grad = ctx.createLinearGradient(0, 0, COVER_W, COVER_H);
  grad.addColorStop(0, top);
  grad.addColorStop(1, bottom);
  ctx.fillStyle = grad;
  ctx.fillRect(0, 0, COVER_W, COVER_H);

  if (coverBgImage) {
    // 按短边铺满裁切，避免拉变形；再压一层暗底，浅色画面上的白字才看得清
    const k = Math.max(COVER_W / coverBgImage.width, COVER_H / coverBgImage.height);
    const w = coverBgImage.width * k, h = coverBgImage.height * k;
    ctx.drawImage(coverBgImage, (COVER_W - w) / 2, (COVER_H - h) / 2, w, h);
    ctx.fillStyle = 'rgba(0,0,0,.45)';
    ctx.fillRect(0, 0, COVER_W, COVER_H);
  }

  const pad = 72;
  let y = pad;
  ctx.textBaseline = 'top';
  ctx.fillStyle = '#FDFBF4';
  ctx.font = '600 84px "PingFang SC","Hiragino Sans GB","Microsoft YaHei",sans-serif';
  for (const line of wrapText(ctx, c.title || '未命名', COVER_W - pad * 2)) {
    ctx.fillText(line, pad, y);
    y += 100;
  }
  if (c.subtitle) {
    ctx.font = '400 36px "PingFang SC","Hiragino Sans GB","Microsoft YaHei",sans-serif';
    ctx.fillStyle = 'rgba(253,251,244,.85)';
    ctx.fillText(wrapText(ctx, c.subtitle, COVER_W - pad * 2)[0], pad, y + 8);
  }
  // 左下角一道强调色，纯装饰：成品不至于是一块纯色
  ctx.fillStyle = 'rgba(253,251,244,.9)';
  ctx.fillRect(pad, COVER_H - pad - 8, 96, 8);
}

// hashText 把标题摊成一个稳定下标：同一个标题永远同一套配色。
function hashText(s) {
  let h = 0;
  for (const ch of String(s)) h = (h * 31 + ch.codePointAt(0)) >>> 0;
  return h;
}

// wrapText 逐字符量宽折行：中英数混排的标题都能量准，
// 按空格切词在中文上会直接失效（整句一个词）。
function wrapText(ctx, text, maxWidth) {
  const lines = [];
  let line = '';
  for (const ch of String(text)) {
    if (ch === '\n') { lines.push(line); line = ''; continue; }
    if (line && ctx.measureText(line + ch).width > maxWidth) { lines.push(line); line = ch; }
    else line += ch;
  }
  if (line) lines.push(line);
  return lines.length ? lines : [''];
}

async function saveCover() {
  const s = state.meta;
  const blob = await new Promise(res => coverCanvas.toBlob(res, 'image/png'));
  if (!blob) throw new Error('画布导出失败');
  const fd = new FormData();
  // 文件名带扩展名没关系：服务端会去掉它，按嗅探出的真实类型补扩展名
  fd.append('name', (s.path || '').split('/').pop() || 'cover');
  fd.append('file', blob, 'cover.png');
  const d = await apiForm('/meta/cover', fd);
  s.loaded.cover = d.cover;
  state.cover = null;
  coverCanvas = null;
  coverBgImage = null;
  say('封面已生成：' + d.cover + '；点「保存元数据」写入这条记录');
}

/* ── 目录密码 ───────────────────────────────────────── */
const FolderPwd = {
  view() {
    const s = state.folder;
    return m('.panel', [
      m('h2', '目录密码'),
      m('p.muted', '给父目录设一次即可保护整棵子树；密码同样以 bcrypt 存储，不留明文。' +
        'APP 在 list/get 的 password 字段、取流时用 ?password= 传入（值同样是静态哈希）。'),
      m('label', [m('span', '目录路径（如 /media/私密）'),
        m('input[type=text]', { value: s.path, oninput: e => s.path = e.target.value })]),
      m('label', [m('span', '密码'),
        m('input[type=password]', { value: s.pwd, oninput: e => s.pwd = e.target.value })]),
      m('.row', [
        m('button', {
          onclick: () => guard(async () => {
            if (!s.pwd) throw new Error('请填写密码');
            await api('/folder/password', {
              method: 'POST',
              body: { path: s.path, password: await staticHash(s.pwd) },
            });
            say('已设置目录密码');
          }),
        }, '设置'),
        m('button.ghost', {
          onclick: () => guard(async () => {
            await api('/folder/password', { method: 'POST', body: { path: s.path, password: '' } });
            say('已清除该目录密码');
          }),
        }, '清除'),
        m('button.ghost', {
          onclick: () => guard(async () => {
            const d = await api('/folder/status?path=' + encodeURIComponent(s.path));
            say(d.protected ? `受保护，规则来自 ${d.protected_dir}` : '该路径未受保护');
          }),
        }, '查询状态'),
      ]),
    ]);
  },
};

/* ── 上传 ───────────────────────────────────────────── */
const Upload = {
  view() {
    const s = state.upload;
    return m('.panel', [
      m('h2', '批量上传'),
      m('label', [m('span', '目标目录'),
        m('input[type=text]', { value: s.dir, oninput: e => s.dir = e.target.value })]),
      m('.row', [
        m('input[type=file][multiple]', {
          onchange: e => {
            s.files = [...e.target.files].map(f => ({ raw: f, name: f.name, pct: 0, status: 'pending' }));
            e.target.value = '';
          },
        }),
        m('button', { disabled: s.busy || s.files.length === 0, onclick: startUpload },
          s.busy ? '上传中…' : '开始上传'),
      ]),
      s.files.length === 0
        ? m('p.muted', '每个文件单独发一次请求，因此各自有一条独立进度条。')
        : s.files.map(f => m('.file', [
            m('.file-head', [
              m('.file-name', f.name),
              m('span', { class: f.status === 'error' ? 'err' : (f.status === 'done' ? 'ok' : 'muted') },
                f.status === 'error' ? f.err : (f.status === 'done' ? '完成' : f.pct + '%')),
            ]),
            m('.bar', m('i', { style: `width:${f.status === 'done' ? 100 : f.pct}%` })),
          ])),
    ]);
  },
};

// 必须用 XHR：fetch 拿不到上传进度
function uploadOne(file, dir, token) {
  return new Promise((resolve, reject) => {
    const fd = new FormData();
    fd.append('file', file.raw);
    const xhr = new XMLHttpRequest();
    xhr.open('POST', `${state.base}/fs/put?path=${encodeURIComponent(dir)}`);
    if (token) xhr.setRequestHeader('Authorization', token);
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

async function startUpload() {
  const s = state.upload;
  s.busy = true;
  const queue = s.files.slice();
  const worker = async () => {
    while (queue.length) {
      const f = queue.shift();
      f.status = 'uploading';
      try { await uploadOne(f, s.dir, state.token); f.status = 'done'; f.pct = 100; }
      catch (e) { f.status = 'error'; f.err = e.message; }
      m.redraw();
    }
  };
  await Promise.all(Array.from({ length: Math.min(3, queue.length) }, worker));
  s.busy = false;
  m.redraw();
}

/* ── 外壳 ───────────────────────────────────────────── */
// Material 的 check 图标路径（M3 分段按钮的选中段用；尺寸由 CSS 定，与段宽无关）
const CHECK_ICON = 'M9 16.17 4.83 12l-1.42 1.41L9 19 21 7l-1.41-1.41z';

const PAGES = [
  ['storages', '挂载点', Storages],
  ['users', '用户', Users],
  ['meta', '元数据', Meta],
  ['folder', '目录密码', FolderPwd],
  ['upload', '上传', Upload],
];

const App = {
  oninit: () => guard(async () => {
    if (!state.token) return;
    state.me = await api('/me');
    await loadStorages();
    await loadUsers();
  }),
  view() {
    if (!state.me) return m(Login);
    const cur = (PAGES.find(p => p[0] === state.page) || PAGES[0])[2];
    return [
      m('.panel.appbar', [
        m('.row', [
          m('.brand', { style: 'margin-right:auto' }, [
            m('img.logo.sm', { src: '../logo.png', alt: 'Mocca' }),
            m('h1', 'Mocca 管理后台'),
            // 紧挨着标题放一个对向链接：两个页面互相指向，来回跳不用记地址。
            // 同目录下的静态页、共用 localStorage 里的令牌，跳过去不用重新登录；
            // 整页跳转，所以是 <a> 而不是按钮。
            m('a.browse-link', { href: '/', title: '浏览应用：目录树、页内播放与图片预览' }, '返回首页'),
          ]),
          m('span.muted', `${state.me.username}（${roleName(state.me.role)}）`),
          m('button.ghost', {
            onclick: () => { state.token = ''; state.me = null; localStorage.removeItem('mocca_token'); },
          }, '退出'),
        ]),
        // 纯选择器写法：不依赖 mithril 的 class 合并规则
        state.toast
          ? m(state.toastErr ? 'p.toast.err' : 'p.toast', state.toast)
          : null,
      ]),
      // 分段按钮组：M3 的选中段除了填色，还带一个对勾。
      // 对勾**每个 tab 都渲染**（未选中的由 CSS 隐藏，且绝对定位不占位）——
      // 只给选中项渲染会让后续 tab 位移，详见 CSS 里 svg.tick 的注释。
      m('nav', PAGES.map(([key, label]) => {
        const on = state.page === key;
        return m('button', { class: on ? 'on' : '', onclick: () => state.page = key },
          m('svg.tick', { viewBox: '0 0 24 24', 'aria-hidden': 'true' },
            m('path', { d: CHECK_ICON })),
          label);
      })),
      m(cur),
    ];
  },
};

m.mount(document.getElementById('app'), App);
