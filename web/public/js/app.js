// 管理后台前端外壳（挂 /admin/）。由 admin/index.html 以 <script type="module" src="/js/app.js"> 加载。
//
// mithril 改为走本地 ./mithril.js：后台要在局域网 http（非安全上下文）
// 下打开，不能依赖 CDN —— 断网或 CDN 挂掉就等于白屏。
//
// 模块划分（本轮重构：Tab 收敛为 用户/挂载点/目录/媒体/设置）：
//   - js/admin-core.js  共享 state、接口封装、目录树、上传、目录密码（目录/媒体两个 Tab 必须看同一个选中目录）
//   - js/admin-dirs.js  目录 Tab 与 媒体 Tab：目录级维护 + 文件级操作 + 上传
//   - js/app.js（本文件）外壳与登录，以及 用户 / 挂载点 / 设置 三个面板
// 原来的 目录密码 / 上传 / 视频切分 三个 Tab 不再存在：它们的动作按对象归属
// 搬进了目录 Tab（目录级：密码、重新索引、数据迁移、上传）与媒体 Tab（文件级：切分、封面、索引、迁移、删除）。
import m from './mithril.js';
import { staticHash } from './static-hash.js';
import { I } from './icons.js';
import {
  state, say, api, guard, roleName, loadStorages, loadUsers, loadSettings,
  loadFolderPasswords, selectDir, fmtTime, goTab, restoreURL,
} from './admin-core.js';
import { Dirs, Media } from './admin-dirs.js';

const ico = (d, cls) => m('svg.ic', { viewBox: '0 0 24 24', 'aria-hidden': 'true', class: cls || '' },
  m('path', { d }));

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

function additionOf(driver, f) {
  return driver === 'smb'
    ? { address: f.address, username: f.username, password: f.password,
        share_name: f.share_name, root_folder_path: f.root_folder_path, meta_dir: f.meta_dir }
    : { root_folder_path: f.root_folder_path, meta_dir: f.meta_dir };
}

