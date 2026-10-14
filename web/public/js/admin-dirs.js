// 目录 Tab 与 媒体 Tab：共用同一棵目录树、同一个选中目录（state.sel 在 admin-core.js）。
//
// 设计要点（都是用户明确要的「集中」「傻瓜化」「自动发现」）：
//   - 进去就是挂载点，点一层展开一层（懒加载），不用手填任何路径；
//   - 选中一层自动列一次 /fs/list，顺手算出「几个子目录 / 视频 / 音频 / 图片 / 文本」，
//     动作按钮按计数决定可用性 —— 没有视频就不会出现「补齐封面」「切分」「导出」；
//   - 原来散在三个 Tab 的操作搬到这里：目录密码（目录级）、重新索引与数据迁移
//     （目录级 + 媒体 Tab 的批量）、上传（目录级和媒体级）。
//   - 弹幕与评论在媒体 Tab 行内审核：列目录顺带探出条数，非零才挂徽标与入口，
//     弹层里逐条删、按类清空（五个 Tab 不因此加一个）。
import m from './mithril.js';
import { staticHash } from './static-hash.js';
import { I } from './icons.js';
import {
  KIND, state, say, api, guard, normPath, fmtSize, fmtTime,
  kindIcon, kindClass, isHlsAsset, pwdOf, pwdRuleOf, loadFolderPasswords,
  node, loadKids, selectDir, emptyCounts, mediaEntries, fullPath,
  runBatch, mark, startUpload, pickFiles, exportPaths, reindexPaths,
  driveURL, posterURL, bumpPosterRev, answerPwd, cancelPwd, solo, loadStorages,
  goTab, setKindFilter, ensureSelVisible,
} from './admin-core.js';

/* ── 共用的小部件 ─────────────────────────────────────── */
const ico = (d, cls) => m('svg.ic', { viewBox: '0 0 24 24', 'aria-hidden': 'true', class: cls || '' },
  m('path', { d }));

// PwdAsk 读受保护目录时的页内口令栏（由 listDir 挂起等待，见 admin-core 的 requestPwd）
const PwdAsk = {
  view() {
    const ask = state.pwdAsk;
    if (!ask) return null;
    // 口令存在 ask.v 上，不是 view 里的局部变量：mithril 在每个 DOM 事件后自动重绘，
    // 重绘会重建 view 的闭包，局部 let v 每次被打回空串 —— 「查看」就永远提交空口令
    // （上一版就是这么坏的，手输必然跨帧，人根本用不了这个栏）。
    const submit = () => guard(() => answerPwd(ask.v));
    return m('.pwd-ask', [
      ico(I.lock),
      m('span', `目录 ${ask.path} 需要口令：`),
      m('input[type=password]', {
        autocomplete: 'current-password', placeholder: '口令', value: ask.v,
        oninput: e => { ask.v = e.target.value; },
        oncreate: el => el.dom.focus(),
        onkeydown: e => { if (e.key === 'Enter') submit(); },
      }),
      m('button', { disabled: !ask.v, onclick: submit }, '查看'),
      m('button.ghost', { onclick: cancelPwd, title: '不读了，回到上一层' }, '取消'),
    ]);
  },
};

// ib 图标按钮：title 就是按钮名（后台按钮多，文字标签会把面板挤爆；
// 悬停/长按出说明，语义相同的动作用同一枚图标，两处 Tab 一看就认得出）。
function ib({ icon, label, onclick, dis, cls, busy }) {
  return m('button.iconbtn' + (cls ? '.' + cls : ''), {
    title: label, 'aria-label': label, disabled: dis || busy, onclick,
  }, [ico(icon), busy ? m('em.dots', '…') : null]);
}

// counts 条数徽章：点一个 = 去媒体 Tab 只看这一类（计数与筛选是同一件事的两面）
function countChip(kindVal, n, icon) {
  return m('button.countchip', {
    disabled: !n, title: n ? `到媒体 Tab 只看这 ${n} 个` : '这一层没有这类文件',
    onclick: () => { setKindFilter(kindVal); goTab('media'); },
  }, [ico(icon), m('b', String(n))]);
}

/* ── 目录树（两个 Tab 共用同一个视图） ────────────────── */
const Tree = {
  view: ({ attrs }) => m('.tree', [
    m('.tree-root', [
      m('span.rootpath', { class: state.sel === '/' ? 'on' : '', onclick: () => selectDir('/'), title: '刷新这一层' }, '/'),
      m('button.iconbtn.ghost', { title: '重新加载整棵树', onclick: () => guard(refreshTree) }, ico(I.refresh)),
    ]),
    renderKids('/'),
  ]),
};

