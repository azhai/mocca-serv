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