const Storages = {
  oninit: () => guard(loadStorages),
  view() {
    const f = state.storageForm;
    return m('.panel', [
      m('h2', [ico(I.mount), ' 挂载点']),
      m('p.muted', '对外路径（mount_path）是 APP 里的根；配好之后到「目录」Tab 就能逐层发现其下的子目录。'),
      m('table', [
        m('thead', m('tr', [m('th', '挂载点'), m('th', '驱动'), m('th', '最近修改'), m('th', '状态'), m('th.actions', '操作')])),
        m('tbody', state.storages.map(s => m('tr', [
          m('td', m('code.path', s.mount_path)),
          m('td', s.driver || 'local'),
          m('td', fmtTime(s.modified)),
          m('td', s.disabled ? m('span.tag.off', '已停用') : m('span.tag', '启用')),
          m('td.actions', m('span.acts', [
            // 停用/启用：原来只能进「编辑」才能改，而编辑表单会把停用中的挂载点
            // 当新状态保存一次——这里直接按当前状态取反，一眼看清在做什么。
            m('button', {
              title: s.disabled ? '启用该挂载点' : '停用该挂载点（浏览与索引都不再访问它）',
              onclick: () => guard(async () => {
                await api('/storage/update', { method: 'POST', body: {
                  id: s.id, mount_path: s.mount_path, driver: s.driver,
                  addition: s.addition, order: s.order || 0, disabled: !s.disabled } });
                await loadStorages(); say(`${s.mount_path} 已${s.disabled ? '启用' : '停用'}`);
              }),
            }, s.disabled ? '启用' : '停用'),
            // 「在目录中打开」：省掉复制路径再去目录 Tab 一层层点开的来回
            m('button.ghost', {
              onclick: () => guard(async () => { goTab('dirs'); await selectDir(s.mount_path); }),
            }, '在目录中打开'),
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
              // .full：根目录要独占一行（后面可能还跟着「扫描外接设备」与候选列表，
              // 与附加信息目录挤在同一行会把两者搅在一起）
              m('label.full', [m('span', f.driver === 'smb' ? '共享内根目录' : '本地根目录'),
                m('input[type=text]', { value: f.root_folder_path, oninput: e => {
                    f.root_folder_path = e.target.value;
                    if (!f.meta_dir) f.meta_dir = e.target.value + '/.mocca';
                  } }),
                f.driver === 'local'
                  ? m('.scan-row', [
                      m('button.ghost.sm', { onclick: scanDevices }, state.scanning ? '扫描中…' : '扫描外接设备'),
                      m('span.form-hint', '自动列出外接硬盘，找不到再手填'),
                    ])
                  : null,
                state.scanList && state.scanList.length
                  ? m('.scan-list', state.scanList.map(c => m('button.chip', {
                      onclick: () => { f.root_folder_path = c.path; f.meta_dir = c.path + '/.mocca'; state.scanList = null; },
                    }, [
                      m('b', c.label),
                      m('span.muted', c.removable ? ' · 外接' : ' · 内置'),
                      m('br'),
                      m('span.path', c.path),
                    ])))
                  : null,
              ]),
              m('label.meta-dir-row', [m('span', '附加信息目录（meta_dir）'),
                m('input[type=text]', { value: f.meta_dir, oninput: e => f.meta_dir = e.target.value }),
                m('span.form-hint', '封面/简介存放处，默认「根目录/.mocca」，可改到设备根或别处'),
              ]),
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
        : m('button', { onclick: () => newStorage() }, [ico(I.plus), ' 新增挂载点']),
    ]);
  },
};

function newStorage() {
  state.storageForm = { id: 0, mount_path: '/media', driver: 'local', address: '', share_name: '',
    username: '', password: '', root_folder_path: '', meta_dir: '' };
}

// 扫描本机外接设备根目录：交给后端跑（Linux 读 /proc/mounts、macOS 读 mount），
// 这里只负责把候选列出来，点一条就填进 root_folder_path。找不到就退回手填。
async function scanDevices() {
  state.scanning = true; state.scanList = null;
  try {
    const list = await api('/storage/scan') || [];
    state.scanList = list;
    if (!list.length) say('未扫描到外接设备，请手填目录');
  } catch (e) {
    say('扫描失败，请手填目录', true);
  } finally {
    state.scanning = false;
  }
}
function editStorage(s) {
  let a = {};
  try { a = JSON.parse(s.addition || '{}'); } catch {}
  state.storageForm = {
    id: s.id, mount_path: s.mount_path, driver: (s.driver || 'local').toLowerCase(),
    address: a.address || '', share_name: a.share_name || '',
    username: a.username || '', password: a.password || '',
    root_folder_path: a.root_folder_path || '', meta_dir: a.meta_dir || '',
  };
}

/* ── 用户管理 ───────────────────────────────────────── */
// 角色取值与 APP 的 Session.role 对齐：0 普通用户 / 1 游客 / 2 管理员
const ROLES = [{ v: 0, label: '普通用户' }, { v: 1, label: '游客' }, { v: 2, label: '管理员' }];
const AVATARS = Array.from({ length: 12 }, (_, i) => String(i + 1).padStart(2, '0'));

// 头像图片地址：后端按 key 现画 SVG，路由挂在根上的 /static（不在 /api 之下）。
// 与 models.AvatarPresets 的取值一一对应，改那边要同步这里。
function avatarUrl(key) { return '/static/avatars/' + (key || '01') + '.png'; }

const Users = {
  oninit: () => guard(loadUsers),
  view() {
    const f = state.userForm;
    return m('.panel', [
      m('h2', [ico(I.user), ' 用户']),
      m('table', [
        m('thead', m('tr', [m('th', '头像'), m('th', '用户名'), m('th', '角色'), m('th', '专属目录'), m('th.actions', '操作')])),
        m('tbody', state.users.map(u => m('tr', [
          m('td', m('img.avatar-sm', { src: avatarUrl(u.avatar), alt: u.username, title: u.avatar || '01' })),
          m('td', u.username),
          m('td', roleName(u.role)),
          m('td', u.base_path ? m('code.path', u.base_path) : '—'),
          m('td.actions', m('span.acts', [
            // 「查看」= 到目录 Tab 选中这个专属目录：管理员不用记路径就能确认
            // 「这个人看到的是不是这里」
            u.base_path ? m('button.ghost', {
              title: `到目录 Tab 打开 ${u.base_path}`,
              onclick: () => guard(async () => { goTab('dirs'); await selectDir(u.base_path); }),
            }, '查看目录') : null,
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
                // 专属目录：候选来自「目录 Tab 已经发现过的目录」，datalist 边打字边补全。
                // 不为此加一个「浏览目录」弹窗：树已经在目录 Tab 里，跳一次就能拿到路径，
                // 而这里填的本来就是同一条路径，两套 UI 只会漂移。
                m('label', [m('span', '专属目录 base_path'),
                  m('input[type=text]', {
                    list: 'dir-options', value: f.base_path, oninput: e => f.base_path = e.target.value,
                    placeholder: '/media/home',
                  }),
                  m('datalist#dir-options', [...state.dirsSeen.keys()].sort().map(p => m('option', { value: p }))),
                  m('span.form-hint', [
                    '留空 = 不受限；填了就只让这个用户看到该目录。候选来自目录 Tab 已发现的目录，',
                    m('button.link', { onclick: () => goTab('dirs') }, '去目录 Tab 展开更多'),
                  ])]),
              ]),
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
        : m('button', { onclick: () => newUser() }, [ico(I.plus), ' 新增用户']),
    ]);
  },
};

function newUser() {
  state.userForm = { id: 0, username: '', password: '', role: 1, base_path: '', avatar: '01' };
}
function editUser(u) {
  state.userForm = { id: u.id, username: u.username, password: '', role: u.role,
    base_path: u.base_path || '', avatar: u.avatar || '01' };
}

/* ── 全局选项 ───────────────────────────────────────── */
// 选项清单由服务端下发（含 label/hint/当前值），前端不硬编码键名：
// 后端加一个开关，这里自动多一行，不用改前端。
const Settings = {
  oninit: () => guard(loadSettings),
  view() {
    return m('.panel', [
      m('h2', [ico(I.settings), ' 全局选项']),
      m('p.muted', '改动即时生效，无需重启服务。'),
      state.settings.length === 0
        ? m('p.muted', '正在读取…')
        : state.settings.map(o => m('label.opt', [
            m('.opt-text', [
              m('span.opt-name', o.label),
              m('span.opt-hint', o.hint),
            ]),
            m('input[type=checkbox]', {
              checked: o.value,
              onchange: e => toggleSetting(o, e.target.checked),
            }),
          ])),
    ]);
  },
};

function toggleSetting(opt, on) {
  return guard(async () => {
    const d = await api('/setting/update', { method: 'POST', body: { key: opt.key, value: on } });
    opt.value = d.value; // 以服务端返回为准，失败时不会让界面停在错误状态
    say(`已${d.value ? '开启' : '关闭'}「${d.label}」`);
  });
}

/* ── 外壳 ───────────────────────────────────────────── */
// Tab 顺序与职责按本轮需求固定：用户 → 挂载点 → 目录 → 媒体 → 设置。
// 上传不再是 Tab（在目录/媒体里就地做）；目录密码、重新索引、数据迁移归到目录 Tab。
const PAGES = [
  ['users',    '用户',   Users,    I.user],
  ['storages', '挂载点', Storages, I.mount],
  ['dirs',     '目录',   Dirs,     I.dir],
  ['media',    '媒体',   Media,    I.video],
  ['settings', '设置',   Settings, I.settings],
];

const App = {
  oninit: () => guard(async () => {
    if (!state.token) return;
    state.me = await api('/me');
    await loadStorages();
    await loadUsers();
    // 目录密码清单在登录后就拉一次：树上标锁与「用户·专属目录」判定都要它，
    // 等用户点进目录 Tab 才拉会让首屏的锁标闪烁。
    await loadFolderPasswords();
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
      m('nav', PAGES.map(([key, label, , icon]) => {
        const on = state.page === key;
        return m('button', { class: on ? 'on' : '', onclick: () => goTab(key) },
          m('svg.tick', { viewBox: '0 0 24 24', 'aria-hidden': 'true' },
            m('path', { d: I.check })),
          ico(icon), label);
      })),
      m(cur),
      // 后台正在跑的批量操作（切分/索引/上传/导出）要有全局可见性：
      // 面板会滚动，行内状态很容易滚出视野，跑完前拦一下切 Tab 之外的重复点击。
      state.busy ? m('p.muted.running', [ico(I.refresh, 'spin'), ` ${state.busy}…`]) : null,
    ];
  },
};

// 先读地址再 mount：Tab / 选中目录 / 媒体筛选在开机第一次绘前就要就位，
// 否则刷新后会看到“闪一下默认 Tab 再跳回原位”。
restoreURL(PAGES.map(p => p[0]));

m.mount(document.getElementById('app'), App);