function renderKids(p) {
  const n = node(p);
  if (n.loading) return m('p.muted.pad', '正在发现子目录…');
  if (n.err) return m('p.err.pad', [n.err, ' ', m('button.ghost.sm', { onclick: () => guard(() => expand(p)) }, '重试')]);
  if (!n.kids) return null;
  if (!n.kids.length) return m('p.muted.pad', '（没有子目录）');
  return m('ul', n.kids.map(k => {
    const kn = node(k.path);
    const rule = pwdRuleOf(k.path);
    return m('li', [
      m('.tnode', { class: [state.sel === k.path ? 'sel' : '', k.mount ? 'is-mount' : ''].filter(Boolean).join(' ') }, [
        m('button.caret', {
          // 点箭头 = 只展开/收起；点名字 = 选中这一层。两件事分开，免得想展开却把选中挪走
          onclick: e => { e.stopPropagation(); guard(() => toggle(k.path)); },
          title: kn.open ? '收起' : '展开', 'aria-label': kn.open ? '收起' : '展开',
        }, ico(kn.open ? I.down : I.right)),
        m('button.tname', { onclick: () => guard(() => selectDir(k.path)), title: k.path }, [
          ico(kindIcon({ is_dir: true, mount: k.mount })),
          m('span.nm', k.name),
          rule.level !== 'none' ? ico(rule.level === 'self' ? I.lock : I.lockOpen, 'lock ' + rule.level) : null,
        ]),
      ]),
      kn.open ? renderKids(k.path) : null,
    ]);
  }));
}

async function toggle(p) {
  const n = node(p);
  if (n.open) { n.open = false; return; }
  if (!n.kids) await expand(p); else n.open = true;
}

// expand 展开一层（带缓存：展开过就不再请求，重进 Tab 不重复扫盘）
async function expand(p) {
  try { await loadKids(p); }
  catch (e) { const n = node(p); n.err = e.message; say(e.message, true); }
}

// refreshTree 清空缓存重扫：外部插了硬盘、目录里刚拷完文件时用一次，
// 比逐层点开重进页面快。保留当前选中，看的内容不跳走。
async function refreshTree() {
  const sel = state.sel;
  state.tree = {};
  state.pwd = {};
  await loadFolderPasswords();
  await loadKids('/');
  await selectDir(sel);
  say('目录树已重新加载');
}

/* ── 目录 Tab ─────────────────────────────────────────── */
export const Dirs = {
  oninit: () => guard(async () => {
    // 挂载点清单必须先拿到：isRealDir 靠它判「这一层能不能做目录级维护」
    if (!state.storages.length) await loadStorages();
    if (!node('/').kids) await loadKids('/');
    if (!state.folderPwd.length) await loadFolderPasswords();
    if (!state.listing) { await ensureSelVisible(); await selectDir(state.sel || '/'); }
  }),
  view: () => m('.cols', [
    m('.col.left', m(Tree)),
    m('.col.right', [m(PwdAsk), m(DirPanel), m(UploadBox)]),
  ]),
};

// DirPanel 选中这一层的「自动发现 + 就地操作」面板
const DirPanel = {
  view() {
    const l = state.listing;
    const p = state.sel;
    const busy = !!state.busy;
    const rule = pwdRuleOf(p);
    const counts = l ? l.counts : emptyCounts();
    const virtual = !!l && l.virtual;
    return m('.panel', [
      m('h2', ['目录 ', m('code.path', p)]),
      l && l.err
        ? m('p.err', [l.err, ' ', m('button.ghost.sm', {
            title: '这一层可能设了密码；上一次输入的口令若不对会被忽略，点这里可以重新输入',
            onclick: () => guard(async () => { delete state.pwd[p]; await selectDir(p); }),
          }, '重试')])
        : null,
      // 计数条：就是「自动发现」的结果，也顺手当筛选入口
      m('.row.counts', l ? [
        countChip(KIND.dir, counts.dir, I.dir),
        countChip(KIND.video, counts.video, I.video),
        countChip(KIND.audio, counts.audio, I.audio),
        countChip(KIND.image, counts.image, I.image),
        countChip(KIND.text, counts.text, I.text),
      ] : m('p.muted', '正在读取…')),
      // 虚拟层（不在任何挂载点里，例如还没配挂载点的根）：
      // 没有真实存储就没有索引/封面/迁移可言，只留能做的，并说清为什么
      virtual ? m('p.muted.hint', [
        ico(I.info), ' 这一层不在任何挂载点里，没有对应的存储目录：索引/封面/迁移对它没有意义。',
        '先在「挂载点」Tab 配好存储，或直接展开下面的挂载点选真实目录。']) : null,
      m('.actions-block', [
        m('h3', '目录维护'),
        m('.row', [
          ib({ icon: I.sync, label: '重新索引本目录（按增量重建 .index.jsonl：没变的沿用旧记录，变了才重算 sha1）',
            dis: virtual || busy, busy: state.busy === '重新索引本目录',
            onclick: () => guard(() => solo('重新索引本目录', async () => {
              const d = await reindexPaths([p]);
              say(`重新索引完成：${d.files} 条记录，${d.rehashed} 条重算了 sha1（${d.dirs} 个目录）`);
            })) }),
          ib({ icon: I.camera, label: '补齐封面：给本目录缺封面的视频逐个截图（时间点见下方输入框）',
            dis: virtual || busy || !counts.video, busy: state.busy === '补齐封面',
            onclick: () => guard(() => solo('补齐封面', async () => {
              const d = await api('/fs/patch', { method: 'POST', body: { path: p, sec: state.shotSec || '1' } });
              // done=新生成、covered=本来就有封面、failed=截图失败（见 handlers.FsPatch）
              bumpPosterRev();
              say(`补齐封面：新增 ${d.done} 个，已有封面 ${d.covered} 个${d.failed ? `，失败 ${d.failed} 个` : ''}`);
            })) }),
          ib({ icon: I.export, label: '数据迁移：把本目录所有视频在 .mocca 里的数据打包下载（tar.gz）',
            dis: virtual || busy || !counts.video, busy: state.busy === '导出本目录',
            onclick: () => guard(async () => {
              const vids = mediaEntries(KIND.video).map(e => fullPath(e.name));
              if (!vids.length) throw new Error('本目录没有可导出的视频');
              await runBatch('导出本目录', [vids], paths => exportPaths(paths));
              say(`已打包本目录 ${vids.length} 个视频的 .mocca 数据`);
            }) }),
          m('span.muted', '时间点 '),
          m('input[type=text].secs', {
            value: state.shotSec, size: 9, title: 'FFmpeg 截图时间点：秒数或 时:分:秒（如 90、1:30）',
            oninput: e => state.shotSec = e.target.value,
          }),
        ]),
      ]),
      m('.actions-block', [
        m('h3', '目录密码'),
        m('p.muted', rule.level === 'self' ? [
            ico(I.lock, 'lock self'), ' 本级已设密码：整棵子树都要口令才能浏览。']
          : rule.level === 'inherit' ? [
            ico(I.lockOpen, 'lock inherit'), ` 继承自上级 `, m('code.path', rule.dir),
            '：想单独改密码，到上级目录里清除，或在这里另设（另设只影响本级及以下的「未设密」子目录）。']
          : '未设密码：浏览这一层不需要口令。'),
        m('.row', [
          m('input[type=password].pwd-in', {
            placeholder: rule.level === 'none' ? '新密码' : '改成新密码',
            autocomplete: 'new-password', value: state.pwdInput,
            oninput: e => state.pwdInput = e.target.value,
          }),
          m('button', {
            disabled: !state.pwdInput || busy,
            onclick: () => guard(async () => {
              await api('/folder/password', { method: 'POST', body: { path: p, password: await staticHash(state.pwdInput) } });
              state.pwd[p] = await pwdOfCache(p, state.pwdInput); // 刚设的密码顺手解开，省一次口令
              state.pwdInput = '';
              await loadFolderPasswords(); await selectDir(p);
              say('已设置目录密码');
            }),
          }, '设置'),
          ib({ icon: I.lockOpen, label: '清除本级密码（继承上级的清不掉：请去上级目录清除）',
            dis: rule.level !== 'self' || busy,
            onclick: () => guard(async () => {
              await api('/folder/password', { method: 'POST', body: { path: p, password: '' } });
              delete state.pwd[p];
              await loadFolderPasswords(); await selectDir(p);
              say('已清除本级密码');
            }) }),
        ]),
      ]),
    ]);
  },
};

// pwdOfCache 设置/改完密码后把这个哈希缓存起来：否则刚设完的自己反倒要再问一次口令
async function pwdOfCache(p, plain) {
  const hash = await staticHash(plain);
  state.pwd[p] = hash;
  return hash;
}

/* ── 上传（目录 Tab 与 媒体 Tab 各挂一处，共用一条队列） ─ */
const UploadBox = {
  view() {
    const s = state.upload;
    return m('.actions-block', [
      m('h3', [ico(I.upload), ' 上传到 ', m('code.path', state.sel)]),
      m('.row', [
        // ponytail: 原生文件选择框足够；不做拖拽区，多一层事件与样式收益不明显
        m('input[type=file][multiple].file-in', {
          disabled: s.busy, onchange: e => { pickFiles(e.target.files); e.target.value = ''; },
        }),
        m('button', { disabled: s.busy || !s.files.length, onclick: () => guard(startUpload) },
          s.busy ? '上传中…' : `开始上传（${s.files.length}）`),
      ]),
      s.files.map(f => m('.file', [
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

/* ── 媒体 Tab ─────────────────────────────────────────── */
const FILTERS = [
  ['all', '全部', null],
  [KIND.video, '视频', I.video],
  [KIND.audio, '音频', I.audio],
  [KIND.image, '图片', I.image],
  [KIND.text, '文本', I.text],
];

const MediaView = {
  oninit: () => guard(async () => {
    if (!state.storages.length) await loadStorages();
    if (!node('/').kids) await loadKids('/');
    if (!state.listing) { await ensureSelVisible(); await selectDir(state.sel || '/'); }
  }),
  view() {
    const l = state.listing;
    const list = mediaEntries();
    // 探测结果要说得出口：一进来就知道这一层有几部已经切过、为什么那几行没有剪刀。
    const cutN = (l?.entries || []).filter(e => e.hls).length;
    return m('.cols', [
      m('.col.left', m(Tree)),
      m('.col.right', [
        m(PwdAsk),
        m('.panel', [
          m('h2', ['媒体 ', m('code.path', state.sel),
            m('button.ghost.sm', { style: 'margin-left:auto', onclick: () => guard(() => selectDir(state.sel)) }, '重新列出')]),
          l && l.err ? m('p.err', l.err) : null,
          m('.row.filters', FILTERS.map(([v, label, icon]) => {
            const n = v === 'all' ? mediaEntries('all').length
              : (l ? (l.counts[v === KIND.video ? 'video' : v === KIND.audio ? 'audio' : v === KIND.image ? 'image' : 'text'] || 0) : 0);
            return m('button.fchip' + (state.mediaFilter === v ? '.on' : ''), {
              disabled: v !== 'all' && !n,
              onclick: () => setKindFilter(v),
            }, [icon ? ico(icon) : null, m('span', label), m('b', String(n))]);
          })),
          m(BatchBar),
          cutN ? m('p.muted', [
            ico(I.cut, 'cut note'), ` 探测到 ${cutN} 个视频已有旁路清单（在各自目录的 .hls/ 里），这些行不再提供切分。`,
          ]) : null,
          !l || !list.length
            ? m('p.muted', l && l.virtual ? '这一层没有文件（中间层），展开挂载点选真实目录。'
              : l && l.counts.files ? '当前筛选下没有条目，换一类看看。' : '这一层没有文件。')
            : m('table.media', [
                m('thead', m('tr', [m('th.selh', ''), m('th', '文件'), m('th', '大小'), m('th', '修改时间'),
                  m('th', '状态'), m('th.actions', '操作')])),
                m('tbody', list.map(e => MediaRow(e))),
              ]),
        ]),
        m(UploadBox),
        m(SocialPanel),
      ]),
    ]);
  },
};

// 导出名与外壳里的 Tab 表一致：外壳只认 Media，本模块内部叫 MediaView 以免与 Tab 键名混误。
export const Media = MediaView;

// BatchBar 批量条：勾选数出来才出现（一次只列 5 个动作，按常用度排序）
const BatchBar = {
  view() {
    const picked = Object.keys(state.mediaSel).filter(k => state.mediaSel[k]);
    if (!picked.length) return null;
    const busy = !!state.busy;
    // 视频子集从列好的条目里取（用 type 判定，不靠扩展名正则：
    // 中文名、带空格的段落、.hls 分片都已经由服务端归过类）
    const inSel = new Set(picked);
    // 已有旁路清单的（e.hls）从批量切分里自动剔掉：不是「不想切」，是「已经切过了」，
    // 重复切只是把自己上一次的产物再切一遍。行上一律不标 = 探测没开/服务端不支持时照常可切。
    const videoRows = mediaEntries(KIND.video);
    const cutSet = new Set(videoRows.filter(e => e.hls).map(e => fullPath(e.name)));
    const vids = videoRows.map(e => fullPath(e.name)).filter(p => inSel.has(p) && !cutSet.has(p));
    const selCut = picked.filter(p => cutSet.has(p)).length;
    return m('.row.batch', [
      m('span.muted', `已选 ${picked.length} 个`),
      m('button.ghost.sm', { onclick: () => { setKindFilter('all'); toggleAll(true); } }, '全选'),
      m('button.ghost.sm', { onclick: () => toggleAll(false) }, '清空'),
      m('button', {
        disabled: busy || !vids.length,
        title: vids.length
          ? `只切不转（-c copy），产物在 <目录>/.hls/ 里，浏览端自动优先走 HLS${selCut ? `（自动跳过 ${selCut} 个已切分）` : ''}`
          : `选中的 ${selCut} 个视频都已经有旁路清单，无需重复切分`,
        onclick: () => guard(() => runBatch('切分 HLS', vids, hlsWorker(), { conc: 2 })) }, '切分 HLS'),
      selCut ? m('span.muted', `跳过已切分 ${selCut} 个`) : null,
      m('button.ghost', { disabled: busy, title: '按增量刷新这些文件所在目录的索引：一次请求全部路径，后端按目录归拢',
        onclick: () => guard(() => doReindex(picked)) }, '重新索引'),
      m('button.ghost', { disabled: busy, title: '数据迁移：把 .mocca 里的海报/附加信息/弹幕打包下载',
        onclick: () => guard(() => runBatch('导出', [picked], ps => exportPaths(ps))) }, '数据迁移'),
      m('button.danger', {
        disabled: busy, title: '删除文件本身（不可恢复）',
        onclick: () => guard(async () => {
          if (!confirm(`确认删除选中的 ${picked.length} 个文件？文件会被真的删掉，不可恢复。`)) return;
          await runBatch('删除', picked, async p => { await api('/fs/remove', { method: 'POST', body: { path: p } }); },
            { conc: 2, onErr: (p, e) => mark(p, e.message, 'err') });
          await selectDir(state.sel);
        }) }, '删除'),
    ]);
  },
};

function toggleAll(on) {
  state.mediaSel = {};
  if (on) for (const e of mediaEntries()) if (!e.is_dir) state.mediaSel[fullPath(e.name)] = true;
}

// doReindex 一批文件一次请求：索引按目录一份，逐个发会让同一层被重写 N 遍
async function doReindex(paths) {
  await solo('重新索引', async () => {
    const d = await reindexPaths(paths);
    paths.forEach(p => mark(p, `索引已刷新（共 ${d.files} 条）`, 'ok'));
    say(`重新索引完成：${d.files} 条记录，其中 ${d.rehashed} 条重算了 sha1（${d.dirs} 个目录）`);
  });
}

/* ── 行内操作 ─────────────────────────────────────────── */
// rowState 取/建一行自己的状态（view 首次渲染也要读 busy，所以只补空、不覆盖）。
function rowState(p) {
  return state.fileState[p] || (state.fileState[p] = { text: '', cls: 'muted', busy: false });
}

// withBusy 跑一个行内操作：busy 一律在 finally 里落下，且行的状态对象身份必须稳定
// （见 admin-core.mark：它只能原地改字段，不能换新对象，否则这里的 finally 写不到
// 渲染正在读的那个对象上，按钮就会做完一次永久置灰）。
async function withBusy(p, text, fn) {
  const st = rowState(p);
  st.busy = true; mark(p, text, 'muted'); m.redraw();
  try {
    return await fn();
  } catch (e) {
    mark(p, e.message, 'err');
    throw e;
  } finally {
    st.busy = false;
    m.redraw();
  }
}

// coverDone 封面变了两件事必须一起做：自增 URL 版本号（绕开浏览器缓存）+
// 抹掉「这行没封面」的缓存位，否则新封面永远不显示。
function coverDone(p, text) {
  delete state.noCover[p];
  bumpPosterRev();
  mark(p, text, 'ok');
}

// markCutLocally 切完就地把自己这一条标成「已切分」：不重列一遍目录也能立刻收掉剪刀按钮，
// 免得用户在同一个会话里对着同一部片子再点一次。
function markCutLocally(p) {
  for (const e of (state.listing?.entries || [])) if ((e.path || fullPath(e.name)) === p) e.hls = true;
}

// hlsWorker 切分一个视频：已有清单时后端会跳过（skipped），照样标出来，别让人以为切了两遍
function hlsWorker() {
  return p => withBusy(p, '切分中…', async () => {
    const d = await api('/fs/hls', { method: 'POST', body: { path: p } });
    mark(p, d && d.skipped ? '已有清单，已跳过' : '切分完成', 'ok');
    markCutLocally(p);
  });
}

// shotCover FFmpeg 抽帧作封面（时间点取自工具条的输入框）
async function shotCover(p) {
  await withBusy(p, '截图…', async () => {
    await api('/fs/shot', { method: 'POST', body: { path: p, sec: state.shotSec || '1' } });
    coverDone(p, '封面已更新');
  });
}

// pickCoverFile 弹原生文件选择框：临时造一个 input，选完即弃。
// 不给每行常驻一个隐藏 <input> 再靠 DOM 顺序去找它 —— mithril 重绘时兄弟节点会重建，
// 靠位置关系引用等于埋一个偶发失效的 bug。
function pickCoverFile(cb) {
  const inp = document.createElement('input');
  inp.type = 'file';
  inp.accept = 'image/*';
  inp.onchange = () => cb(inp.files[0]);
  inp.click();
}

// uploadCover 上传替换封面：/fs/cov 直接吞图片字节，路径与口令走查询串（<img> 带不了头的同一套约定）
async function uploadCover(p, file) {
  if (!file) return;
  await withBusy(p, '上传封面…', async () => {
    const q = new URLSearchParams({ path: normPath(p) });
    const pw = pwdOf(p);
    if (pw) q.set('password', pw);
    const res = await fetch(state.base + '/fs/cov?' + q, {
      method: 'POST', body: await file.arrayBuffer(),
      headers: state.token ? { Authorization: state.token } : {},
    });
    const env = await res.json().catch(() => ({}));
    if (!env || env.code !== 200) throw new Error((env && env.message) || '上传失败');
    coverDone(p, '封面已更新');
  });
}

async function removeCover(p) {
  await withBusy(p, '清除封面…', async () => {
    const d = await api('/fs/uncov', { method: 'POST', body: { path: p } });
    coverDone(p, d && d.removed === false ? '本来就没有封面' : '封面已删除');
  });
}

// openIn 打开播放/预览：先让服务端拼好 raw_url（受保护目录会带上取流口令），再开新标签
async function openIn(p) {
  try { window.open(await driveURL(p), '_blank', 'noopener'); }
  catch (e) { say(e.message, true); }
}

/* ── 一行条目 ─────────────────────────────────────────── */
function MediaRow(e) {
  const p = e.path || fullPath(e.name);
  const st = rowState(p);
  const isVid = e.type === KIND.video && !isHlsAsset(e.name);
  // 已经有旁路清单：这一行只收掉「切分」入口（再切 = 把自己的产物切第二遍），
  // 其余动作照常。
  const cut = isVid && !!e.hls;
  const isAud = e.type === KIND.audio;
  const coverable = isVid || isAud;
  // 弹幕/评论条数：只有服务端探到非零才下发字段（admin-core 的 listDir 带 social:true）。
  // 没这条数据就没得管，所以**徽标与按钮一起**只在 e.social 存在时给。
  const soc = e.social;
  const dis = !!state.busy || st.busy;
  const acts = [];
  if (isVid || isAud || e.type === KIND.image || e.type === KIND.text)
    acts.push(ib({ icon: I.play, label: '在新标签页播放 / 预览', dis, onclick: () => openIn(p) }));
  if (soc)
    acts.push(ib({ icon: I.dm, label: `审核弹幕与评论（弹幕 ${soc.danmaku}、评论 ${soc.comments}）`,
      dis, onclick: () => guard(() => openSocial(e)) }));
  if (isVid && !cut)
    acts.push(ib({ icon: I.cut, label: '切分旁路 HLS（-c copy，只切不转）', dis, busy: st.busy, onclick: () => guard(() => hlsWorker()(p)) }));
  if (coverable) {
    acts.push(ib({ icon: I.camera, label: `FFmpeg 在 ${state.shotSec || '1'} 秒处截图作封面`, dis, busy: st.busy, onclick: () => guard(() => shotCover(p)) }));
    acts.push(ib({ icon: I.upload, label: '上传本机图片作封面', dis, busy: st.busy,
      onclick: () => pickCoverFile(file => guard(() => uploadCover(p, file))) }));
    acts.push(ib({ icon: I.block, label: '清除封面', dis, busy: st.busy, onclick: () => guard(() => removeCover(p)) }));
  }
  // 重新索引与数据迁移不在行内给按钮：重新索引本来就是按目录归拢的（单行一个
  // 图标只会让人以为它只作用于这个文件），迁移也是一批一批跑的事——两个都只在
  // 批量条上（见 BatchBar）与目录 Tab 的「目录维护」里。
  acts.push(ib({ icon: I.trash, label: '删除文件（不可恢复）', dis, cls: 'danger', onclick: () => guard(async () => {
    if (!confirm(`确认删除 ${e.name}？文件会被真的删掉，不可恢复。`)) return;
    await api('/fs/remove', { method: 'POST', body: { path: p } });
    await selectDir(state.sel);
    say('已删除');
  }) }));

  return m('tr', [
    m('td.selh', m('input[type=checkbox]', {
      checked: !!state.mediaSel[p], disabled: !!state.busy,
      onchange: ev => { state.mediaSel[p] = ev.target.checked; },
    })),
    m('td.fname' + (cut ? '.cut' : ''), { title: p }, [
      m('span.kindic', { class: kindClass(e) }, ico(kindIcon(e))),
      // 没拿到过封面的行不再发请求：无封面是常态（音频、新拷进来的片子），
      // 每次都拉一次 /meta/poster 只会把控制台塞满 404、把列表拉慢。
      coverable && !state.noCover[p] ? m('img.thumb', {
        src: posterURL(p), loading: 'lazy', alt: '',
        onerror: () => { state.noCover[p] = true; m.redraw(); },
      }) : null,
      m('span.nm', e.name),
      cut ? m('span.cutmark', { title: '已经有旁路清单（<目录>/.hls/…/index.m3u8），所以不再提供切分' }, '已切分') : null,
      soc ? m('span.socialmark', { title: `弹幕 ${soc.danmaku} 条、评论 ${soc.comments} 条：点操作里的弹幕图标审核` },
        `弹 ${soc.danmaku} · 评 ${soc.comments}`) : null,
    ]),
    m('td', fmtSize(e.size)),
    m('td', fmtTime(e.modified)),
    m('td.st', { class: st.text ? st.cls : cut ? 'cut' : st.cls },
      st.text || (cut ? '已有清单' : '—')),
    m('td.actions', m('span.acts', acts)),
  ]);
}

/* ── 弹幕与评论审核（媒体 Tab 的行内弹层） ─────────────── */

// 类型的取值与 mediaindex 的 iota 对齐（0 评论 / 1 弹幕）。这里只拿来决定
// 确认框里说“删这条评论”还是“删这条弹幕”，所以写定两个数字，不在前端另外抄一份枚举。
const DM_COMMENT = 0;

// avatarURL 发送当时的头像快照 key → 预设头像直链（与 home.js、app.js 同一口径：
// 服务端按 key 现画 SVG，这里只拼地址，不去 join 用户表）。
const avatarURL = key => '/static/avatars/' + (key || '01') + '.png';

// fmtOffset 弹幕出现的时刻：毫秒 → 分:秒（超过一小时才带上小时位）。
// 取整跟 home.js 的播放器同口径（floor，不是四舍五入）：后台写 0:08、播放器跳过去显示 0:07，
// 审核的人会以为错了时间；小时位是后台多给的，播放器只会把 3600 秒写成 60:00。
function fmtOffset(ms) {
  const t = Math.max(0, Math.floor((ms || 0) / 1000));
  const p = x => String(x).padStart(2, '0');
  const h = Math.floor(t / 3600), mnt = Math.floor((t % 3600) / 60);
  return (h ? h + ':' + p(mnt) : mnt) + ':' + p(t % 60);
}

// openSocial 打开某媒体的审核弹层。行上的徽标就是入口：探到非零计数才有得管。
async function openSocial(e) {
  state.social = {
    path: e.path || fullPath(e.name), name: e.name,
    loading: true, err: '', sha1: '',
    counts: { danmaku: 0, comments: 0 }, danmaku: [], comments: [],
  };
  m.redraw();
  await reloadSocial();
}

function closeSocial() {
  state.social = null;
  // 显式重绘：Esc 路径上是 document 的原生监听，不经 mithril 的事件委托，
  // 拿不到「每个 DOM 事件后自动重绘」—— 少这一句就是 state 清空了而遮罩还盖着整页（冒烟实测）。
  m.redraw();
}

// reloadSocial 从服务端重读一屏。删一条与清一类之后都走它：计数是探出来的，
// 不在前端自己减 1 —— 猜的数和读的数混在一起显示，错的时分不清是哪边。
async function reloadSocial() {
  const s = state.social;
  if (!s) return;
  s.loading = true; s.err = '';
  try {
    const d = await api('/fs/social', { method: 'POST', body: { path: s.path } });
    s.sha1 = d.sha1 || '';
    s.counts = d.counts || { danmaku: 0, comments: 0 };
    // 服务端按写入顺序回（弹幕 jsonl 是追加的），面板标题说的是「按出现时刻」——
    // 当场排一下才名实相符（不靠后端预排：后端只保证存/取，展示排序是前端的事）。
    s.danmaku = (d.danmaku || []).slice().sort((a, b) => a.offset - b.offset);
    s.comments = d.comments || [];
    syncSocialBadge(s.path, s.counts);
  } catch (e) {
    s.err = e.message;
  } finally {
    s.loading = false;
    // api() 走的是 fetch，mithril 不会自动重绘（只认 m.request）
    m.redraw();
  }
}

// syncSocialBadge 把行上的计数改成服务端刚给出的数。两类都归零就摘掉徽标：
// 已清空还挂着个 “弹 3”，下次点进去是空的，比不标更误导。
function syncSocialBadge(p, counts) {
  const hit = (state.listing?.entries || []).find(e => (e.path || fullPath(e.name)) === p);
  if (!hit) return;
  hit.social = (counts.danmaku || counts.comments) ? counts : undefined;
}

// delSocialRow 删单条：复用浏览端的 DELETE /api/comments（管理员在 DeleteDanmaku 里
// 按 isAdmin 放行，能删任何人的、弹幕或评论都行）—— 不为后台再开一条删除接口。
async function delSocialRow(row) {
  const s = state.social;
  if (!s) return;
  if (!confirm(`删除这条${row.type === DM_COMMENT ? '评论' : '弹幕'}？不可恢复。`)) return;
  await api('/comments', { method: 'DELETE', body: { path: s.path, id: row.id } });
  await reloadSocial();
  say('已删除一条');
}

// clearSocial 按类型清空。kind 只认 danmaku / comment（后台不给“两类都清”的按钮：
// 一次点错抹掉两种东西，不如分两次点）。
async function clearSocial(kind, label) {
  const s = state.social;
  if (!s) return;
  if (!confirm(`确认清空这部片子的全部${label}？整份重写，不可恢复。`)) return;
  const d = await api('/fs/social/clear', { method: 'POST', body: { path: s.path, kind } });
  await reloadSocial();
  say(`已清空 ${d.cleared} 条${label}`);
}

// socialRow 一条记录：头像 + 昵称 + 时刻/标记 + 内容 + 发送时间 + 删除。
function socialRow(e, tag, reply) {
  const s = state.social;
  return m('.srow' + (reply ? '.reply' : ''), [
    m('img.savatar', { src: avatarURL(e.user_avatar), alt: '',
      // 快照里的 key 已在新版头像表里换掉时回退到 01：拿不到图不能让一行审核没法读
      onerror: ev => { ev.target.src = avatarURL('01'); } }),
    m('span.sname', { title: `用户 #${e.user_id}（昵称为发送当时的快照）` }, e.user_name || `用户#${e.user_id}`),
    tag ? m('span.stag', tag) : null,
    m('span.scontent', e.content),
    m('span.stime', fmtTime(e.created_at)),
    m('button.iconbtn.danger', {
      title: '删除这一条（不可恢复）', 'aria-label': '删除这一条',
      disabled: !s || s.loading,
      onclick: () => guard(() => delSocialRow(e)),
    }, ico(I.trash)),
  ]);
}

// onSocialEsc 必须是**模块级**的固定引用，不能写在 view 里。mithril 复用同一 DOM
// 元素时只换 vnode.attrs、不会重跑 oncreate：闭包写在 view 里的话，第一次渲染挂上
// 的是 f1，reloadSocial 那一次 m.redraw 把 attrs 换成 f2，onremove 解绑的就拿 f2 ——
// f1 永久留在 document 上，以后在任何 Tab 按 Esc 都会白触发一次 closeSocial。
function onSocialEsc(ev) {
  // 判空 state.social：万一还有没解干净的监听器，也别让它去动别的页面的状态。
  if (ev.key === 'Escape' && state.social) closeSocial();
}

// SocialPanel 审核弹层。临时视图，不写进 URL：刷新后回到原位指的是 Tab/目录/筛选，
// 不是重新弹开一个审核窗。
const SocialPanel = {
  view() {
    const s = state.social;
    if (!s) return null;
    const c = s.counts || { danmaku: 0, comments: 0 };
    const busy = s.loading || !!state.busy;
    return m('.social-mask', {
      oncreate: () => document.addEventListener('keydown', onSocialEsc),
      onremove: () => document.removeEventListener('keydown', onSocialEsc),
    }, [
      // 关闭挂在遮罩自己身上，面板是它的**兄弟**而不是子节点：面板里的点击因此
      // 到不了关闭区（mithril 的事件委托到 document，在 handler 里读 ev.currentTarget
      // 只会拿到 document，判不出人点的到底是哪一层）。
      m('.social-bg', { onclick: closeSocial, 'aria-hidden': 'true' }),
      m('.social-panel', { role: 'dialog', 'aria-modal': 'true', 'aria-label': '弹幕与评论管理' }, [
        m('.social-head', [
          m('h3', [ico(I.dm), m('span', '弹幕与评论'), m('code.path', s.name)]),
          m('button.iconbtn.ghost', { title: '关闭（Esc）', 'aria-label': '关闭', onclick: closeSocial }, ico(I.close)),
        ]),
        m('.row.social-sum', [
          m('span.muted', `弹幕 ${c.danmaku} 条`),
          c.danmaku ? m('button.ghost.sm.danger', {
            disabled: busy, title: '只清弹幕，评论保留；整份重写，不可恢复',
            onclick: () => guard(() => clearSocial('danmaku', '弹幕')) }, '清空弹幕') : null,
          m('span.muted', `评论 ${c.comments} 条（含回复）`),
          c.comments ? m('button.ghost.sm.danger', {
            disabled: busy, title: '只清评论（连回复一起），弹幕保留；整份重写，不可恢复',
            onclick: () => guard(() => clearSocial('comment', '评论')) }, '清空评论') : null,
        ]),
        m('p.muted', '弹幕与评论都按内容指纹锁定（' + (s.sha1 ? s.sha1.slice(0, 12) + '…' : '—')
          + '），存在该存储的 .mocca 里：改名、移动都不会丢。'),
        s.err ? m('p.err', s.err) : null,
        busy ? m('p.muted.running', [ico(I.refresh, 'spin'), ' 读取中…']) : [
          m('h4', '弹幕（按出现时刻）'),
          s.danmaku.length
            ? m('.social-list', s.danmaku.map(e => socialRow(e, fmtOffset(e.offset))))
            : m('p.muted', '这部片子还没有弹幕。'),
          m('h4', '评论（最新在上）'),
          s.comments.length
            ? m('.social-list', s.comments.map(t => [
              socialRow(t, '', false),
              (t.replies || []).map(r => socialRow(r, '回复', true)),
            ]))
            : m('p.muted', '这部片子还没有评论。'),
        ],
      ]),
    ]);
  },
};
