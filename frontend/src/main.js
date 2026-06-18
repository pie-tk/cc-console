// 直接使用 Wails runtime Call.ByID，绕过自动绑定的循环依赖问题
// ID 取自自动生成的 frontend/bindings/claude-monitor/service/monitorservice.js
import { Call, Events } from "@wailsio/runtime";

// Binding IDs (FNV hash of fully qualified method names)
const ID_DETECT      = 2236708032;
const ID_GET_THEME   = 1324148558;
const ID_GET_CLOCK   = 3525810521;
const ID_ACT_CLEAR   = 2684523210;
const ID_ACT_REWIND  = 765506358;
const ID_ACT_PROMPT  = 3578199235;
const ID_ACT_SHOW    = 3029513688;
const ID_GET_SETTINGS  = 4111710580;
const ID_SAVE_SETTINGS = 2821561663;
const ID_GET_BRIDGE_STATUS = 3146505974;
const ID_ENABLE_BRIDGE = 2832995149;
const ID_GET_BRIDGE_RULES = 3926351507;
const ID_OPEN_URL         = 2662437060;
const ID_CHECK_UPDATE     = 2276698880;
const ID_DOWNLOAD_UPDATE  = 1405235130;
const ID_GET_CHAT_HISTORY  = 3915737321;
const ID_GET_RECENT_DIRS   = 3059062206;
const ID_LAUNCH_INSTANCE   = 3964521291;
const ID_PICK_DIRECTORY    = 3885139809;
const ID_GET_COMMANDS      = 4004856507; // GetCommandSuggestions(pid)
const ID_SAVE_LIST_PREFS   = 1375619032; // SaveListPrefs(sortField, sortDir)
const ID_ACT_ASK_ANSWER    = 1988866268; // ActAskAnswer(pid, actionsJSON) — AskUserQuestion 按键序列注入

// ---- State ----
let currentPids = [];
let promptTargetPid = null;
let footTimer = null;
let sortField = 'updatedAt';
let sortDir = 'desc';
let chatPanelPid = null;
let chatHistoryHash = 0;
let chatRefreshTimer = null;
let lastChatMessages = [];      // 最近一次渲染的消息，供未变 hash 时重新评估交互按钮
let lastReplySignature = '';    // 上次注入的快速回复签名，避免每秒重复 innerHTML 重写
// 处理中/完成指示器（复刻 Claude Code 风格）：处理中随机切换动词（Channeling…），
// 完成时显示「动词过去式 + 用时」（Crunched for 33s）。状态机：idle → processing → completed → idle。
let procState = 'idle';        // idle | processing | completed
let procStartTime = 0;         // 本轮处理开始时刻(ms)，用于完成时计算用时
let procVerbIdx = 0;           // 当前 spinner 动词下标
let procHasBeenBusy = false;   // 本轮是否经历过 busy（用于区分「处理完成」与「乐观窗口内尚未 busy」）
let procOptimistic = false;    // 发送后乐观窗口（status 变 busy 前的空窗）
let procCompletionText = '';   // 完成态文案
let verbSwitchTimer = null;    // 处理中动词切换定时器
let completionTimer = null;    // 完成态停留定时器
let optimisticTimer = null;    // 乐观窗口兜底定时器

// Claude Code 风格的 spinner 动词（处理中 gerund + 完成时 past）。取自其动词表的常见词。
var SPINNER_VERBS = [
  { ing: 'Channeling',   ed: 'Channelled' },
  { ing: 'Pondering',    ed: 'Pondered' },
  { ing: 'Crunching',    ed: 'Crunched' },
  { ing: 'Working',      ed: 'Worked' },
  { ing: 'Thinking',     ed: 'Thought' },
  { ing: 'Synthesizing', ed: 'Synthesized' },
  { ing: 'Deliberating', ed: 'Deliberated' },
  { ing: 'Ruminating',   ed: 'Ruminated' },
  { ing: 'Musing',       ed: 'Mused' },
  { ing: 'Conjuring',    ed: 'Conjured' },
  { ing: 'Noodling',     ed: 'Noodled' },
  { ing: 'Distilling',   ed: 'Distilled' },
  { ing: 'Cogitating',   ed: 'Cogitated' },
  { ing: 'Brewing',      ed: 'Brewed' },
  { ing: 'Plotting',     ed: 'Plotted' },
  { ing: 'Scheming',     ed: 'Schemed' },
  { ing: 'Dreaming',     ed: 'Dreamed' },
  { ing: 'Processing',   ed: 'Processed' },
  { ing: 'Analyzing',    ed: 'Analyzed' },
  { ing: 'Cooking',      ed: 'Cooked' },
];
// AskUserQuestion 多问追踪:同一 tool_use 内多个问题按序展示。
// 活跃会话 jsonl 滞后(答完一题 jsonl 不更新),没有外部信号告知「现在问到第几题」,
// 只能本地追踪——用户在消息框点选一题后推进到下一题。tool_use ID 变化或交互消失则重置。
let askToolUseId = '';
let askQuestionIndex = 0;
let askQuestionCount = 0;
// AskUserQuestion 已选答案记忆：key = askToolUseId + '#' + askQuestionIndex。
// value 结构：
//   { kind:'single', optionIndex:number, label:string }
//   { kind:'multi', picks:{optionIndex:true}, labels:string[] }
//   { kind:'custom', text:string }
// 用于切回题目时恢复高亮/勾选/自定义横幅，避免默认又亮第一个选项。
let askAnswers = {};
// 多选勾选态：key = askToolUseId + '#' + askQuestionIndex，value = {optionIndex: true}。
// 用 tool_use id + 题号隔离；轮询重渲染时不进签名，勾选靠就地翻转 class 保留（见 toggleAskPick）。
let askMultiSelectPicks = {};
// Type something 自定义输入态：askCustomPending=true 时，下次发送走自定义答案而非普通 prompt。
// askCustomQuestionIndex 记录是为哪一题输入（切题/关面板时重置）。
let askCustomPending = false;
let askCustomQuestionIndex = 0;
let instanceMeta = {}; // pid → {topic, model}
let newInstanceSelected = -1; // 新建实例面板当前选中项索引
let newInstanceItems = [];    // 新建实例面板项：[{type:'dir',path}, {type:'pick'}]
let sendOnEnter = true;       // 消息框发送键：true=回车发送(Shift+回车换行)；false=回车换行(Shift+回车发送)
let autoCheckClaudeSettings = true;
let autoRepairClaudeSettings = true;
let chatDrafts = {};           // 消息框草稿：key = pid|cwd，关闭面板后保留，重新打开时恢复
let bridgeStatusWarnKey = '';   // 避免 settings.json 漂移告警每 10s 重复弹
let bridgeRepairInFlight = false;

// ---- 斜杠命令自动补全状态 ----
let slashList = [];       // 全量命令/技能建议缓存
let slashFiltered = [];   // 当前筛选结果
let slashIdx = 0;         // 选中下标
let slashOpen = false;    // 下拉是否展开
let slashInput = null;    // 当前绑定的 textarea（chat-input 或 prompt-input）

// ---- 目录筛选状态（纯前端，本次启动生效，不持久化）----
// dirFilterHidden 记录被隐藏的 cwd（→ true）；未记录的目录默认显示。
let dirFilterHidden = {};
let dirFilterSig = '';   // 唯一目录签名，变化时才重建下拉 DOM（避免每秒刷新抖动）

// ---- 聊天面板回溯提示 ----
let chatHintTimer = null;

// ---- Boot ----
async function boot() {
  try {
    await applyTheme();
  } catch (e) {
    console.error("Theme init error:", e);
  }
  loadSendMode(); // 加载消息框发送键设置，更新占位符/提示文案
  loadListPrefs(); // 加载持久化的列表排序偏好（字段 + 方向）
  initSlashAutocomplete(); // 绑定消息框斜杠命令自动补全
  initDirFilter(); // 绑定目录筛选下拉的外部点击关闭
  refresh();
  pollBridgeStatus();
  setInterval(refresh, 1000);
  setInterval(pollBridgeStatus, 10000);
}

// 加载发送键设置（回车发送 or Shift+回车发送），并刷新输入框提示文案。
async function loadSendMode() {
  try {
    var s = await Call.ByID(ID_GET_SETTINGS);
    if (s) {
      sendOnEnter = !!s.enterToSend;
      updateSendHints();
    }
  } catch (e) { /* 读取失败保持默认 true */ }
}

// 依据 sendOnEnter 同步两处输入框的提示文案。
function updateSendHints() {
  var chatInput = document.getElementById("chat-input");
  if (chatInput) {
    chatInput.placeholder = sendOnEnter
      ? "输入消息，Enter 发送，Shift+Enter 换行..."
      : "输入消息，Shift+Enter 发送，Enter 换行...";
  }
  var sub = document.getElementById("prompt-subtitle");
  if (sub) {
    sub.textContent = sendOnEnter
      ? "输入文字后点击 发送 ⏎ 或按 Enter。多行会被折叠为空格。"
      : "输入文字后点击 发送 ⏎ 或按 Shift+Enter。多行会被折叠为空格。";
  }
}

// 加载持久化的列表排序偏好（字段 + 方向），覆盖内存默认值并同步排序栏高亮。
async function loadListPrefs() {
  try {
    var s = await Call.ByID(ID_GET_SETTINGS);
    if (s) {
      if (s.sortField) sortField = s.sortField;
      if (s.sortDir) sortDir = s.sortDir;
      updateSortBar();
    }
  } catch (e) { /* 读取失败保持默认 */ }
}

// 持久化当前排序偏好（字段 + 方向）。失败静默，不阻断 UI。
async function saveListPrefs() {
  try {
    await Call.ByID(ID_SAVE_LIST_PREFS, sortField, sortDir);
  } catch (e) { /* 忽略 */ }
}

boot();

// ---- Theme ----
async function applyTheme() {
  const info = await Call.ByID(ID_GET_THEME);
  if (!info) return;
  document.body.classList.toggle("dark", info.isDark);
  if (info.css) {
    const root = document.documentElement;
    for (const [key, val] of Object.entries(info.css)) {
      root.style.setProperty(key, val);
    }
  }
}

// ---- Refresh Loop ----
async function refresh() {
  try {
    const result = await Call.ByID(ID_DETECT);
    const live = (result && result.live) || [];
    const stale = (result && result.stale) || [];
    const stats = (result && result.stats) || {};

    updateStats(stats);
    updateClock();
    renderCards(live, stale);
    updateFooter(live, stats);

    // 聊天面板打开时同步刷新消息 + 底部 context/tokens 信息条
    if (chatPanelPid !== null) {
      refreshChatMessages(chatPanelPid);
      renderChatStats(chatPanelPid);
    }
  } catch (e) {
    console.error("Refresh error:", e);
    const msg = e && e.message ? e.message : String(e);
    document.getElementById("foot-msg").textContent = "检测出错: " + msg;
    document.getElementById("foot-msg").className = "foot-msg fresh";
  }
}

async function pollBridgeStatus() {
  try {
    if (!autoCheckClaudeSettings) return;
    const info = await Call.ByID(ID_GET_BRIDGE_STATUS);
    if (!info) return;
    var drift = [];
    if (!info.hooked) drift.push('statusLine');
    if (info.enabled && !info.hooksInstalled) drift.push('lifecycle hooks');
    var key = drift.join('|');
    if (!key) {
      bridgeStatusWarnKey = '';
      return;
    }
    if (!info.enabled) return;
    if (!autoRepairClaudeSettings) {
      if (bridgeStatusWarnKey !== key) {
        flashFoot('⚠ 检测到 ~/.claude/settings.json 已偏离监控器要求：缺少 ' + drift.join(' + ') + '；当前仅检测，不自动修复');
      }
      bridgeStatusWarnKey = key;
      return;
    }
    if (bridgeRepairInFlight) return;
    bridgeRepairInFlight = true;
    try {
      await Call.ByID(ID_ENABLE_BRIDGE);
      if (bridgeStatusWarnKey !== key) {
        flashFoot('🔧 已自动修复 ~/.claude/settings.json：恢复 ' + drift.join(' + '));
      }
      bridgeStatusWarnKey = key;
    } catch (e) {
      if (bridgeStatusWarnKey !== key) {
        flashFoot('⚠ 自动修复 ~/.claude/settings.json 失败：' + (e && e.message ? e.message : e));
      }
      bridgeStatusWarnKey = key;
    } finally {
      bridgeRepairInFlight = false;
    }
  } catch (e) {
    // 静默：轮询告警不应打断主刷新
  }
}

// ---- Stats ----
function updateStats(stats) {
  const el = document.getElementById("stats");
  const badge = document.getElementById("bridge-badge");
  if (!stats || stats.online === 0) {
    el.textContent = "🌙  当前无实例运行";
    if (badge) badge.textContent = "";
    return;
  }
  const parts = ["在线 " + stats.online, "🔴 " + stats.busy + " 忙碌", "🟢 " + stats.idle + " 空闲"];
  if (stats.totalTokens > 0) parts.push("📦 " + formatTokens(stats.totalTokens) + " tokens");
  if (stats.stale > 0) parts.push("🌓 " + stats.stale + " 残留");
  el.textContent = parts.join("  ·  ");
  // statusline 桥接接入徽标:让用户看到实时数据接入比例
  if (badge) {
    var hooked = stats.online - (stats.offline || 0);
    if ((stats.offline || 0) > 0) {
      badge.textContent = "📡 实时 " + hooked + "/" + stats.online + " · " + stats.offline + " 待接入";
      badge.className = "bridge-badge warn";
    } else {
      badge.textContent = "📡 实时接入 " + hooked + "/" + stats.online;
      badge.className = "bridge-badge";
    }
  }
}

function updateClock() {
  const now = new Date();
  const h = String(now.getHours()).padStart(2, "0");
  const m = String(now.getMinutes()).padStart(2, "0");
  const s = String(now.getSeconds()).padStart(2, "0");
  document.getElementById("clock").textContent = "⏱  " + h + ":" + m + ":" + s;
}

// ---- Cards ----
function renderCards(live, stale) {
  const container = document.getElementById("cards");
  const emptyState = document.getElementById("empty-state");
  const all = sortInstances([...live, ...stale.map(s => Object.assign({}, s, { _stale: true }))]);

  // 构建实例元数据：topic/model/status 供聊天面板标题与交互判定，
  // 另带 context/tokens 字段供聊天面板底部信息条显示（与卡片同形式）。
  // 用未筛选的 all 构建，确保被筛选隐藏的实例在面板已打开时仍可刷新。
  const newMeta = {};
  for (var i = 0; i < all.length; i++) {
    var inst = all[i];
    newMeta[inst.pid] = {
      topic: inst.topic || '',
      model: inst.model || '',
      branch: inst.gitBranch || '',
      cwd: inst.cwd || '',
      status: inst.status || 'unknown',
      hasConversation: !!inst.hasConversation,
      contextTokens: inst.contextTokens || 0,
      contextLimit: inst.contextLimit || 0,
      outputTokens: inst.outputTokens || 0,
      bridgeConnected: !!inst.bridgeConnected,
      costUsd: inst.costUsd || 0,
      durationMs: inst.durationMs || 0,
      totalInputTokens: inst.totalInputTokens || 0,
      totalOutputTokens: inst.totalOutputTokens || 0,
      totalCacheTokens: inst.totalCacheTokens || 0,
    };
  }
  instanceMeta = newMeta;

  // 刷新目录筛选下拉（含按钮显隐、文案、勾选态）
  renderDirFilter(all);

  if (all.length === 0) {
    container.innerHTML = "";
    emptyState.classList.remove("hidden");
    currentPids = [];
    return;
  }

  // 应用目录筛选：dirFilterHidden 中记录的 cwd 被隐藏
  const shown = applyDirFilter(all);

  // 全部被筛选隐藏 → 显示专门空态
  if (shown.length === 0) {
    container.innerHTML = '<div class="filter-empty">'
      + '<div class="empty-icon">🔍</div>'
      + '<div class="empty-title">所有目录都已被筛选隐藏</div>'
      + '<div class="empty-hint">点击右上角「📂 目录」恢复勾选</div>'
      + '</div>';
    emptyState.classList.add("hidden");
    currentPids = [];
    return;
  }
  emptyState.classList.add("hidden");

  const newPids = shown.map(i => i.pid).join(",");
  const oldPids = currentPids.join(",");

  if (newPids !== oldPids) {
    container.innerHTML = shown.map(cardHTML).join("");
    currentPids = shown.map(i => i.pid);
    container.querySelectorAll(".card-history").forEach(function(h) { h.scrollTop = h.scrollHeight; });
  } else {
    shown.forEach((inst, i) => {
      updateCardText(container.children[i], inst);
    });
  }
}

// ---- Sort ----
function sortInstances(arr) {
  if (sortField === 'updatedAt') {
    // 最后活动：先按 busy > idle > stale 分组，再按时间排序
    // 降序（最新在前）：busy 优先 → idle → stale，各组内按时间降序
    // 升序（最旧在前）：idle 优先 → busy → stale，各组内按时间升序
    function rank(inst) {
      if (inst._stale) return 2;
      return inst.status === 'busy' ? 0 : 1;
    }
    return arr.slice().sort(function(a, b) {
      var ra = rank(a), rb = rank(b);
      if (ra !== rb) return sortDir === 'desc' ? ra - rb : rb - ra;
      var va = a.updatedAt || 0, vb = b.updatedAt || 0;
      return sortDir === 'desc' ? vb - va : va - vb;
    });
  }
  return arr.slice().sort(function(a, b) {
    var va = a[sortField] || 0;
    var vb = b[sortField] || 0;
    return sortDir === 'desc' ? vb - va : va - vb;
  });
}

window.handleSort = function(field) {
  if (sortField === field) {
    sortDir = sortDir === 'desc' ? 'asc' : 'desc';
  } else {
    sortField = field;
    sortDir = 'desc';
  }
  updateSortBar();
  saveListPrefs(); // 持久化排序偏好，下次启动沿用
  currentPids = [];
  refresh();
};

function updateSortBar() {
  var btns = document.querySelectorAll('.sort-btn');
  for (var i = 0; i < btns.length; i++) {
    var btn = btns[i];
    var isActive = btn.dataset.sort === sortField;
    btn.classList.toggle('active', isActive);
    var arrow = btn.querySelector('.sort-arrow');
    arrow.textContent = isActive ? (sortDir === 'desc' ? '↓' : '↑') : '↓';
    btn.dataset.dir = isActive ? sortDir : 'desc';
  }
}

// ---- 目录筛选下拉 ----

// collectUniqueDirs 返回去重后的 cwd 列表（按首次出现顺序）。
function collectUniqueDirs(all) {
  var seen = {};
  var dirs = [];
  for (var i = 0; i < all.length; i++) {
    var cwd = all[i].cwd || '';
    if (!cwd || seen[cwd]) continue;
    seen[cwd] = true;
    dirs.push(cwd);
  }
  return dirs;
}

// renderDirFilter 刷新筛选按钮显隐/文案与下拉列表。
// 列表 DOM 仅在唯一目录集合变化（签名不同）时重建，避免每秒刷新抖动并保留勾选交互。
function renderDirFilter(all) {
  var dirs = collectUniqueDirs(all);
  var btn = document.getElementById('dir-filter-btn');
  var wrap = document.getElementById('dir-filter-wrap');
  if (!btn || !wrap) return;
  // 仅 ≤1 个唯一目录时隐藏筛选（无意义）
  wrap.style.display = dirs.length > 1 ? '' : 'none';
  if (dirs.length <= 1) return;

  // 按钮文案：有隐藏项时显示「· 隐 N」
  var hiddenInList = 0;
  for (var i = 0; i < dirs.length; i++) {
    if (dirFilterHidden[dirs[i]]) hiddenInList++;
  }
  btn.textContent = hiddenInList > 0 ? '📂 目录 · 隐 ' + hiddenInList : '📂 目录';

  // 签名比对决定是否重建列表 DOM
  var sig = dirs.join('\n');
  if (sig === dirFilterSig) {
    // 集合未变，仍需同步「全选」勾选态（隐藏项可能因实例消失而变化）
    syncSelectAll(dirs);
    return;
  }
  dirFilterSig = sig;

  // 重建复选框列表
  var listEl = document.getElementById('dir-filter-list');
  if (!listEl) return;
  var html = '';
  for (var j = 0; j < dirs.length; j++) {
    var cwd = dirs[j];
    var checked = !dirFilterHidden[cwd];
    // value 用下标索引，cwd 经 data-cwd 传递；目录中含特殊字符故用属性而非内联参数
    html += '<label class="dir-filter-item" title="' + escAttr(cwd) + '">'
      + '<input type="checkbox" data-cwd="' + escAttr(cwd) + '"' + (checked ? ' checked' : '')
      + ' onchange="onDirFilterItemChange(this)">'
      + '<span class="dir-filter-item-label">' + escHtml(cwdTitle(cwd)) + '</span>'
      + '</label>';
  }
  listEl.innerHTML = html;
  syncSelectAll(dirs);
}

// syncSelectAll 按当前目录集合同步「全选」复选框的勾选态。
function syncSelectAll(dirs) {
  var allCb = document.getElementById('dir-filter-selectall');
  if (!allCb) return;
  var allShown = true;
  for (var i = 0; i < dirs.length; i++) {
    if (dirFilterHidden[dirs[i]]) { allShown = false; break; }
  }
  allCb.checked = allShown;
}

// applyDirFilter 过滤掉被隐藏目录的实例。
function applyDirFilter(all) {
  var hasHidden = false;
  for (var k in dirFilterHidden) { if (dirFilterHidden[k]) { hasHidden = true; break; } }
  if (!hasHidden) return all;
  return all.filter(function(i) { return !dirFilterHidden[i.cwd || '']; });
}

window.toggleDirFilter = function(e) {
  if (e) e.stopPropagation();
  var dd = document.getElementById('dir-filter-dropdown');
  if (dd) dd.classList.toggle('hidden');
};

// initDirFilter 绑定「点击下拉外部关闭」。列表内部点击不冒泡，避免误关。
function initDirFilter() {
  document.addEventListener('click', function(e) {
    var dd = document.getElementById('dir-filter-dropdown');
    if (!dd || dd.classList.contains('hidden')) return;
    var wrap = document.getElementById('dir-filter-wrap');
    if (wrap && !wrap.contains(e.target)) {
      dd.classList.add('hidden');
    }
  });
}

// onDirFilterItemChange 单个目录勾选/取消勾选 → 更新隐藏集合并刷新卡片。
window.onDirFilterItemChange = function(cb) {
  var cwd = cb.getAttribute('data-cwd') || '';
  if (cb.checked) delete dirFilterHidden[cwd];
  else dirFilterHidden[cwd] = true;
  // 仅刷新按钮文案与「全选」态，无需重建列表（避免勾选闪烁）
  syncSelectAllFromDOM();
  refreshFilterBtnText();
  currentPids = [];
  refresh();
};

// onDirFilterAllChange 全选/全不选：对当前下拉中所有目录统一显隐。
window.onDirFilterAllChange = function() {
  var allCb = document.getElementById('dir-filter-selectall');
  if (!allCb) return;
  var cbs = document.querySelectorAll('#dir-filter-list input[type=checkbox]');
  for (var i = 0; i < cbs.length; i++) {
    cbs[i].checked = allCb.checked;
    var cwd = cbs[i].getAttribute('data-cwd') || '';
    if (allCb.checked) delete dirFilterHidden[cwd];
    else dirFilterHidden[cwd] = true;
  }
  refreshFilterBtnText();
  currentPids = [];
  refresh();
};

// syncSelectAllFromDOM 从当前 DOM 复选框反推「全选」态。
function syncSelectAllFromDOM() {
  var cbs = document.querySelectorAll('#dir-filter-list input[type=checkbox]');
  var allShown = true;
  for (var i = 0; i < cbs.length; i++) {
    if (!cbs[i].checked) { allShown = false; break; }
  }
  var allCb = document.getElementById('dir-filter-selectall');
  if (allCb) allCb.checked = allShown;
}

// refreshFilterBtnText 按当前 DOM 复选框统计隐藏数并更新按钮文案。
function refreshFilterBtnText() {
  var btn = document.getElementById('dir-filter-btn');
  if (!btn) return;
  var cbs = document.querySelectorAll('#dir-filter-list input[type=checkbox]');
  var hidden = 0;
  for (var i = 0; i < cbs.length; i++) if (!cbs[i].checked) hidden++;
  btn.textContent = hidden > 0 ? '📂 目录 · 隐 ' + hidden : '📂 目录';
}

function cwdTitle(cwd) {
  if (!cwd) return "（未知目录）";
  var parts = cwd.replace(/\\/g, '/').replace(/\/$/, '').split('/');
  if (parts.length <= 2) return cwd;
  return '\\' + parts.slice(-2).join('\\');
}

function cardHTML(inst) {
  const stale = inst._stale ? " stale" : "";
  const emoji = statusEmoji(inst.status);
  const statusClass = inst.status || "unknown";
  const label = statusLabel(inst.status);
  const model = modelDisplay(inst);
  const cwd = inst.cwd || "";
  const title = cwdTitle(cwd);
  const topic = topicDisplay(inst);
  const ctxBar = contextBar(inst);
  const ctxDetail = contextDetail(inst);
  const output = outputDisplay(inst);
  const totalTokens = totalTokensDisplay(inst);

  return '<div class="card' + stale + '" data-pid="' + inst.pid + '">'
    + '<div class="card-inner">'
    + '<div class="card-row">'
    + '<span class="card-emoji">' + emoji + '</span>'
    + '<span class="card-title" data-field="title" title="' + escAttr(topic) + '">' + escHtml(topic) + '</span>'
    + '<span class="card-branch" data-field="branch">' + escHtml(branchDisplay(inst)) + '</span>'
    + '<span class="card-status ' + statusClass + '" data-field="status">' + label + '</span>'
    + '<span class="card-bridge-tag' + (inst.bridgeConnected ? '' : ' show') + '" data-field="bridge" title="statusline 桥接尚未生效，实时数据待接入（新会话刷新后自动接入）">⏳ 未接入</span>'
    + '<span class="card-pid-subtle">PID ' + inst.pid + '</span>'
    + '<span class="card-model" data-field="model">' + model + '</span>'
    + '<span class="card-duration" data-field="duration">' + humanDuration(inst.startedAt) + '</span>'
    + '</div>'
    + '<div class="card-row card-topic-row">'
    + '<span class="card-topic" data-field="topic" title="' + escAttr(cwd) + '">📁 ' + escHtml(title) + '</span>'
    + '</div>'
    + historyHTML(inst)
    + '<div class="card-row card-context">'
    + '<span class="context-bar ' + contextBarClass(inst) + '" data-field="ctxBar">' + ctxBar + '</span>'
    + '<span class="context-pct" data-field="ctxPct">' + contextPct(inst) + '</span>'
    + '<span class="context-detail" data-field="ctxDetail">' + ctxDetail + '</span>'
    + '<span class="card-output" data-field="output">↑ ' + output + '</span>'
    + '</div>'
    + (totalTokens ? '<div class="card-row card-tokens"><span class="card-total-tokens" data-field="totalTokens">📦 ' + totalTokens + '</span></div>' : '')
    + '</div>'
    + '<div class="card-actions">'
    + '<button class="action-btn" onclick="handleClear(' + inst.pid + ')">清空</button>'
    + '<button class="action-btn" onclick="openChatPanel(' + inst.pid + ')">对话</button>'
    + '<button class="action-btn" onclick="handleRewind(' + inst.pid + ')">回溯</button>'
    + '<button class="action-btn" onclick="handleShowWin(' + inst.pid + ')">窗口</button>'
    + '</div>'
    + '</div>';
}

function updateCardText(el, inst) {
  if (!el) return;
  const set = (sel, val) => { const e = el.querySelector(sel); if (e) e.textContent = val; };
  set("[data-field=title]", topicDisplay(inst));
  set("[data-field=branch]", branchDisplay(inst));
  set("[data-field=status]", statusLabel(inst.status));
  set("[data-field=model]", modelDisplay(inst));
  set("[data-field=duration]", humanDuration(inst.startedAt));
  set("[data-field=topic]", "📁 " + cwdTitle(inst.cwd || ""));
  set("[data-field=ctxBar]", contextBar(inst));
  set("[data-field=ctxPct]", contextPct(inst));
  set("[data-field=ctxDetail]", contextDetail(inst));
  set("[data-field=output]", "↑ " + outputDisplay(inst));
  // 对话历史区域：比较 historyHash 而非 turns——assistant 回复追加到已有轮次时
  // turns 不变，但 historyHash（= Σ(len(Q)*31 + len(R)*17)）一定会变。
  var histEl = el.querySelector(".card-history");
  var newHash = inst.historyHash || 0;
  var oldHash = histEl ? parseInt(histEl.getAttribute("data-hist-hash") || "0") : -1;
  if (newHash !== oldHash) {
    if (histEl) {
      histEl.parentNode.removeChild(histEl);
    }
    var histHTML = historyHTML(inst);
    if (histHTML) {
      var tempDiv = document.createElement("div");
      tempDiv.innerHTML = histHTML;
      var newHistEl = tempDiv.firstChild;
      var topicRow = el.querySelector(".card-topic-row");
      if (topicRow && topicRow.nextSibling) {
        topicRow.parentNode.insertBefore(newHistEl, topicRow.nextSibling);
      }
      newHistEl.scrollTop = newHistEl.scrollHeight;
    }
  }

  // 累计 token 行：动态插入/更新/移除
  var totalTokens = totalTokensDisplay(inst);
  var tokensEl = el.querySelector("[data-field=totalTokens]");
  if (totalTokens) {
    if (!tokensEl) {
      // 插入新行到 card-context 之后
      var row = document.createElement("div");
      row.className = "card-row card-tokens";
      row.innerHTML = '<span class="card-total-tokens" data-field="totalTokens">📦 ' + totalTokens + '</span>';
      var ctxRow = el.querySelector(".card-context");
      if (ctxRow && ctxRow.nextSibling) {
        ctxRow.parentNode.insertBefore(row, ctxRow.nextSibling);
      } else if (ctxRow) {
        ctxRow.parentNode.appendChild(row);
      }
    } else {
      tokensEl.textContent = "📦 " + totalTokens;
    }
  } else if (tokensEl) {
    var row = tokensEl.parentNode;
    row.parentNode.removeChild(row);
  }

  var titleEl = el.querySelector("[data-field=title]");
  if (titleEl) titleEl.title = inst.topic || "";
  var topicEl = el.querySelector("[data-field=topic]");
  if (topicEl) topicEl.title = inst.cwd || "";
  var statusEl = el.querySelector(".card-status");
  if (statusEl) statusEl.className = "card-status " + (inst.status || "unknown");
  var barEl = el.querySelector(".context-bar");
  if (barEl) barEl.className = "context-bar " + contextBarClass(inst);
  set(".card-emoji", statusEmoji(inst.status));
  var bridgeEl = el.querySelector("[data-field=bridge]");
  if (bridgeEl) bridgeEl.classList.toggle("show", !inst.bridgeConnected);
}

// ---- Escape helpers ----
function escHtml(s) { return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }
function escAttr(s) { return s.replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/</g,'&lt;').replace(/>/g,'&gt;'); }

// highlightDiff 检测代码是否为 diff 格式，若是则对 +/-/@@ 行着色并返回 HTML；
// 若不是 diff 则返回空字符串（调用方回退到普通代码块渲染）。
// code 参数已通过 escHtml 转义。
function highlightDiff(code) {
  var lines = code.split('\n');
  var headers = 0, adds = 0, dels = 0;
  for (var i = 0; i < lines.length; i++) {
    var ch = lines[i].charAt(0);
    var ch2 = lines[i].charAt(1);
    if (ch === '@' && ch2 === '@') headers++;
    if (ch === '+' && ch2 !== '+') adds++;
    if (ch === '-' && ch2 !== '-') dels++;
  }
  // 至少 1 个 @@ 头部，或 3 行以上 +/- 才视为 diff
  if (headers === 0 && adds + dels < 3) return '';

  var result = '';
  for (var j = 0; j < lines.length; j++) {
    var line = lines[j];
    if (/^@@\s+-\d+/.test(line)) {
      result += '<span class="diff-header">' + line + '</span>\n';
    } else if (/^---\s/.test(line) || /^\+\+\+\s/.test(line)) {
      result += '<span class="diff-meta">' + line + '</span>\n';
    } else if (/^\+/.test(line)) {
      result += '<span class="diff-add">' + line + '</span>\n';
    } else if (/^-/.test(line)) {
      result += '<span class="diff-del">' + line + '</span>\n';
    } else {
      result += line + '\n';
    }
  }
  return result.replace(/\n$/, '');
}

// computeLineDiff 对 old/new 两段文本做逐行 LCS diff，返回 [{type:'same'|'del'|'add', text, ln}]
// startLine 为修改区域起始行号（1-based，0 表示未知——此时不附带行号）。
function computeLineDiff(oldStr, newStr, startLine) {
  var oldLines = oldStr.split('\n');
  var newLines = newStr.split('\n');
  var m = oldLines.length, n = newLines.length;

  // LCS DP 表：dp[i][j] = oldLines[0..i-1] 与 newLines[0..j-1] 的最长公共子序列长度
  var dp = new Array(m + 1);
  for (var i = 0; i <= m; i++) {
    dp[i] = new Array(n + 1);
    for (var j = 0; j <= n; j++) {
      if (i === 0 || j === 0) {
        dp[i][j] = 0;
      } else if (oldLines[i - 1] === newLines[j - 1]) {
        dp[i][j] = dp[i - 1][j - 1] + 1;
      } else {
        dp[i][j] = Math.max(dp[i - 1][j], dp[i][j - 1]);
      }
    }
  }

  // 回溯构造 diff 序列（正序）
  var result = [];
  var i = m, j = n;
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && oldLines[i - 1] === newLines[j - 1]) {
      result.push({ type: 'same', text: oldLines[i - 1] });
      i--; j--;
    } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
      result.push({ type: 'add', text: newLines[j - 1] });
      j--;
    } else {
      result.push({ type: 'del', text: oldLines[i - 1] });
      i--;
    }
  }
  result.reverse();

  // 正向计算每行对应的文件行号：del 用 oldLine，add/same 用 newLine（均从 startLine 起）
  if (startLine > 0) {
    var oldLine = startLine, newLine = startLine;
    for (var k = 0; k < result.length; k++) {
      var r = result[k];
      if (r.type === 'del') { r.ln = oldLine; oldLine++; }
      else if (r.type === 'add') { r.ln = newLine; newLine++; }
      else { r.ln = newLine; oldLine++; newLine++; }
    }
  }
  return result;
}

// renderToolCallBody 渲染工具调用的输入体。对 Edit/Write 工具提取 old_string/new_string
// 并渲染为增删行颜色标记（红删绿增）；其他工具回退为 JSON 原文。
// startLine 为 Edit 修改区域起始行号（来自后端定位），>0 时 diff 左侧显示行号列。
function renderToolCallBody(tool, rawContent, startLine) {
  if (tool !== 'Edit' && tool !== 'Write') {
    return '<div class="chat-msg-tool-input">' + escHtml(rawContent) + '</div>';
  }

  // 尝试解析 JSON 输入
  var input;
  try { input = JSON.parse(rawContent); } catch (e) { input = null; }
  if (!input) {
    return '<div class="chat-msg-tool-input">' + escHtml(rawContent) + '</div>';
  }

  // 文件路径头部
  var html = '';
  if (input.file_path) {
    html += '<div class="tool-edit-file">📄 ' + escHtml(input.file_path) + '</div>';
  }

  // Write 工具：新建/覆盖整个文件，不直接平铺全部内容，给出提示并可折叠查看
  if (tool === 'Write' && input.content) {
    var lineCount = input.content.split('\n').length;
    var byteLen = input.content.length;
    html += '<div class="tool-edit-hint">📝 新建文件 · ' + lineCount + ' 行 · ' + byteLen + ' 字符</div>';
    var wc = input.content;
    if (wc.length > 8000) wc = wc.slice(0, 8000) + '\n...（内容过长，已截断）';
    html += '<details class="tool-edit-details"><summary>查看文件内容</summary>'
      + '<div class="tool-edit-diff"><pre><code>' + escHtml(wc) + '</code></pre></div></details>';
    return html;
  }

  // Edit 工具：逐行 LCS diff，只标真正变化的行
  var oldStr = input.old_string || '';
  var newStr = input.new_string || '';
  if (!oldStr && !newStr) {
    html += '<div class="chat-msg-tool-input">' + escHtml(rawContent) + '</div>';
    return html;
  }

  var changes = computeLineDiff(oldStr, newStr, startLine || 0);
  var diff = '';
  for (var k = 0; k < changes.length; k++) {
    var ch = changes[k];
    var cls = ch.type === 'del' ? 'diff-del' : (ch.type === 'add' ? 'diff-add' : 'diff-same');
    var sign = ch.type === 'del' ? '-' : (ch.type === 'add' ? '+' : ' ');
    var lnHTML = ch.ln ? '<span class="diff-ln">' + ch.ln + '</span>' : '';
    diff += '<div class="' + cls + '">' + lnHTML
      + '<span class="diff-ct">' + sign + ' ' + escHtml(ch.text || ' ') + '</span></div>';
  }
  html += '<div class="tool-edit-diff' + (startLine ? ' has-linenr' : '') + '">' + diff + '</div>';
  return html;
}

// ---- Markdown 格式化 ----
// buildTable 把 GFM 表格的若干原始行（已转义）渲染成 <table>。
// rows[0]=表头行，rows[1]=分隔行（决定对齐），其余=数据行。
function buildTable(rows) {
  function parseCells(rowLine) {
    var inner = rowLine.replace(/^\|/, '').replace(/\|\s*$/, '');
    return inner.split('|').map(function(c) { return c.trim(); });
  }
  function alignOf(s) {
    if (/^:-+$/.test(s)) return 'left';
    if (/^-+:$/.test(s)) return 'right';
    if (/^:-+:$/.test(s)) return 'center';
    return '';
  }
  var header = parseCells(rows[0]);
  var aligns = parseCells(rows[1]).map(alignOf);
  function cellTag(tag, content, idx) {
    var a = aligns[idx];
    var style = a ? ' style="text-align:' + a + '"' : '';
    return '<' + tag + style + '>' + (content == null ? '' : content) + '</' + tag + '>';
  }
  var t = '<table class="md-table"><thead><tr>';
  header.forEach(function(c, idx) { t += cellTag('th', c, idx); });
  t += '</tr></thead><tbody>';
  var ncols = header.length;
  for (var r = 2; r < rows.length; r++) {
    var cells = parseCells(rows[r]);
    t += '<tr>';
    for (var c = 0; c < ncols; c++) { t += cellTag('td', cells[c], c); }
    t += '</tr>';
  }
  t += '</tbody></table>';
  return t;
}

// renderMarkdown 把文本中的常见 markdown 语法转为 HTML。
// 调用方负责保证输入不含未闭合的 Claude Code 注解标签（即注解标签已先由
// formatRichContent 处理完毕）。函数内部先做 HTML 转义，再转换 markdown。
function renderMarkdown(text) {
  if (!text) return '';
  var html = escHtml(text);

  // 保护围栏代码块：```lang\n...\n```，避免内部 **、* 等被误转
  var fenced = [];
  html = html.replace(/```(\w*)\n([\s\S]*?)```/g, function(_, lang, code) {
    var idx = fenced.length;
    var content = code.replace(/\n$/, '');
    var diffHTML;
    // diff/patch 语言标记，或自动检测内容是否匹配 diff 格式
    if (lang === 'diff' || lang === 'patch') {
      diffHTML = highlightDiff(content);
    } else if (!lang) {
      diffHTML = highlightDiff(content); // 无语言标记时自动检测
    }
    if (diffHTML) {
      fenced.push('<pre class="diff-block"><code>' + diffHTML + '</code></pre>');
    } else {
      fenced.push('<pre><code' + (lang ? ' class="language-' + lang + '"' : '') + '>' + content + '</code></pre>');
    }
    return '\x00F' + idx + '\x00';
  });

  // 保护行内代码：`...`
  var inlined = [];
  html = html.replace(/`([^`\n]+)`/g, function(_, code) {
    var idx = inlined.length;
    inlined.push('<code>' + code + '</code>');
    return '\x00I' + idx + '\x00';
  });

  // 粗体 + 斜体（粗斜体优先，避免 *** 被错拆）
  html = html.replace(/\*\*\*(.+?)\*\*\*/g, '<strong><em>$1</em></strong>');
  html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
  html = html.replace(/(?<!\w)\*(.+?)\*(?!\w)/g, '<em>$1</em>');

  // 链接 [text](url)
  html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');

  // 逐行处理块级元素，构建为单个字符串避免 out.join 引入多余 \n
  var lines = html.split('\n');
  var out = [];
  for (var i = 0; i < lines.length; i++) {
    var line = lines[i];
    var m;

    if ((m = /^### (.+)$/.exec(line)))  { out.push('<h3>' + m[1] + '</h3>'); continue; }
    if ((m = /^## (.+)$/.exec(line)))   { out.push('<h2>' + m[1] + '</h2>'); continue; }
    if ((m = /^# (.+)$/.exec(line)))    { out.push('<h1>' + m[1] + '</h1>'); continue; }

    if (/^(---|\*\*\*|___)$/.test(line)) { out.push('<hr>'); continue; }

    // 表格：首行 |...| + 第二行分隔行 |---|---|
    if (/^\|.+\|\s*$/.test(line) && i + 1 < lines.length &&
        /^\|[\s:?|-]+$/.test(lines[i + 1]) && /-/.test(lines[i + 1])) {
      var trows = [line, lines[i + 1]];
      var ti = i + 2;
      while (ti < lines.length && /^\|.+\|\s*$/.test(lines[ti])) {
        trows.push(lines[ti]);
        ti++;
      }
      out.push(buildTable(trows));
      i = ti - 1;
      continue;
    }

    // 无序列表：- / * 开头，连续行拼成单个 <ul> 字符串
    if ((m = /^[\-*] (.+)$/.exec(line))) {
      var ul = '<ul><li>' + m[1] + '</li>';
      i++;
      while (i < lines.length && (m = /^[\-*] (.+)$/.exec(lines[i]))) {
        ul += '<li>' + m[1] + '</li>';
        i++;
      }
      ul += '</ul>';
      out.push(ul);
      i--;
      continue;
    }

    // 有序列表：1. 开头，连续行拼成单个 <ol> 字符串
    if ((m = /^\d+\. (.+)$/.exec(line))) {
      var ol = '<ol><li>' + m[1] + '</li>';
      i++;
      while (i < lines.length && (m = /^\d+\. (.+)$/.exec(lines[i]))) {
        ol += '<li>' + m[1] + '</li>';
        i++;
      }
      ol += '</ol>';
      out.push(ol);
      i--;
      continue;
    }

    // 引用块：>  开头（已转义为 &gt;），连续行拼成单个 <blockquote> 字符串
    if ((m = /^&gt; ?(.+)$/.exec(line))) {
      var bq = '<blockquote>' + m[1];
      i++;
      while (i < lines.length && (m = /^&gt; ?(.+)$/.exec(lines[i]))) {
        bq += '<br>' + m[1];
        i++;
      }
      bq += '</blockquote>';
      out.push(bq);
      i--;
      continue;
    }

    out.push(line);
  }
  html = out.join('\n');

  // 清理块级标签前后的多余换行——chat-msg 有 white-space:pre-wrap，
  // 这些 \n 会被渲染为额外的空行，而块级元素本身就换行
  html = html.replace(/(^|\n)(<(?:h[1-6]|ul|ol|blockquote|hr|div)\b[^>]*>)/g, '$2');
  html = html.replace(/(<\/(?:h[1-6]|ul|ol|blockquote|div)>)(\n|$)/g, '$1');

  // 还原代码块
  html = html.replace(/\x00F(\d+)\x00/g, function(_, idx) { return fenced[+idx]; });
  html = html.replace(/\x00I(\d+)\x00/g, function(_, idx) { return inlined[+idx]; });

  return html;
}

// ---- Claude Code 注解标签格式化 ----
// Claude Code 在消息文本里嵌入伪 XML 注解（斜杠命令、系统提示、任务通知、摘录等），
// 直接显示尖括号原文很突兀。formatRichContent 把已知标签渲染成带样式的结构化块，
// 未知标签（含代码里的 <…>）按普通文本转义，不破坏源码。

// 去除终端 ANSI 颜色转义（命令输出里常见，否则显示 [1m 乱码）。
function stripAnsi(s) { return s.replace(/\x1b\[[0-9;]*m/g, ''); }

// 已知容器标签 → 渲染类型。
var CC_BLOCK_TAGS = {
  'command-name':'cmd', 'command-message':'cmd', 'command-args':'cmd', 'command-body':'cmd',
  'local-command-stdout':'cmdout', 'local-command-stderr':'cmderr', 'local-command-caveat':'cmdcaveat',
  'system-reminder':'system', 'env':'env', 'user-memory-content':'memory',
  'task-notification':'task', 'task-reminder':'task',
  'persisted-output':'persisted',
  'excerpt':'quote',
  'bash-input':'bashin','bash-stdout':'bashout','bash-stderr':'basherr',
  'thinking':'think','antThinking':'think',
};
// 全部已知标签名（供残片兜底正则用）。
var CC_TAG_ALT = 'command-name|command-message|command-args|command-body|local-command-stdout|local-command-stderr|local-command-caveat|system-reminder|env|user-memory-content|task-notification|task-reminder|persisted-output|excerpt|bash-input|bash-stdout|bash-stderr|thinking|antThinking';

// grabTag 从 content 中提取某标签的纯文本内文。
function grabTag(content, tag) {
  var m = new RegExp('<' + tag + '\\b[^>]*>([\\s\\S]*?)</' + tag + '>', 'i').exec(content);
  return m ? m[1].replace(/^\n+|\n+$/g, '') : '';
}

// renderCommandCard 渲染斜杠命令卡片（已合并 name + args）。
function renderCommandCard(o) {
  var line = (o.n || '') + (o.a ? (' ' + o.a) : '');
  return '<div class="cc-cmd"><span class="cc-cmd-icon">⌘</span><code>' + escHtml(line) + '</code></div>';
}

// renderTask 渲染任务通知：解析内嵌 status/summary。
function renderTask(content) {
  var status = grabTag(content, 'status');
  var summary = grabTag(content, 'summary');
  var cls = (status === 'success' || status === 'completed') ? 'ok'
    : (status === 'failed' || status === 'error') ? 'fail' : '';
  var icon = cls === 'ok' ? '✅' : cls === 'fail' ? '❌' : '🔔';
  var h = '<div class="cc-task' + (cls ? (' cc-task-' + cls) : '') + '">'
    + icon + ' <span class="cc-task-label">后台任务' + (status ? (' · ' + escHtml(status)) : '') + '</span>';
  if (summary) h += '<div class="cc-task-summary">' + escHtml(summary) + '</div>';
  h += '</div>';
  return h;
}

// renderBlock 把单个已知标签的内文渲染成对应结构化块。
function renderBlock(name, content) {
  var body = content.replace(/^\n+|\n+$/g, '');
  switch (CC_BLOCK_TAGS[name]) {
  case 'cmd': return renderCommandCard({ n: body });
  case 'cmdout': return '<pre class="cc-cmdout">' + escHtml(body) + '</pre>';
  case 'cmderr': return '<pre class="cc-cmderr">' + escHtml(body) + '</pre>';
  case 'cmdcaveat': return '<div class="cc-caveat">⚠ ' + escHtml(body) + '</div>';
  case 'system': return '<div class="cc-block cc-system"><span class="cc-label">系统</span>' + renderMarkdown(body) + '</div>';
  case 'env': return '<div class="cc-block cc-system"><span class="cc-label">环境</span>' + escHtml(body) + '</div>';
  case 'memory': return '<div class="cc-block cc-system"><span class="cc-label">记忆</span>' + formatRichContent(content) + '</div>';
  case 'task': return renderTask(content);
  case 'persisted': return '<div class="cc-persisted">📎 ' + escHtml(body) + '</div>';
  case 'quote': return '<blockquote class="cc-quote">' + formatRichContent(content) + '</blockquote>';
  case 'bashin': return '<pre class="cc-bashin">$ ' + escHtml(body) + '</pre>';
  case 'bashout': return '<pre class="cc-cmdout">' + escHtml(body) + '</pre>';
  case 'basherr': return '<pre class="cc-cmderr">' + escHtml(body) + '</pre>';
  case 'think': return '<details class="cc-think"><summary>💭 思考过程</summary><div>' + formatRichContent(content) + '</div></details>';
  default: return escHtml(content); // 未知标签：剥外壳留内文
  }
}

// formatRichContent 把含 Claude Code 注解标签的文本渲染成 HTML（未知/代码标签按文本转义）。
function formatRichContent(text) {
  if (!text) return '';
  text = stripAnsi(text);
  // 先合并斜杠命令三件套 name(+message)(+args) 为一个命令卡片标记（\u0000CMD{json}\u0000），
  // 这样三件套收成一张卡片，而不是三个零散块。
  text = text.replace(
    /<command-name>([\s\S]*?)<\/command-name>\s*(?:<command-message>[\s\S]*?<\/command-message>\s*)?(?:<command-args>([\s\S]*?)<\/command-args>\s*)?/g,
    function (_, n, a) { return '\u0000CMD' + JSON.stringify({ n: n.trim(), a: (a || '').trim() }) + '\u0000'; }
  );
  var html = '';
  var i = 0;
  var tagRe = /^<([a-zA-Z][\w-]*)\b[^>]*?\/?>/;
  while (i < text.length) {
    // 命令卡片标记
    if (text.charAt(i) === '\u0000' && text.substr(i, 4) === '\u0000CMD') {
      var end = text.indexOf('\u0000', i + 4);
      if (end < 0) { html += renderMarkdown(text.slice(i)); break; }
      try { html += renderCommandCard(JSON.parse(text.slice(i + 4, end))); } catch (e) { /* 跳过 */ }
      i = end + 1;
      continue;
    }
    // 从 i 起向后扫描第一个「已知注解标签」的 <；途中的非已知 <（代码/正文里的 < 等）
    // 一律视为普通文本，不在此切分——它们连同前后文本整体交给 renderMarkdown，< 由
    // escHtml 转义为 &lt;。切分会破坏表格等跨多行 markdown 结构（行被截断后不再以 |
    // 结尾，数据行匹配失败，导致只渲染表头）。
    // 关键：只推进临时扫描指针 scan，不移动 i——原先遇到非已知 < 直接 i=lt+1 会把 <
    // 及其之前的整段文本永久丢弃（消息含 `<pid>` 这类时首段直接消失），这是回归根因。
    var tagAt = -1, mOpen = null, scan = i;
    while (true) {
      var lt = text.indexOf('<', scan);
      if (lt < 0) break;
      var mm = tagRe.exec(text.slice(lt));
      if (mm && CC_BLOCK_TAGS[mm[1]]) { tagAt = lt; mOpen = mm; break; }
      scan = lt + 1; // 非已知标签：跳过继续找，不丢文本
    }
    if (tagAt < 0) { html += renderMarkdown(text.slice(i)); break; } // 无更多已知标签
    html += renderMarkdown(text.slice(i, tagAt)); // 标签前文本（含途中所有非已知 <）
    var name = mOpen[1];
    var afterOpen = tagAt + mOpen[0].length;
    var close = '</' + name + '>';
    var ci = text.indexOf(close, afterOpen);
    var content, eend;
    if (ci >= 0) { content = text.slice(afterOpen, ci); eend = ci + close.length; }
    else { content = text.slice(afterOpen); eend = text.length; } // 无闭合：取到结尾
    html += renderBlock(name, content);
    i = eend;
  }
  return html;
}

// isAnnotationOnly 判断消息是否"纯注解"（命令/系统/任务事件，无人类真实文本）。
// 此类消息渲染为居中事件行而非左右气泡。
function isAnnotationOnly(text) {
  if (!text) return false;
  if (!new RegExp('<(?:' + CC_TAG_ALT + ')\\b').test(text)) return false;
  var t = stripAnsi(text);
  // 移除命令三件套（name..args 或 name..name）
  t = t.replace(/<command-name>[\s\S]*?(?:<\/command-args>|<\/command-name>)/g, '');
  // 移除各 remove 标签（含内容），用反向引用配对开闭
  t = t.replace(/<(system-reminder|env|user-memory-content|task-notification|task-reminder|persisted-output|local-command-caveat|local-command-stdout|local-command-stderr|command-message|command-args|command-body|thinking|antThinking)\b[^>]*>[\s\S]*?<\/\1>/g, '');
  // 剥 excerpt/bash 外壳
  t = t.replace(/<\/?(excerpt|bash-input|bash-stdout|bash-stderr)\b[^>]*>/g, '');
  // 残片兜底
  t = t.replace(new RegExp('<\\/?(?:' + CC_TAG_ALT + ')\\b[^>]*>', 'g'), '');
  return t.replace(/\s/g, '').length === 0;
}

// ---- Display helpers ----
function statusEmoji(s) {
  if (s === "busy") return "🔴";
  if (s === "idle") return "🟢";
  return "⚪";
}

function statusLabel(s) {
  if (s === "busy") return "忙碌";
  if (s === "idle") return "空闲";
  return "未知";
}

function modelDisplay(inst) {
  if (!inst.hasConversation) return "（新）";
  if (!inst.model) return "—";
  return inst.model;
}

function topicDisplay(inst) {
  if (!inst.hasConversation) return "（新会话·无消息）";
  if (!inst.topic) return "（暂无主题）";
  return inst.topic;
}

// 分支展示：有 git 分支返回 "🌿 <branch>"，无仓库/无分支返回空串（前端 :empty 自动隐藏）。
function branchDisplay(inst) {
  if (!inst.gitBranch) return "";
  return "🌿 " + inst.gitBranch;
}

function outputDisplay(inst) {
  if (!inst.hasConversation) return "（新）";
  return formatTokens(inst.outputTokens);
}

function totalTokensDisplay(inst) {
  // 桥接实时实例:显示费用/时长(活跃会话 jsonl 未落盘,用 statusline cost)
  if (inst.bridgeConnected) {
    var parts = [];
    if (inst.costUsd > 0) parts.push("$" + inst.costUsd.toFixed(2));
    if (inst.durationMs > 0) parts.push(formatDuration(inst.durationMs));
    if (parts.length) return parts.join(" · ");
  }
  if (!inst.hasConversation) return "";
  var tin = inst.totalInputTokens || 0;
  var tout = inst.totalOutputTokens || 0;
  var tcache = inst.totalCacheTokens || 0;
  var total = tin + tout + tcache;
  if (total <= 0) return "";
  return formatTokens(total) + " (in: " + formatTokens(tin) + ", out: " + formatTokens(tout) + ", cache: " + formatTokens(tcache) + ")";
}

// chatTokensDisplay 用于聊天面板底部 Tokens 信息:优先显示累计 token 明细(in/out/cache,
// 来自 jsonl),仅当无累计数据时(活跃会话 jsonl 未落盘)才回退到费用/时长。
// 与卡片的 totalTokensDisplay 相反——后者对桥接实例优先显示费用/时长,这里优先 token 明细。
function chatTokensDisplay(inst) {
  var tin = inst.totalInputTokens || 0;
  var tout = inst.totalOutputTokens || 0;
  var tcache = inst.totalCacheTokens || 0;
  var total = tin + tout + tcache;
  if (total > 0) {
    return formatTokens(total) + " (in: " + formatTokens(tin) + ", out: " + formatTokens(tout) + ", cache: " + formatTokens(tcache) + ")";
  }
  var parts = [];
  if (inst.costUsd > 0) parts.push("$" + inst.costUsd.toFixed(2));
  if (inst.durationMs > 0) parts.push(formatDuration(inst.durationMs));
  return parts.join(" · ");
}

// ---- 主题行右侧：会话动态信息 ----
function lastQueryDisplay(inst) {
  if (!inst.hasConversation || !inst.lastUserQuery) return "";
  return "📝 " + inst.lastUserQuery;
}
function turnsDisplay(inst) {
  if (!inst.hasConversation || !inst.turns) return "";
  return "🔄 " + inst.turns;
}
function toolDisplay(inst) {
  if (!inst.hasConversation || !inst.lastTool) return "";
  return "🔧 " + inst.lastTool;
}
// 最近助手回复挂 tooltip（hover 最近提问区显示）
function lastQueryTitle(inst) {
  if (!inst.hasConversation || !inst.lastReplySnip) return "";
  return "🤖 " + inst.lastReplySnip;
}

// ---- 对话历史区域 ----
function historyHTML(inst) {
  if (!inst.hasConversation) return "";
  if (!inst.history || inst.history.length === 0) {
    var msg = inst.bridgeConnected ? '📡 实时接入中 · 完整对话记录在会话结束后归档' : '（暂无对话记录）';
    return '<div class="card-history card-history-empty">'
      + '<span class="history-empty-msg">' + msg + '</span>'
      + '</div>';
  }
  var header = '<div class="history-header">'
    + '<span class="history-turns">🔄 ' + (inst.turns || inst.history.length) + ' 轮对话</span>';
  if (inst.lastTool) {
    header += ' · <span class="history-tool">🔧 ' + escHtml(inst.lastTool) + '</span>';
  }
  header += '<span class="history-header-spacer"></span>';
  header += '<button class="history-expand-btn" onclick="event.stopPropagation(); openChatPanel(' + inst.pid + ')" title="展开完整会话">⛶</button>';
  header += '</div>';
  var items = '';
  for (var i = 0; i < inst.history.length; i++) {
    var t = inst.history[i];
    items += '<div class="history-turn">'
      + '<div class="history-q">📝 ' + escHtml(t.q || "") + '</div>'
      + '<div class="history-r">🤖 ' + escHtml(t.r || "") + '</div>'
      + '</div>';
  }
  return '<div class="card-history" data-hist-hash="' + (inst.historyHash || 0) + '">' + header + items + '</div>';
}

function contextBar(inst) {
  if (!inst.hasConversation) return "（新会话）";
  if (inst.contextTokens <= 0) return "—";
  if (inst.contextLimit > 0) {
    var pct = Math.round(inst.contextTokens * 100 / inst.contextLimit);
    return unicodeBar(pct, 22);
  }
  return compactK(inst.contextTokens);
}

function contextBarClass(inst) {
  if (!inst.hasConversation || inst.contextTokens <= 0 || inst.contextLimit <= 0) return "";
  var pct = inst.contextTokens * 100 / inst.contextLimit;
  if (pct < 50) return "";
  if (pct < 80) return "mid";
  return "high";
}

function contextPct(inst) {
  if (!inst.hasConversation || inst.contextTokens <= 0 || inst.contextLimit <= 0) return "";
  return Math.round(inst.contextTokens * 100 / inst.contextLimit) + "%";
}

function contextDetail(inst) {
  if (!inst.hasConversation || inst.contextTokens <= 0) return "";
  if (inst.contextLimit > 0) return compactK(inst.contextTokens) + "/" + compactK(inst.contextLimit);
  return compactK(inst.contextTokens);
}

function unicodeBar(pct, width) {
  pct = Math.max(0, Math.min(100, pct));
  var filled = Math.floor(pct * width / 100);
  if (pct > 0 && filled === 0) filled = 1;
  var bar = "";
  for (var i = 0; i < width; i++) {
    bar += i < filled ? "━" : "─";
  }
  return bar;
}

function compactK(n) {
  if (n >= 1000000) return Math.floor(n / 1000000) + "M";
  if (n >= 1000) return Math.floor(n / 1000) + "k";
  return String(n);
}

function formatTokens(n) {
  if (n <= 0) return "—";
  if (n >= 1000000) return (n / 1000000).toFixed(1) + "M";
  if (n >= 1000) return (n / 1000).toFixed(1) + "k";
  return String(n);
}

function formatDuration(ms) {
  if (!ms || ms <= 0) return "—";
  var s = Math.floor(ms / 1000);
  var m = Math.floor(s / 60);
  if (m < 60) return m + " 分";
  var h = Math.floor(m / 60);
  return h + " 小时 " + (m % 60) + " 分";
}

function humanDuration(fromMs) {
  if (!fromMs || fromMs <= 0) return "—";
  var d = Date.now() - fromMs;
  if (d < 0) return "—";
  var sec = Math.floor(d / 1000);
  if (sec < 60) return sec + " 秒";
  if (sec < 3600) return Math.floor(sec / 60) + " 分钟";
  if (sec < 86400) return Math.floor(sec / 3600) + " 小时 " + Math.floor((sec % 3600) / 60) + " 分";
  return Math.floor(sec / 86400) + " 天 " + Math.floor((sec % 86400) / 3600) + " 小时";
}

// ---- Footer ----
function updateFooter(live, stats) {
  var el = document.getElementById("foot-msg");
  if (!footTimer) {
    if (live.length === 0) {
      el.textContent = "待机中 · 没有运行中的实例";
    } else {
      el.textContent = "正在监控 " + live.length + " 个实例 · 每 1 秒刷新";
    }
    el.className = "foot-msg";
  }
}

function flashFoot(msg) {
  var el = document.getElementById("foot-msg");
  el.textContent = msg;
  el.className = "foot-msg fresh";
  if (footTimer) clearTimeout(footTimer);
  footTimer = setTimeout(function() {
    el.className = "foot-msg fading";
    footTimer = setTimeout(function() {
      el.className = "foot-msg";
      footTimer = null;
    }, 1500);
  }, 3000);
}

// ---- Action Handlers ----
window.handleClear = async function(pid) {
  if (!confirm("确定要清空 PID " + pid + " 的会话吗？\n此操作将清除当前对话内容。")) return;
  try {
    await Call.ByID(ID_ACT_CLEAR, pid);
    flashFoot("✓  已向 PID " + pid + " 发送 /clear");
  } catch (e) {
    alert("清空失败: " + (e && e.message ? e.message : e));
  }
};

window.handleRewind = async function(pid) {
  try {
    await Call.ByID(ID_ACT_REWIND, pid);
    flashFoot("↺  已向 PID " + pid + " 发送 ESC×2（回溯）");
  } catch (e) {
    alert("回溯失败: " + (e && e.message ? e.message : e));
  }
};

window.handlePrompt = function(pid) {
  promptTargetPid = pid;
  loadSlashSuggestions(pid); // 预载斜杠命令/技能供消息框补全
  var overlay = document.getElementById("prompt-overlay");
  var input = document.getElementById("prompt-input");
  overlay.classList.remove("hidden");
  input.value = "";
  input.focus();
};

window.handleShowWin = async function(pid) {
  try {
    await Call.ByID(ID_ACT_SHOW, pid);
    flashFoot("🪟  已将 PID " + pid + " 的窗口置前");
  } catch (e) {
    alert("操作失败: " + (e && e.message ? e.message : e));
  }
};

// ---- Chat Panel ----

window.openChatPanel = async function(pid) {
  chatPanelPid = pid;
  chatHistoryHash = 0;
  lastChatMessages = [];
  lastReplySignature = '';
  procReset();
  loadSlashSuggestions(pid); // 预载斜杠命令/技能供消息框补全
  // 注意:不重置 ask 多问追踪状态——用户可能关闭面板后重开,中途的多问进度
  // (askQuestionIndex)应保留;重置交给 injectInteractivePrompts 在 tool_use ID 变化时做。

  // 标题显示当前会话主题（或回退到 PID）
  var meta = instanceMeta[pid];
  var topic = (meta && meta.topic) ? meta.topic : ('PID ' + pid);
  document.getElementById("chat-title").textContent = topic;
  var modelEl = document.getElementById("chat-model");
  if (meta && meta.model) {
    modelEl.textContent = meta.model;
    modelEl.style.display = "";
  } else {
    modelEl.style.display = "none";
  }
  var branchEl = document.getElementById("chat-branch");
  if (branchEl) {
    var br = (meta && meta.branch) ? meta.branch : "";
    branchEl.textContent = br ? ("🌿 " + br) : "";
    branchEl.style.display = br ? "" : "none";
  }
  var cwdEl = document.getElementById("chat-cwd");
  if (cwdEl) {
    var cwd = (meta && meta.cwd) ? meta.cwd : "";
    cwdEl.textContent = cwdTitle(cwd);
    cwdEl.title = cwd;
    cwdEl.style.display = cwd ? "" : "none";
  }

  document.getElementById("chat-messages").innerHTML = '<div class="chat-empty">加载中...</div>';
  var draftKey = pid + '|' + (meta && meta.cwd ? meta.cwd : '');
  document.getElementById("chat-input").value = chatDrafts[draftKey] || "";
  document.getElementById("chat-overlay").classList.remove("hidden");
  document.getElementById("chat-input").focus();

  await refreshChatMessages(pid);
  renderChatStats(pid);

  // 面板打开时启动 2 秒快速轮询（主循环 1 秒也刷新，双层保障）
  if (chatRefreshTimer) clearInterval(chatRefreshTimer);
  chatRefreshTimer = setInterval(function() {
    if (chatPanelPid !== null) refreshChatMessages(chatPanelPid);
  }, 2000);
};

// ---- 处理中/完成指示器状态机（Claude Code 风格 spinner） ----

// 把毫秒格式化为 Claude Code 风格用时：< 60s → "33s"，否则 → "2m 34s"。
function procFormatDuration(ms) {
  var s = Math.max(1, Math.round(ms / 1000));
  if (s < 60) return s + 's';
  var m = Math.floor(s / 60);
  return m + 'm ' + (s % 60) + 's';
}

function procClearTimers() {
  if (verbSwitchTimer) { clearTimeout(verbSwitchTimer); verbSwitchTimer = null; }
  if (completionTimer) { clearTimeout(completionTimer); completionTimer = null; }
  if (optimisticTimer) { clearTimeout(optimisticTimer); optimisticTimer = null; }
}

function procRandomVerbIdx() { return Math.floor(Math.random() * SPINNER_VERBS.length); }

// startProcessing：进入处理中态，开始计时 + 随机选动词 + 定期切换。
function startProcessing() {
  procStartTime = Date.now();
  procVerbIdx = procRandomVerbIdx();
  procState = 'processing';
  procHasBeenBusy = false;
  procRender();
  procScheduleVerbSwitch();
}

// 处理中每 3.5s 随机换一个动词，贴近 Claude Code 动效。
function procScheduleVerbSwitch() {
  if (verbSwitchTimer) clearTimeout(verbSwitchTimer);
  verbSwitchTimer = setTimeout(function() {
    if (procState !== 'processing') return;
    var next = procRandomVerbIdx();
    if (next === procVerbIdx) next = (next + 1) % SPINNER_VERBS.length;
    procVerbIdx = next;
    procRender();
    procScheduleVerbSwitch();
  }, 3500);
}

// completeProcessing：进入完成态，显示「动词过去式 + 用时」，停留 4s 后消失。
function completeProcessing() {
  if (completionTimer) clearTimeout(completionTimer);
  if (verbSwitchTimer) { clearTimeout(verbSwitchTimer); verbSwitchTimer = null; }
  var dur = procFormatDuration(Date.now() - procStartTime);
  procCompletionText = SPINNER_VERBS[procVerbIdx].ed + ' for ' + dur;
  procState = 'completed';
  procRender();
  completionTimer = setTimeout(function() {
    procState = 'idle';
    procRender();
  }, 4000);
}

// procReset：清空所有状态与定时器，隐藏指示器。
function procReset() {
  procClearTimers();
  procState = 'idle';
  procOptimistic = false;
  procHasBeenBusy = false;
  procRender();
}

// showProcessingOptimistic：发送消息后立即乐观进入处理中态（status 变 busy 前的空窗）。
function showProcessingOptimistic() {
  procOptimistic = true;
  if (optimisticTimer) clearTimeout(optimisticTimer);
  optimisticTimer = setTimeout(function() { procOptimistic = false; procUpdate(); }, 30000);
  startProcessing();
}

// procRender：按当前状态刷新指示器 DOM。
function procRender() {
  var el = document.getElementById('chat-processing');
  if (!el) return;
  var wasNearBottom = isChatNearBottom();
  if (procState === 'idle') { el.classList.add('hidden'); return; }
  el.classList.remove('hidden');
  el.classList.toggle('completed', procState === 'completed');
  var textEl = el.querySelector('.chat-processing-text');
  if (procState === 'processing') {
    if (textEl) textEl.textContent = SPINNER_VERBS[procVerbIdx].ing + '…';
  } else {
    if (textEl) textEl.textContent = procCompletionText;
  }
  // 仅在用户已在底部时跟随，避免打断查看历史
  if (wasNearBottom) {
    var body = document.querySelector('.chat-body');
    if (body) body.scrollTop = body.scrollHeight;
  }
}

// procUpdate：每秒由 renderChatStats 调用，驱动 idle↔processing↔completed 状态机。
function procUpdate() {
  var el = document.getElementById('chat-processing');
  if (!el) return;
  if (chatPanelPid === null) { procReset(); return; }
  var meta = instanceMeta[chatPanelPid];
  if (!meta) { procReset(); return; } // 实例已退出
  var busy = meta.status === 'busy';
  if (busy) {
    procHasBeenBusy = true;
    if (procState !== 'processing') startProcessing(); // 非用户触发的处理也开始计时
  } else if (procState === 'processing') {
    if (procHasBeenBusy) {
      completeProcessing(); // 经历 busy 后空闲 → 完成，显示用时
    } else if (!procOptimistic) {
      procReset(); // 乐观窗口已过且从未 busy → 放弃
    }
    // 否则：乐观窗口内尚未 busy，继续显示 processing
  }
}

// renderChatStats 渲染聊天面板底部 context/tokens 信息条，复用卡片的显示函数与配色。
function renderChatStats(pid) {
  var statsEl = document.getElementById("chat-stats");
  if (!statsEl) return;
  procUpdate(); // 放在最前，确保 early-return（实例退出）时也能更新处理中指示器
  if (pid === null) { statsEl.classList.add("hidden"); return; }
  var inst = instanceMeta[pid];
  if (!inst) { statsEl.classList.add("hidden"); return; } // 实例数据未就绪/已退出
  statsEl.classList.remove("hidden");

  // context：进度条（按用量配色）+ 百分比 + 明细，与卡片 context 行一致
  var barEl = document.getElementById("chat-ctx-bar");
  if (barEl) {
    barEl.className = "context-bar " + contextBarClass(inst);
    barEl.textContent = contextBar(inst);
  }
  var pctEl = document.getElementById("chat-ctx-pct");
  if (pctEl) pctEl.textContent = contextPct(inst);
  var detailEl = document.getElementById("chat-ctx-detail");
  if (detailEl) detailEl.textContent = contextDetail(inst);

  // tokens：累计 token 总量及 in/out/cache 明细（无累计数据时回退费用/时长，再无则 —）
  var tokensEl = document.getElementById("chat-tokens");
  if (tokensEl) {
    tokensEl.textContent = chatTokensDisplay(inst) || "—";
  }

  // 分支：每秒刷新，跟随用户在其他终端的分支切换
  var branchEl = document.getElementById("chat-branch");
  if (branchEl) {
    var br = inst.branch || "";
    branchEl.textContent = br ? ("🌿 " + br) : "";
    branchEl.style.display = br ? "" : "none";
  }

  // 模型：每秒刷新，跟随 /model 切换（openChatPanel 只在打开瞬间设一次，这里补刷新）
  var modelEl = document.getElementById("chat-model");
  if (modelEl) {
    var mdl = inst.model || "";
    modelEl.textContent = mdl;
    modelEl.style.display = mdl ? "" : "none";
  }

  // 主题：与 instanceMeta 对比，不一致时更新（新会话获得主题、/clear 后主题变更）
  var titleEl = document.getElementById("chat-title");
  if (titleEl) {
    var topic = (inst && inst.topic) ? inst.topic : ('PID ' + pid);
    if (titleEl.textContent !== topic) titleEl.textContent = topic;
  }

  // 目录：每秒刷新
  var cwdEl = document.getElementById("chat-cwd");
  if (cwdEl) {
    var cwd = inst.cwd || "";
    cwdEl.textContent = cwdTitle(cwd);
    cwdEl.title = cwd;
    cwdEl.style.display = cwd ? "" : "none";
  }
}

window.closeChatPanel = function() {
  document.getElementById("chat-overlay").classList.add("hidden");
  document.getElementById("chat-waiting").classList.add("hidden");
  document.getElementById("chat-quick-replies").classList.add("hidden");
  document.getElementById("chat-processing").classList.add("hidden");
  hideChatHint();
  hideSlash();
  chatPanelPid = null;
  chatHistoryHash = 0;
  lastChatMessages = [];
  lastReplySignature = '';
  procReset();
  askCustomPending = false; // 关面板清掉自定义输入态
  // 不重置 ask 多问追踪(保留中途进度,重开面板可续上)
  if (chatRefreshTimer) { clearInterval(chatRefreshTimer); chatRefreshTimer = null; }
};

// ---- 聊天面板回溯 ----
// 回溯选择器是 Claude Code 在终端渲染的 TUI，JSONL 里没有它的列表，
// 无法在面板精准复刻/安全驱动。这里发 Esc×2 打开选择器并把该实例终端置前，
// 面板内提示用户到终端选择回溯点。
window.handleChatRewind = async function() {
  if (!chatPanelPid) return;
  showChatHint('⏪ 已打开回溯选择器，请在终端选择回溯点…');
  try {
    await Call.ByID(ID_ACT_REWIND, chatPanelPid); // 发送 ESC×2
    await Call.ByID(ID_ACT_SHOW, chatPanelPid);   // 把该实例终端置前，立刻看到选择器
  } catch (e) {
    showChatHint('回溯失败: ' + (e && e.message ? e.message : String(e)));
  }
};

// showChatHint 显示聊天面板左下角的提示文案，4s 后自动隐藏（叠加调用重置计时）。
function showChatHint(msg) {
  var el = document.getElementById('chat-hint');
  if (!el) return;
  el.textContent = msg;
  el.classList.remove('hidden');
  if (chatHintTimer) clearTimeout(chatHintTimer);
  chatHintTimer = setTimeout(function() {
    el.classList.add('hidden');
    chatHintTimer = null;
  }, 4000);
}

// hideChatHint 立即隐藏提示并清计时器（关闭面板时调用）。
function hideChatHint() {
  if (chatHintTimer) { clearTimeout(chatHintTimer); chatHintTimer = null; }
  var el = document.getElementById('chat-hint');
  if (el) el.classList.add('hidden');
}

async function refreshChatMessages(pid) {
  if (pid === null) return;
  try {
    var result = await Call.ByID(ID_GET_CHAT_HISTORY, pid);
    // 新会话没有 JSONL 文件时 result.messages 为 null，当作空数组处理，
    // 避免因 early return 导致面板一直卡在「加载中...」。
    var messages = (result && result.messages) || [];
    var hash = (result && result.hash) || 0;
    if (hash === chatHistoryHash && chatHistoryHash !== 0 && messages.length > 0) {
      // 消息未变(hash 稳定),但实例状态(busy↔idle)或 AskUserQuestion 多问进度可能已变——
      // 交互按钮的显隐依赖这些信号,必须用最近渲染的消息重新评估,
      // 否则「面板已开 + 提示刚出现时短暂 busy」会永久错过按钮注入。
      injectInteractivePrompts(lastChatMessages);
      return;
    }
    chatHistoryHash = hash;
    renderChatMessages(messages);
  } catch (e) {
    console.error("Chat history error:", e);
    var msgEl = document.getElementById("chat-messages");
    msgEl.innerHTML = '<div class="chat-empty">加载失败: ' + (e && e.message ? e.message : String(e)) + '</div>';
  }
}

// isChatNearBottom 判断聊天面板是否已滚到底部附近（< 80px）。
// 用于决定自动滚动跟随最新内容——用户在查看历史时不打断。
function isChatNearBottom() {
  var body = document.querySelector(".chat-body");
  if (!body) return true;
  return body.scrollHeight - body.scrollTop - body.clientHeight < 80;
}

function renderChatMessages(messages) {
  lastChatMessages = messages || [];
  var container = document.getElementById("chat-messages");
  var html = '';
  for (var i = 0; i < messages.length; i++) {
    var m = messages[i];
    switch (m.role) {
    case 'user':
      // 纯注解消息（斜杠命令 / 系统提示 / 任务通知）渲染为居中事件行，不做左右气泡
      if (isAnnotationOnly(m.content || '')) {
        html += '<div class="chat-msg chat-msg-event">' + formatRichContent(m.content || '') + '</div>';
      } else {
        html += '<div class="chat-msg chat-msg-user">'
          + '<span class="chat-msg-label">📝 用户</span>'
          + formatRichContent(m.content || '')
          + '</div>';
      }
      break;
    case 'assistant':
      html += '<div class="chat-msg chat-msg-assistant">'
        + '<span class="chat-msg-label">🤖 助手</span>'
        + formatRichContent(m.content || '')
        + '</div>';
      break;
    case 'tool_use':
      html += '<div class="chat-msg chat-msg-tool">'
        + '<span class="chat-msg-label">🔧 调用工具: ' + escHtml(m.tool || '') + '</span>'
        + renderToolCallBody(m.tool, m.content || '', m.editStartLine || 0)
        + '</div>';
      break;
    case 'tool_result':
      var rt = m.content || '';
      if (rt.length > 6000) rt = rt.slice(0, 6000) + '\n... (结果过长，已截断)';
      html += '<div class="chat-msg chat-msg-tool-result">'
        + '<span class="chat-msg-label">📋 工具结果' + (m.toolId ? ' (' + escHtml(m.toolId) + ')' : '') + '</span>'
        + formatRichContent(rt)
        + '</div>';
      break;
    }
  }
  if (messages.length === 0) {
    html = '<div class="chat-empty">✨ 发送第一条消息，开始对话吧</div>';
  }
  // 重建前记录滚动状态：仅在用户原本就在底部附近时才跟随到底，否则保留原位置（不打断查看历史）
  var body = container.parentNode;
  var wasNearBottom = isChatNearBottom();
  var prevScrollTop = body ? body.scrollTop : 0;
  container.innerHTML = html;
  // 检测交互式提示并注入快速回复按钮
  injectInteractivePrompts(messages);
  if (wasNearBottom) {
    body.scrollTop = body.scrollHeight;
  } else if (body) {
    body.scrollTop = prevScrollTop; // 新消息追加在末尾，保持原位置仍指向之前的内容
  }
}

window.sendChatMessage = async function() {
  if (!chatPanelPid) return;
  var input = document.getElementById("chat-input");
  var text = input.value.trim();
  if (!text) return;

  // Type something 自定义输入态:走按键序列提交(不走普通 prompt)
  if (askCustomPending) {
    return submitAskCustom(text);
  }

  var btn = document.getElementById("chat-send-btn");
  btn.disabled = true;
  btn.textContent = "发送中...";

  try {
    await Call.ByID(ID_ACT_PROMPT, chatPanelPid, text);
    input.value = "";
    input.style.height = ""; // 重置 textarea 高度
    delete chatDrafts[chatDraftKey()]; // 发送成功，清除草稿
    // 乐观显示已发送的消息
    var container = document.getElementById("chat-messages");
    var optHTML = '<div class="chat-msg chat-msg-user">'
      + '<span class="chat-msg-label">📝 用户（已发送）</span>'
      + escHtml(text)
      + '</div>';
    container.insertAdjacentHTML("beforeend", optHTML);
    var body = container.parentNode;
    body.scrollTop = body.scrollHeight;
    // 立即显示「处理中」动效（乐观），status 变 busy 后接管
    showProcessingOptimistic();
    // 立即刷新 + 2 秒后再刷新，尽快捕获 AI 回复
    refreshChatMessages(chatPanelPid);
    setTimeout(function() { if (chatPanelPid) refreshChatMessages(chatPanelPid); }, 2000);
  } catch (e) {
    alert("发送失败: " + (e && e.message ? e.message : e));
  }
  btn.disabled = false;
  btn.textContent = "发送 ⏎";
};

// ---- Interactive Chat: quick-reply & waiting indicator ----

function injectInteractivePrompts(messages) {
  var waitingEl = document.getElementById("chat-waiting");
  var repliesEl = document.getElementById("chat-quick-replies");
  // Type something 自定义输入态:保持 banner,不重写选项区(用户正在下方输入框输入)
  if (askCustomPending) return;
  if (!messages || messages.length === 0) {
    waitingEl.classList.add("hidden");
    repliesEl.classList.add("hidden");
    lastReplySignature = '';
    return;
  }

  // 结构化判定:Claude Code 是否有挂起的 tool_use(等待用户选择)。
  // 只认 tool 层的真实选择场景(ExitPlanMode / AskUserQuestion / 权限请求),
  // 不看 assistant 文本——Claude Code 的选择 UI 由主程序渲染,不会出现在 text 里。
  var info = detectInteraction(messages);

  // AskUserQuestion 多问追踪:同一 tool_use 可能含多个问题,Claude Code 逐个询问
  // (答完 Q1 才显示 Q2)。活跃会话 jsonl 滞后,无外部信号告知当前问到第几题,
  // 只能本地追踪 askQuestionIndex:用户在消息框点选一题后推进(见 sendQuickReply)。
  // tool_use ID 变化(新一轮提问)或交互消失则重置回第 0 题。
  if (info && info.kind === 'ask' && info.askToolUseId) {
    if (info.askToolUseId !== askToolUseId) {
      askToolUseId = info.askToolUseId;
      askQuestionIndex = 0;
      askQuestionCount = (info.askQuestions || []).length;
      askAnswers = {}; // 新一轮提问,清空旧答案记忆
      askMultiSelectPicks = {}; // 新一轮提问,清空旧多选勾选(防泄漏)
    }
    var qs = info.askQuestions || [];
    if (askQuestionIndex < qs.length) {
      var cur = qs[askQuestionIndex];
      // hint 附加多选标记,提示用户可勾选多项
      var multiTag = cur.multiSelect ? '（可多选）' : '';
      info.hint = '❓ ' + (cur.question || cur.header || '请选择：')
        + (qs.length > 1 ? '  （' + (askQuestionIndex + 1) + '/' + qs.length + '）' : '')
        + (multiTag ? '  ' + multiTag : '');
      info.buttons = buildAskButtons(cur);
    } else {
      // 所有问题已答完(Claude Code 进入 Submit 步骤),不在消息框显示按钮;
      // 用户在终端按 Enter 提交即可。
      info = null;
    }
  } else if (!info || info.kind !== 'ask') {
    // 非 AskUserQuestion(无交互 / plan / perm),清空多问追踪状态
    askToolUseId = '';
    askQuestionIndex = 0;
    askQuestionCount = 0;
    askAnswers = {};
    askMultiSelectPicks = {};
  }

  // 交互暂停点判定:
  //   ExitPlanMode / AskUserQuestion → 这类工具不执行,未配对 tool_use 必然是
  //     「等待用户输入」,没有「结果尚未落盘」的 busy 态,故不依赖 busy/idle,
  //     一出现就给按钮(避免面板已开、提示刚冒出时短暂 busy 永久错过按钮)。
  //   其他工具(权限请求)→ 仍需 idle:busy 时未配对 tool_use 多半是工具执行中、
  //     result 尚未落盘,不是权限等待。
  var meta = instanceMeta[chatPanelPid];
  var isIdle = !!(meta && meta.status === 'idle');
  var alwaysInteractive = info && (info.kind === 'plan' || info.kind === 'ask');
  if (!info || (!alwaysInteractive && !isIdle)) {
    waitingEl.classList.add("hidden");
    repliesEl.classList.add("hidden");
    lastReplySignature = '';
    return;
  }

  // 显示等待状态
  waitingEl.classList.remove("hidden");

  // 高亮最后一条 assistant 消息
  var assistantEls = document.querySelectorAll(".chat-msg-assistant");
  if (assistantEls.length > 0) {
    assistantEls[assistantEls.length - 1].classList.add("chat-msg-interactive");
  }

  // 生成快速回复按钮(签名去重:每秒轮询重评估时,结构不变就不重写 innerHTML)
  // 签名只描述结构(题号 + 多选标志 + 选项列表),不含勾选态——勾选靠就地翻转 class,
  // 避免每秒轮询重写 innerHTML 冲掉用户的多选勾选(见 toggleAskPick)。
  var askMulti = info.kind === 'ask' && info.buttons.length > 0 && info.buttons[0].multi;
  var sig = info.kind;
  if (info.kind === 'ask') sig += '#' + askQuestionIndex + '|m=' + (askMulti ? 1 : 0);
  sig += '|' + info.buttons.map(function(b) { return (b.optionIndex != null ? b.optionIndex : b.value); }).join(',');
  if (sig !== lastReplySignature) {
    // 选项是否带说明文字(AskUserQuestion 的 option.description),或多选(需纵向勾选卡片)。
    var hasDesc = false;
    for (var j = 0; j < info.buttons.length; j++) {
      if (info.buttons[j].desc) { hasDesc = true; break; }
    }
    var fullwidth = hasDesc || askMulti; // 纵向满宽布局
    var multiQ = info.kind === 'ask' && askQuestionCount > 1; // 多问(显示 ‹ › 导航)
    // AskUserQuestion 多问时加 ‹ › 导航,且同步终端焦点(见 navAskQuestion)。
    var navPrev = '<button class="quick-reply-btn nav" onclick="navAskQuestion(-1)"'
      + (askQuestionIndex <= 0 ? ' disabled' : '') + '>‹</button>';
    var navNext = '<button class="quick-reply-btn nav" onclick="navAskQuestion(1)"'
      + (askQuestionIndex >= askQuestionCount ? ' disabled' : '') + '>›</button>';

    // 选项按钮分流:ask 多选(勾选) / ask 单选(按键序列) / plan·perm(发文本)
    var optsHTML = '';
    var currentAnswer = currentAskAnswer();
    if (fullwidth) optsHTML += '<div class="ask-option-group">';
    for (var j = 0; j < info.buttons.length; j++) {
      var b = info.buttons[j];
      var cls = b.cls || '';
      if (info.kind === 'ask' && b.multi) {
        // 多选:优先用 askMultiSelectPicks（当前编辑态），无则回退到已记忆答案 askAnswers。
        var picked = isAskPicked(b.optionIndex) || !!(currentAnswer && currentAnswer.kind === 'multi' && currentAnswer.picks && currentAnswer.picks[b.optionIndex]);
        optsHTML += '<button class="quick-reply-btn with-desc ask-multi' + (picked ? ' selected' : '') + '"'
          + ' data-opt-idx="' + b.optionIndex + '" onclick="toggleAskPick(' + b.optionIndex + ')">'
          + '<span class="ask-multi-box">' + (picked ? '☑' : '☐') + '</span>'
          + '<span class="ask-option-label">' + escHtml(b.label) + '</span>'
          + (b.desc ? '<span class="ask-option-desc">' + escHtml(b.desc) + '</span>' : '')
          + '</button>';
      } else if (info.kind === 'ask') {
        // 单选:按真实已选答案高亮；不再默认高亮第一项。
        var selected = isAskSingleSelected(b.optionIndex);
        var scls = selected ? ' primary selected' : '';
        if (b.desc) {
          optsHTML += '<button class="quick-reply-btn with-desc' + scls + '" onclick="sendQuickReply(' + b.optionIndex + ', \'ask\')">'
            + '<span class="ask-option-label">' + escHtml(b.label) + '</span>'
            + '<span class="ask-option-desc">' + escHtml(b.desc) + '</span>'
            + '</button>';
        } else {
          optsHTML += '<button class="quick-reply-btn' + scls + '" onclick="sendQuickReply(' + b.optionIndex + ', \'ask\')">' + escHtml(b.label) + '</button>';
        }
      } else {
        // plan / perm:维持发文本(ActPrompt),value='1'/'2'/'3'/'y'/'n'
        if (b.desc) {
          optsHTML += '<button class="quick-reply-btn with-desc ' + cls + '" onclick="sendQuickReply(\'' + escAttr(String(b.value)) + '\', \'' + info.kind + '\')">'
            + '<span class="ask-option-label">' + escHtml(b.label) + '</span>'
            + '<span class="ask-option-desc">' + escHtml(b.desc) + '</span>'
            + '</button>';
        } else {
          optsHTML += '<button class="quick-reply-btn ' + cls + '" onclick="sendQuickReply(\'' + escAttr(String(b.value)) + '\', \'' + info.kind + '\')">' + escHtml(b.label) + '</button>';
        }
      }
    }
    // ask 追加「✍ 自定义输入」(终端 Type something 入口) + 多选「✓ 确认提交」
    if (info.kind === 'ask') {
      var customSelected = currentAnswer && currentAnswer.kind === 'custom';
      var customLabel = customSelected ? ('✍ 已选自定义：' + (currentAnswer.text || '')) : '✍ 自定义输入';
      optsHTML += '<button class="quick-reply-btn ask-custom' + (customSelected ? ' selected' : '') + '" onclick="startAskCustom()">' + escHtml(customLabel) + '</button>';
    }
    if (askMulti) {
      optsHTML += '<button class="quick-reply-btn ask-submit" onclick="submitMultiSelect()">✓ 确认提交</button>';
    }
    if (fullwidth) optsHTML += '</div>';

    var btnsHTML;
    if (fullwidth && multiQ) {
      // 满宽场景:导航放头部,避免 › 单独落在末行。
      btnsHTML = navPrev + navNext + optsHTML;
    } else {
      // 横向 pill:维持夹层式 ‹ 选项 › 布局。
      btnsHTML = (multiQ ? navPrev : '') + optsHTML + (multiQ ? navNext : '');
    }
    repliesEl.innerHTML = '<span class="chat-msg-label" style="margin-right:6px">' + escHtml(info.hint) + '</span>' + btnsHTML;
    lastReplySignature = sig;
  }
  repliesEl.classList.remove("hidden");
}

// ---- 多选勾选状态(就地翻转,不触发轮询重渲染) ----
function askPicksKey() { return askToolUseId + '#' + askQuestionIndex; }
function isAskPicked(optionIndex) {
  var p = askMultiSelectPicks[askPicksKey()];
  return !!(p && p[optionIndex]);
}
// toggleAskPick 切换某选项的勾选态:更新 askMultiSelectPicks + 就地翻转按钮 DOM 的 class/复选框,
// 不动 lastReplySignature(签名不含勾选态),避免每秒轮询冲掉勾选。
window.toggleAskPick = function(optionIndex) {
  var k = askPicksKey();
  if (!askMultiSelectPicks[k]) askMultiSelectPicks[k] = {};
  if (askMultiSelectPicks[k][optionIndex]) delete askMultiSelectPicks[k][optionIndex];
  else askMultiSelectPicks[k][optionIndex] = true;
  var btn = document.querySelector('#chat-quick-replies button[data-opt-idx="' + optionIndex + '"]');
  if (btn) {
    var on = btn.classList.toggle('selected');
    var box = btn.querySelector('.ask-multi-box');
    if (box) box.textContent = on ? '☑' : '☐';
  }
};

// buildAskButtons 由一个 AskUserQuestion 问题(question)构造快速回复按钮。
// 每个按钮带 optionIndex(options 数组 0-based 原始下标),供 buildAskSequence 计算方向键次数;
// multi 标志透传给渲染层决定单选(点击即发)还是多选(点击勾选 + 单独提交)。
function buildAskButtons(question) {
  var opts = (question && question.options) || [];
  var multi = !!(question && question.multiSelect);
  var btns = [];
  for (var oi = 0; oi < opts.length; oi++) {
    var opt = opts[oi];
    var label = (typeof opt === 'object') ? (opt.label || '') : String(opt);
    // 「Type something.」是终端 UI 自动追加的自由输入项(样本确认不在 input 里,此处防御性跳过)。
    // 消息框用专门的「✍ 自定义输入」按钮承接(见 injectInteractivePrompts 渲染层)。
    if (label === 'Type something.') continue;
    var desc = (typeof opt === 'object' && opt.description) ? opt.description : '';
    btns.push({
      label: label,
      desc: desc,
      optionIndex: oi,  // options 数组原始下标(buildAskSequence 据此决定 ↓ 次数)
      multi: multi,     // 是否多选(渲染层据此决定点击行为)
      cls: ''           // 不再默认高亮第一项；高亮由 askAnswers/askMultiSelectPicks 的真实选择决定
    });
  }
  return btns;
}

function askAnswerKey() { return askToolUseId + '#' + askQuestionIndex; }
function currentAskAnswer() { return askAnswers[askAnswerKey()] || null; }
function setAskSingleAnswer(optionIndex, label) {
  askAnswers[askAnswerKey()] = { kind: 'single', optionIndex: optionIndex, label: label || '' };
}
function setAskMultiAnswer(picks, labels) {
  askAnswers[askAnswerKey()] = { kind: 'multi', picks: picks || {}, labels: labels || [] };
}
function setAskCustomAnswer(text) {
  askAnswers[askAnswerKey()] = { kind: 'custom', text: text || '' };
}
function isAskSingleSelected(optionIndex) {
  var a = currentAskAnswer();
  return !!(a && a.kind === 'single' && a.optionIndex === optionIndex);
}

// currentAskQuestion 取当前 AskUserQuestion 的当前题对象(由 askQuestionIndex 决定)。
// 重新调 detectInteraction(lastChatMessages) 取最新 questions,无额外缓存状态。
function currentAskQuestion() {
  var info = detectInteraction(lastChatMessages);
  if (!info || info.kind !== 'ask') return null;
  var qs = info.askQuestions || [];
  return qs[askQuestionIndex] || qs[0] || null;
}

// getOptionLabel 取 question.options[idx] 的 label 文本。
function getOptionLabel(question, optionIndex) {
  if (!question || !question.options) return '';
  var opt = question.options[optionIndex];
  if (opt == null) return '';
  return (typeof opt === 'object') ? (opt.label || '') : String(opt);
}

// buildAskSequence 把「对当前题的选择」翻译成终端按键 token 序列。
// 返回 [{key:'down'},...] 或 [{text:'abc'},...] 的数组,JSON.stringify 后交给 ActAskAnswer。
// 终端交互(Claude Code Select 上下文,依据官方 keybindings 文档 + Issue #22300):
//   - 数字键 '1'-'9':直接选择/切换第 N 项(单选已实测可用)。
//   - j/k:select:next/previous 的官方别名,可打印字符,替代注入不了的方向键 ↓/↑。
//   - Enter(\r):确认/提交。
// 终端交互(依据最新实测):
//   - 单选:数字键 '1'-'9' 直接作答当前题。非最后一题后面不能盲补回车,否则会落到下一题默认项。
//   - 多选:数字键 toggle 各项;真正的键盘 ↑/↓ 事件有效,可用 ↓ 导航。
//   - 多选 UI 结构 = 选项列表 + Type something + Submit,需多按一次 ↓ 越过 Type something 到 Submit。
//   - Type something:用 ↓ 导航到该项,再 Enter 进入输入。
// 多问切题需同步终端 ←/→ 焦点,否则消息框与终端会错位(见 navAskQuestion)。
function buildAskSequence(p) {
  var seq = [];
  // totalOptionsCount = question.options.length + 1(+1 为终端末尾自动追加的 Type something 项)
  var totalOpts = p.totalOptionsCount || 0;
  if (p.customText) {
    // Type something:从第 1 项 ↓ 到末尾 Type something(index=选项数) + Enter 进入输入 + 文本 + Enter 提交
    // 注意:Other 文本含数字会被 claude 误判为选项选择(其已知 bug),仅字母/符号可靠。
    for (var i = 0; i < totalOpts - 1; i++) seq.push({ key: 'down' });
    seq.push({ key: 'enter' });
    seq.push({ text: p.customText });
    seq.push({ key: 'enter' });
    return seq;
  }
  if (p.multiSelect) {
    // 多选:数字键 toggle 各选中项,然后用真正的 ↓ 键导航到 Submit 项 + 回车提交。
    // UI = 选项列表 + Type something + Submit;从第 1 项到 Submit 需 ↓×totalOpts 次
    // (越过选项数-1 次到 Type something,再多 1 次到 Submit)。
    var picks = (p.selectedIndices || []).slice();
    for (var s = 0; s < picks.length; s++) seq.push({ text: String(picks[s] + 1) });
    for (var t = 0; t < totalOpts; t++) seq.push({ key: 'down' });
    seq.push({ key: 'enter' });
    return seq;
  }
  // 单选:只发数字键(optionIndex+1)直接作答当前题。
  // 非最后一题若再补回车,会误命中下一题默认高亮项;最终确认由调用方在最后一题单独处理。
  var idx = (p.selectedIndices && p.selectedIndices[0] != null) ? p.selectedIndices[0] : 0;
  seq.push({ text: String(idx + 1) });
  return seq;
}

// NO_CONFIRM_TOOLS：Claude Code 默认自动批准、无需用户权限确认的工具。
// 这些工具 tool_use 未配对 tool_result 时只是「执行中」，不应误判为权限等待。
var NO_CONFIRM_TOOLS = {
  // 只读 / 查询
  'Read': 1, 'Grep': 1, 'Glob': 1, 'LS': 1, 'LSP': 1, 'NotebookRead': 1,
  'WebSearch': 1, 'WebFetch': 1,
  // 任务管理（自动批准）
  'TodoWrite': 1, 'Task': 1, 'Agent': 1,
  'TaskCreate': 1, 'TaskUpdate': 1, 'TaskGet': 1, 'TaskList': 1,
  'TaskOutput': 1, 'TaskStop': 1,
  // 定时任务（内部）
  'ScheduleWakeup': 1, 'CronCreate': 1, 'CronDelete': 1, 'CronList': 1,
  // 交互类（已有专门处理，不走 perm 分支）
  'EnterPlanMode': 1, 'ExitPlanMode': 1, 'AskUserQuestion': 1,
};

// detectInteraction 基于 messages 结构判定 Claude Code 当前的交互暂停点。
// 只识别 tool 层的真实选择场景——Claude Code 的选择 UI 由主程序在 tool 执行前渲染,
// 永远不会出现在 assistant 的 text 里,因此完全不看文本内容,杜绝「是否」「(y/n)」之类的误判。
// 返回 null 表示无挂起交互。
//
// 判定依据:最后一个 tool_use 是否「已配对到 tool_result」。
//   - 未配对(挂起) → Claude 正在等用户就这个工具做选择,按工具名分流:
//       ExitPlanMode    → Plan 审批
//       AskUserQuestion → 问题选项(取自 tool input)
//       其他工具        → 权限请求(等 yes/no)
//   - 已配对(工具执行完毕) → 返回 null
function detectInteraction(messages) {
  // 从末尾找最后一个 tool_use
  var lastToolUseIdx = -1;
  for (var i = messages.length - 1; i >= 0; i--) {
    if (messages[i].role === 'tool_use') {
      lastToolUseIdx = i;
      break;
    }
  }
  if (lastToolUseIdx === -1) return null;

  var lastToolUse = messages[lastToolUseIdx];

  // 该 tool_use 是否已有对应的 tool_result(用 toolId 配对)
  if (lastToolUse.toolId) {
    for (var j = lastToolUseIdx + 1; j < messages.length; j++) {
      if (messages[j].role === 'tool_result' && messages[j].toolId === lastToolUse.toolId) {
        return null; // 已执行完毕,无挂起
      }
    }
  }

  // 挂起的 tool_use,按工具名分流到对应选择场景。
  // 标签必须与 Claude Code 真实选项语义一致(发的是数字键 1/2/3,选第 N 项):
  //   1 = Yes, and bypass permissions  → 执行且跳过后续权限确认(最激进,UI 默认光标在此)
  //   2 = Yes, manually approve edits  → 执行但逐个手动确认编辑(继续询问)
  //   3 = Tell Claude what to change   → 不执行,给反馈让 Claude 改计划
  if (lastToolUse.tool === 'ExitPlanMode') {
    return {
      kind: 'plan',
      hint: '📋 Plan 审批：',
      buttons: [
        { label: '1. 执行·跳过权限确认', value: '1', cls: 'primary' },
        { label: '2. 执行·逐个确认编辑', value: '2', cls: '' },
        { label: '3. 告诉 Claude 怎么改', value: '3', cls: '' },
      ]
    };
  }

  if (lastToolUse.tool === 'AskUserQuestion') {
    try {
      var input = JSON.parse(lastToolUse.content || '{}');
      var questions = input.questions || [];
      if (questions.length > 0) {
        // 返回全部问题 + tool_use ID;具体展示第几题由 injectInteractivePrompts
        // 按 askQuestionIndex 决定(本地多问追踪)。这里默认取第 0 题。
        var q0 = questions[0];
        var btns = buildAskButtons(q0);
        if (btns.length > 0) {
          return {
            kind: 'ask',
            hint: '❓ ' + (q0.question || q0.header || '请选择：'),
            buttons: btns,
            askToolUseId: lastToolUse.toolId,
            askQuestions: questions
          };
        }
      }
    } catch (e) { /* 解析失败则落到下方通用权限/选择处理 */ }
  }

  // 已知不需要权限确认的工具（只读/自动批准/内部交互类）→ 不显示按钮。
  // 这类工具 tool_use 未配对只是「执行中」，结果落盘前的空窗不该被误判为权限等待。
  if (NO_CONFIRM_TOOLS[lastToolUse.tool]) return null;

  // 其他 tool_use 挂起 → 工具权限请求(Claude Code 在等用户允许/拒绝)
  return {
    kind: 'perm',
    hint: '🔐 权限请求（' + (lastToolUse.tool || '工具') + '）：',
    buttons: [
      { label: '✓ 允许 (y)', value: 'y', cls: 'primary' },
      { label: '✗ 拒绝 (n)', value: 'n', cls: 'danger' },
    ]
  };
}

// navAskQuestion 切换 AskUserQuestion 多问的当前题,并同步终端 ←/→ 焦点。
// 实测若只在前端切题、不同步终端,会出现:消息框看的是第 2 题,终端仍停第 1 题,
// 结果点击第 2 题选项时,第 1 题和第 2 题同时被误答。故这里恢复同步方向键。
window.navAskQuestion = function(delta) {
  if (!askToolUseId) return;
  var next = askQuestionIndex + delta;
  if (next < 0) next = 0;
  if (next > askQuestionCount) next = askQuestionCount;
  if (next === askQuestionIndex) return;
  // 自定义输入态下切题:先取消,避免 banner 指向错误题号
  if (askCustomPending) cancelAskCustom();
  askQuestionIndex = next;
  lastReplySignature = ''; // 强制重新注入(换题后按钮变了)
  if (chatPanelPid && next < askQuestionCount) {
    var key = delta > 0 ? 'right' : 'left';
    Call.ByID(ID_ACT_ASK_ANSWER, chatPanelPid, JSON.stringify([{ key: key }])).catch(function(e) {
      showChatHint('切题同步失败: ' + (e && e.message ? e.message : e));
    });
  }
  injectInteractivePrompts(lastChatMessages);
};

// sendQuickReply 发送快速回复。
// kind='ask'  → value 是 optionIndex,走按键序列(ActAskAnswer + buildAskSequence)驱动终端选择。
// kind='plan'/'perm' → value 是 '1'/'2'/'3'/'y'/'n',发文本(ActPrompt)。
window.sendQuickReply = async function(value, kind) {
  if (!chatPanelPid) return;
  try {
    var optimisticText = String(value);
    if (kind === 'ask') {
      // 单选:value = optionIndex。当前题只发数字键作答;若这是最后一题,再补最终确认页的 1 + Enter。
      var cur = currentAskQuestion();
      var isLastQuestion = askToolUseId && askQuestionIndex === askQuestionCount - 1;
      var seq = buildAskSequence({
        questionIndex: askQuestionIndex,
        totalCount: askQuestionCount,
        totalOptionsCount: (cur ? cur.options.length : 0) + 1,
        multiSelect: false,
        selectedIndices: [value],
        customText: ''
      });
      await Call.ByID(ID_ACT_ASK_ANSWER, chatPanelPid, JSON.stringify(seq));
      if (isLastQuestion) {
        await new Promise(function(resolve) { setTimeout(resolve, 200); });
        await Call.ByID(ID_ACT_ASK_ANSWER, chatPanelPid, JSON.stringify([{ text: '1' }]));
        await new Promise(function(resolve) { setTimeout(resolve, 80); });
        await Call.ByID(ID_ACT_ASK_ANSWER, chatPanelPid, JSON.stringify([{ key: 'enter' }]));
      }
      optimisticText = cur ? getOptionLabel(cur, value) : String(value);
      setAskSingleAnswer(value, optimisticText);
    } else {
      // plan / perm:发文本
      await Call.ByID(ID_ACT_PROMPT, chatPanelPid, String(value));
    }
    // 推进本地多问进度(AskUserQuestion 答完一题到下一题)
    if (askToolUseId && askQuestionIndex < askQuestionCount) {
      askQuestionIndex++;
    }
    showOptimisticReply(optimisticText);
    finishAskInteraction();
    refreshChatMessages(chatPanelPid);
    setTimeout(function() { if (chatPanelPid) refreshChatMessages(chatPanelPid); }, 2000);
  } catch (e) {
    alert("发送失败: " + (e && e.message ? e.message : e));
  }
};

// submitMultiSelect 提交当前多选题的所有勾选:构造多选按键序列注入终端。
window.submitMultiSelect = async function() {
  if (!chatPanelPid) return;
  var picks = askMultiSelectPicks[askPicksKey()] || {};
  var indices = Object.keys(picks).map(Number).sort(function(a, b) { return a - b; });
  if (indices.length === 0) { showChatHint('请至少勾选一项'); return; }
  var cur = currentAskQuestion();
  try {
    // 三阶段发送(关键):
    //  1) 先发数字键 toggle 勾选
    //  2) 等待 claude UI 消化勾选
    //  3) 像手动点调试键栏一样,每次只发一个 ↓,步进到 Submit 项后再单独回车
    // 原因:实测单发 ↓ 有效,但把多个 ↓ 批量/高速发送时 claude 多选 UI 不跟随。
    var toggleSeq = indices.map(function(i) { return { text: String(i + 1) }; });
    var totalOpts = (cur ? cur.options.length : 0) + 1; // +1 为 Type something
    var isLastQuestion = askToolUseId && askQuestionIndex === askQuestionCount - 1;

    await Call.ByID(ID_ACT_ASK_ANSWER, chatPanelPid, JSON.stringify(toggleSeq));
    await new Promise(function(resolve) { setTimeout(resolve, 200); });

    for (var t = 0; t < totalOpts; t++) {
      await Call.ByID(ID_ACT_ASK_ANSWER, chatPanelPid, JSON.stringify([{ key: 'down' }]));
      await new Promise(function(resolve) { setTimeout(resolve, 100); });
    }
    await Call.ByID(ID_ACT_ASK_ANSWER, chatPanelPid, JSON.stringify([{ key: 'enter' }]));

    // 最后一题会进入 AskUserQuestion 的最终确认页:
    //   1. Submit answers
    //   2. Cancel
    // 这里不能盲目对所有多选多发回车,否则在非最后一题会误伤下一题默认项。
    // 仅当当前题是最后一题时,等待确认页渲染后自动发 "1" + 回车完成最终提交。
    if (isLastQuestion) {
      await new Promise(function(resolve) { setTimeout(resolve, 200); });
      await Call.ByID(ID_ACT_ASK_ANSWER, chatPanelPid, JSON.stringify([{ text: '1' }]));
      await new Promise(function(resolve) { setTimeout(resolve, 80); });
      await Call.ByID(ID_ACT_ASK_ANSWER, chatPanelPid, JSON.stringify([{ key: 'enter' }]));
    }

    var labels = indices.map(function(i) { return getOptionLabel(cur, i); });
    setAskMultiAnswer(Object.assign({}, picks), labels);
    delete askMultiSelectPicks[askPicksKey()]; // 清该题勾选
    if (askToolUseId && askQuestionIndex < askQuestionCount) askQuestionIndex++;
    showOptimisticReply(labels.join('、'));
    finishAskInteraction();
    refreshChatMessages(chatPanelPid);
    setTimeout(function() { if (chatPanelPid) refreshChatMessages(chatPanelPid); }, 2000);
  } catch (e) {
    alert("发送失败: " + (e && e.message ? e.message : e));
  }
};

// ---- Type something 自定义输入流程 ----
// 点击「✍ 自定义输入」后,选项区换成提示 banner,聚焦下方输入框;
// 用户输入文本发送时(sendChatMessage 拦截)走 submitAskCustom 构造 Type something 序列。
window.startAskCustom = function() {
  if (!chatPanelPid) return;
  askCustomPending = true;
  askCustomQuestionIndex = askQuestionIndex;
  var repliesEl = document.getElementById("chat-quick-replies");
  repliesEl.innerHTML = '<div class="ask-custom-banner">'
    + '<span>✍ 正在为第 ' + (askQuestionIndex + 1) + ' 题输入自定义答案(下方输入框输入,发送即提交)</span>'
    + '<button class="ask-custom-cancel" onclick="cancelAskCustom()">✗ 取消</button>'
    + '</div>';
  repliesEl.classList.remove("hidden");
  document.getElementById("chat-waiting").classList.add("hidden");
  var input = document.getElementById("chat-input");
  input.value = "";
  input.placeholder = "输入自定义答案,发送即提交...";
  input.focus();
};

window.cancelAskCustom = function() {
  askCustomPending = false;
  var input = document.getElementById("chat-input");
  input.value = "";
  updateSendHints(); // 恢复 placeholder
  lastReplySignature = ''; // 触发重新渲染选项区
  injectInteractivePrompts(lastChatMessages);
};

// submitAskCustom 用 Type something 序列提交自定义文本。
// 文本换行扁平化为空格(终端输入框换行不可预测,与 ActPrompt 一致)。
async function submitAskCustom(text) {
  if (!chatPanelPid) return;
  var flat = text.replace(/\r\n/g, ' ').replace(/\n/g, ' ');
  var cur = currentAskQuestion();
  try {
    var seq = buildAskSequence({
      questionIndex: askCustomQuestionIndex,
      totalCount: askQuestionCount,
      totalOptionsCount: (cur ? cur.options.length : 0) + 1,
      multiSelect: false,
      selectedIndices: [],
      customText: flat
    });
    await Call.ByID(ID_ACT_ASK_ANSWER, chatPanelPid, JSON.stringify(seq));
    askCustomPending = false;
    var input = document.getElementById("chat-input");
    input.value = "";
    input.style.height = "";
    delete chatDrafts[chatDraftKey()];
    updateSendHints(); // 恢复 placeholder
    setAskCustomAnswer(flat);
    // 推进多问进度(对齐进入自定义模式时的题号)
    if (askToolUseId && askCustomQuestionIndex < askQuestionCount) {
      askQuestionIndex = askCustomQuestionIndex + 1;
    }
    showOptimisticReply('✍ ' + flat);
    finishAskInteraction();
    refreshChatMessages(chatPanelPid);
    setTimeout(function() { if (chatPanelPid) refreshChatMessages(chatPanelPid); }, 2000);
  } catch (e) {
    alert("发送失败: " + (e && e.message ? e.message : e));
    askCustomPending = false;
    updateSendHints();
    lastReplySignature = '';
    injectInteractivePrompts(lastChatMessages);
  }
}
window.submitAskCustom = submitAskCustom;

// ---- 交互作答的公共收尾 ----
// showOptimisticReply 在消息区追加一条「快速回复」气泡并滚到底。
function showOptimisticReply(text) {
  var container = document.getElementById("chat-messages");
  container.insertAdjacentHTML("beforeend", '<div class="chat-msg chat-msg-user">'
    + '<span class="chat-msg-label">📝 快速回复</span>'
    + escHtml(text) + '</div>');
  var body = container.parentNode;
  body.scrollTop = body.scrollHeight;
}

// finishAskInteraction 隐藏交互 UI、重置签名、显示处理中动效(下一题随后刷新时重新注入)。
function finishAskInteraction() {
  document.getElementById("chat-waiting").classList.add("hidden");
  document.getElementById("chat-quick-replies").classList.add("hidden");
  lastReplySignature = '';
  showProcessingOptimistic();
}

// ---- 调试键:逐个注入单键到终端,摸清 claude 选择 UI 的真实响应(临时,验证后删除) ----
// sendText 注入一个文本字符(如 j/k/数字);sendKey 注入一个控制键(方向键/回车/空格/Tab)。
window.sendText = async function(ch) {
  if (!chatPanelPid) return;
  try {
    await Call.ByID(ID_ACT_ASK_ANSWER, chatPanelPid, JSON.stringify([{ text: ch }]));
    showChatHint('已发送文本: ' + ch);
  } catch (e) { showChatHint('发送失败: ' + (e && e.message ? e.message : e)); }
};
window.sendKey = async function(key) {
  if (!chatPanelPid) return;
  try {
    await Call.ByID(ID_ACT_ASK_ANSWER, chatPanelPid, JSON.stringify([{ key: key }]));
    showChatHint('已发送键: ' + key);
  } catch (e) { showChatHint('发送失败: ' + (e && e.message ? e.message : e)); }
};

// ---- 消息框草稿：按 pid+cwd 存储，关闭面板后保留，发送后清除 ----
function chatDraftKey() {
  if (!chatPanelPid) return null;
  var meta = instanceMeta[chatPanelPid];
  var cwd = (meta && meta.cwd) ? meta.cwd : '';
  return chatPanelPid + '|' + cwd;
}

function saveChatDraft() {
  var key = chatDraftKey();
  if (!key) return;
  var val = document.getElementById("chat-input").value;
  if (val) { chatDrafts[key] = val; }
  else { delete chatDrafts[key]; }
}

// ---- 斜杠命令/技能自动补全 ----
// 复刻 Claude Code 终端体验：消息框输入 / 后弹出可用命令/技能列表，
// 上下键选中、Enter/Tab 补全为 /name + 空格（不发送），Esc 关闭。
// 下拉为 body 级浮层，按 textarea 的 getBoundingClientRect 定位，兼容对话面板与发送对话框。

// initSlashAutocomplete 绑定两个消息框的 input 事件 + 窗口缩放重定位。
function initSlashAutocomplete() {
  var chatInput = document.getElementById("chat-input");
  if (chatInput) {
    chatInput.addEventListener("input", onSlashInput);
    chatInput.addEventListener("input", saveChatDraft);
    chatInput.addEventListener("blur", function() { setTimeout(hideSlash, 120); });
  }
  var promptInput = document.getElementById("prompt-input");
  if (promptInput) {
    promptInput.addEventListener("input", onSlashInput);
    promptInput.addEventListener("blur", function() { setTimeout(hideSlash, 120); });
  }
  window.addEventListener("resize", function() { if (slashOpen) positionSlashMenu(); });
}

// loadSlashSuggestions 拉取该实例可用的命令/技能并缓存（面板打开时调用）。
async function loadSlashSuggestions(pid) {
  try {
    slashList = (await Call.ByID(ID_GET_COMMANDS, pid)) || [];
  } catch (e) {
    slashList = [];
  }
}

// onSlashInput：输入框内容以 / 开头且尚无空白时，按前缀筛选并展开下拉。
function onSlashInput(e) {
  slashInput = e.target;
  var val = slashInput.value;
  if (val.length > 0 && val.charAt(0) === '/' && !/\s/.test(val)) {
    var q = val.slice(1).toLowerCase();
    slashFiltered = slashList.filter(function(c) {
      return c.name.toLowerCase().indexOf(q) === 0;
    });
    // 内置优先，其余按名称排序，保证列表稳定可预期
    slashFiltered.sort(function(a, b) {
      if (a.type === 'builtin' && b.type !== 'builtin') return -1;
      if (b.type === 'builtin' && a.type !== 'builtin') return 1;
      return a.name < b.name ? -1 : (a.name > b.name ? 1 : 0);
    });
    if (slashFiltered.length > 0) {
      slashIdx = 0;
      showSlashMenu();
      return;
    }
  }
  hideSlash();
}

function slashTypeLabel(type) {
  return type === 'builtin' ? '内置' : (type === 'skill' ? '技能' : '命令');
}

function ensureSlashMenu() {
  var menu = document.getElementById("slash-menu");
  if (!menu) {
    menu = document.createElement("div");
    menu.id = "slash-menu";
    menu.className = "slash-menu hidden";
    document.body.appendChild(menu);
  }
  return menu;
}

function showSlashMenu() {
  slashOpen = true;
  var menu = ensureSlashMenu();
  menu.innerHTML = slashFiltered.map(function(c, i) {
    return '<div class="slash-item' + (i === slashIdx ? ' selected' : '') + '" data-idx="' + i + '">'
      + '<span class="slash-item-name">/' + escHtml(c.name) + '</span>'
      + '<span class="slash-item-type ' + (c.type || 'command') + '">' + escHtml(slashTypeLabel(c.type)) + '</span>'
      + '<span class="slash-item-desc">' + escHtml(c.description || '') + '</span>'
      + '</div>';
  }).join('');
  var items = menu.querySelectorAll(".slash-item");
  for (var i = 0; i < items.length; i++) {
    (function(item) {
      item.addEventListener("mouseenter", function() {
        slashIdx = parseInt(item.dataset.idx, 10);
        highlightSlash();
      });
      // mousedown（先于 blur）阻止 textarea 失焦，再补全
      item.addEventListener("mousedown", function(ev) {
        ev.preventDefault();
        slashIdx = parseInt(item.dataset.idx, 10);
        acceptSlash();
      });
    })(items[i]);
  }
  positionSlashMenu();
  menu.classList.remove("hidden");
}

function positionSlashMenu() {
  if (!slashInput) return;
  var menu = document.getElementById("slash-menu");
  if (!menu) return;
  var rect = slashInput.getBoundingClientRect();
  menu.style.left = rect.left + "px";
  menu.style.width = rect.width + "px";
  menu.style.bottom = (window.innerHeight - rect.top + 4) + "px"; // 浮在输入框正上方
}

function highlightSlash() {
  var menu = document.getElementById("slash-menu");
  if (!menu) return;
  var items = menu.querySelectorAll(".slash-item");
  for (var i = 0; i < items.length; i++) {
    items[i].classList.toggle("selected", i === slashIdx);
  }
  var sel = menu.querySelector(".slash-item.selected");
  if (sel) sel.scrollIntoView({ block: "nearest" });
}

// navigateSlash 上下移动选中项（循环）。
function navigateSlash(delta) {
  if (slashFiltered.length === 0) return;
  slashIdx = (slashIdx + delta + slashFiltered.length) % slashFiltered.length;
  highlightSlash();
}

// acceptSlash 用选中命令替换输入框的 /query，补全为 /name + 空格，保留焦点。
function acceptSlash() {
  if (!slashOpen || slashIdx >= slashFiltered.length) return;
  var c = slashFiltered[slashIdx];
  if (slashInput) {
    slashInput.value = "/" + c.name + " ";
    var len = slashInput.value.length;
    slashInput.setSelectionRange(len, len);
    slashInput.focus();
  }
  hideSlash();
}

function hideSlash() {
  slashOpen = false;
  var menu = document.getElementById("slash-menu");
  if (menu) menu.classList.add("hidden");
}

// ---- New Instance Panel ----

window.openNewInstancePanel = async function() {
  var overlay = document.getElementById("new-instance-overlay");
  var listEl = document.getElementById("recent-list");
  overlay.classList.remove("hidden");
  listEl.innerHTML = '<div class="recent-empty">加载中...</div>';

  var dirs = [];
  try { dirs = (await Call.ByID(ID_GET_RECENT_DIRS)) || []; } catch (e) { dirs = []; }

  newInstanceItems = [];
  var html = '';
  for (var i = 0; i < dirs.length; i++) {
    newInstanceItems.push({ type: 'dir', path: dirs[i] });
    html += renderRecentItem(i, '📂', dirs[i], false);
  }
  newInstanceItems.push({ type: 'pick' });
  html += renderRecentItem(newInstanceItems.length - 1, '📁', '选择其他目录...', true);

  listEl.innerHTML = html;
  // 默认选中第一项；无历史目录则选中「选择其他目录」
  newInstanceSelected = dirs.length > 0 ? 0 : (newInstanceItems.length - 1);
  newInstanceHighlight();
};

function renderRecentItem(idx, icon, label, isPick) {
  var cls = isPick ? 'recent-item recent-item-pick' : 'recent-item';
  return '<div class="' + cls + '" data-idx="' + idx + '" onclick="newInstanceActivate(' + idx + ')">'
    + '<span class="recent-item-icon">' + icon + '</span>'
    + '<span class="recent-item-path" title="' + escAttr(label) + '">' + escHtml(label) + '</span>'
    + '</div>';
}

window.closeNewInstancePanel = function() {
  document.getElementById("new-instance-overlay").classList.add("hidden");
  newInstanceSelected = -1;
  newInstanceItems = [];
};

function newInstanceHighlight() {
  var items = document.querySelectorAll('#recent-list .recent-item');
  for (var i = 0; i < items.length; i++) {
    items[i].classList.toggle('active', i === newInstanceSelected);
  }
  var sel = items[newInstanceSelected];
  if (sel) sel.scrollIntoView({ block: 'nearest' });
}

window.newInstanceActivate = async function(idx) {
  var item = newInstanceItems[idx];
  if (!item) return;
  closeNewInstancePanel();
  await doLaunchInstance(item.type === 'dir' ? item.path : "");
};

async function doLaunchInstance(workdir) {
  try {
    var used = await Call.ByID(ID_LAUNCH_INSTANCE, workdir);
    if (used === "" || used == null) return; // 用户在文件夹框取消，静默
    flashFoot("🚀 已在 " + (workdir ? workdir : "选定目录") + " 用 " + used + " 启动 claude");
    setTimeout(refresh, 1500); // 加快新实例出现在监控列表
  } catch (e) {
    alert("启动失败: " + (e && e.message ? e.message : e));
  }
}

// 新建实例面板键盘处理：面板打开时拦截 ↑↓/Enter/Esc。返回是否已处理。
function newInstanceKeyHandler(e) {
  var overlay = document.getElementById("new-instance-overlay");
  if (!overlay || overlay.classList.contains("hidden")) return false;
  if (newInstanceItems.length === 0) return false;
  if (e.key === "ArrowDown") {
    e.preventDefault();
    newInstanceSelected = (newInstanceSelected + 1) % newInstanceItems.length;
    newInstanceHighlight();
    return true;
  }
  if (e.key === "ArrowUp") {
    e.preventDefault();
    newInstanceSelected = (newInstanceSelected - 1 + newInstanceItems.length) % newInstanceItems.length;
    newInstanceHighlight();
    return true;
  }
  if (e.key === "Enter") {
    e.preventDefault();
    newInstanceActivate(newInstanceSelected);
    return true;
  }
  if (e.key === "Escape") {
    e.preventDefault();
    closeNewInstancePanel();
    return true;
  }
  return false;
}

// ---- Prompt Modal ----
window.hidePromptModal = function() {
  document.getElementById("prompt-overlay").classList.add("hidden");
  hideSlash();
  promptTargetPid = null;
};

window.sendPrompt = async function() {
  if (!promptTargetPid) return;
  var text = document.getElementById("prompt-input").value.trim();
  if (!text) return;
  try {
    await Call.ByID(ID_ACT_PROMPT, promptTargetPid, text);
    var display = text.length > 40 ? text.slice(0, 40) + "…" : text;
    flashFoot("✓  已向 PID " + promptTargetPid + " 发送：" + display);
  } catch (e) {
    alert("发送失败: " + (e && e.message ? e.message : e));
  }
  hidePromptModal();
};

document.addEventListener("keydown", function(e) {
  if (newInstanceKeyHandler(e)) return;

  // 斜杠命令下拉导航（对话面板 / 发送对话框均可触发）：菜单展开时拦截方向键、
  // Enter/Tab（补全而非发送）、Esc（仅关菜单）。必须在发送键判断之前处理。
  if (slashOpen) {
    if (e.key === "ArrowDown") { e.preventDefault(); navigateSlash(1); return; }
    if (e.key === "ArrowUp") { e.preventDefault(); navigateSlash(-1); return; }
    if (e.key === "Enter" || e.key === "Tab") { e.preventDefault(); acceptSlash(); return; }
    if (e.key === "Escape") { e.preventDefault(); hideSlash(); return; }
  }

  var promptOverlay = document.getElementById("prompt-overlay");
  var settingsOverlay = document.getElementById("settings-overlay");
  var chatOverlay = document.getElementById("chat-overlay");

  // 设置：Escape 关闭
  if (!settingsOverlay.classList.contains("hidden") && e.key === "Escape") {
    hideSettings();
    return;
  }

  // 聊天面板：可配置发送键，Ctrl/Cmd+Enter 始终发送，Escape 关闭
  // sendOnEnter=true  → 回车发送、Shift+回车换行
  // sendOnEnter=false → 回车换行、Shift+回车发送
  if (!chatOverlay.classList.contains("hidden")) {
    if (e.key === "Escape") {
      closeChatPanel();
      return;
    }
    if (e.key === "Enter" && shouldSendOnEnter(e)) {
      e.preventDefault();
      sendChatMessage();
      return;
    }
    return; // 聊天面板打开时不向下传递（其余按键交给 textarea 默认行为）
  }

  // Prompt 发送对话框：同上发送键逻辑
  if (promptOverlay.classList.contains("hidden")) return;
  if (e.key === "Escape") { hidePromptModal(); return; }
  if (e.key === "Enter" && shouldSendOnEnter(e)) {
    e.preventDefault();
    sendPrompt();
  }
});

// 判断当前 Enter 事件是否应触发「发送」。
// Ctrl/Cmd+Enter 永远发送；否则按 sendOnEnter 决定回车或 Shift+回车发送。
// Alt+Enter、纯换行、IME 组词确认(isComposing)等情况返回 false（交给默认行为）。
function shouldSendOnEnter(e) {
  if (e.isComposing) return false; // 中文输入法组词确认，不发送
  if (e.ctrlKey || e.metaKey) return true;
  if (e.altKey) return false;
  return sendOnEnter ? !e.shiftKey : e.shiftKey;
}

function updateClaudeSettingsToggleState() {
  var autoCheck = document.getElementById("toggle-auto-check-claude-settings");
  var autoRepair = document.getElementById("toggle-auto-repair-claude-settings");
  var row = document.getElementById("settings-item-auto-repair-claude-settings");
  if (!autoCheck || !autoRepair || !row) return;
  var enabled = !!autoCheck.checked;
  autoRepair.disabled = !enabled;
  row.classList.toggle("disabled", !enabled);
}

function buildRulesHTML(rules) {
  if (!rules) return '<div class="chat-empty">加载失败</div>';
  var statusPath = escapeHtml(rules.claudeSettingsPath || '~/.claude/settings.json');
  var backupPath = escapeHtml(rules.backupPath || '~/.claude-monitor/orig-statusline.json');
  var statusJson = escapeHtml(rules.statusLineJson || '');
  var hooksJson = escapeHtml(rules.hooksJson || '');
  return ''
    + '<div class="rules-section">'
    + '  <div class="rules-heading">两个开关的作用</div>'
    + '  <div class="rules-text">自动检查：每 10 秒检查 ' + statusPath + ' 是否仍保留监控器要求的 statusLine 与 lifecycle hooks。\n自动修复：发现配置漂移时，自动恢复监控器需要的 statusLine 与 lifecycle hooks；关闭后只检测不写回。</div>'
    + '</div>'
    + '<div class="rules-section">'
    + '  <div class="rules-heading">本应用修改规则</div>'
    + '  <div class="rules-text">只修改 ' + statusPath + '。\n只涉及 statusLine 与 4 个 lifecycle hooks（UserPromptSubmit / PreToolUse / PostToolUse / Stop）。\n不会修改 env、model、插件配置等其他字段。\n原 statusLine 会备份到 ' + backupPath + '。</div>'
    + '</div>'
    + '<div class="rules-section">'
    + '  <div class="rules-heading">statusLine 配置片段（可复制）</div>'
    + '  <textarea class="modal-textarea rules-code" readonly>' + statusJson + '</textarea>'
    + '</div>'
    + '<div class="rules-section">'
    + '  <div class="rules-heading">hooks 配置片段（可复制）</div>'
    + '  <textarea class="modal-textarea rules-code" readonly>' + hooksJson + '</textarea>'
    + '</div>'
    + '<div class="rules-section">'
    + '  <div class="rules-heading">手动维护说明</div>'
    + '  <div class="rules-text">如果关闭自动修复，可将以上片段合并到 ' + statusPath + ' 中手动维护。\n如需恢复原状，可在监控器中禁用桥接，或移除监控器注入的 statusLine 与 hooks 条目。</div>'
    + '</div>';
}

function escapeHtml(s) {
  return String(s || '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}
// ---- Settings ----
window.showSettings = async function() {
  try {
    var s = await Call.ByID(ID_GET_SETTINGS);
    document.getElementById("toggle-close-quits").checked = s.closeQuits;
    document.getElementById("toggle-auto-start").checked = s.autoStart;
    document.getElementById("about-version").textContent = "版本 " + (s.version || "--");
    var modeSelect = document.getElementById("select-launch-mode");
    if (modeSelect) modeSelect.value = s.launchWindowMode || "hide";
    var sendToggle = document.getElementById("toggle-enter-to-send");
    if (sendToggle) sendToggle.checked = !!s.enterToSend;
    var yoloToggle = document.getElementById("toggle-launch-yolo");
    if (yoloToggle) yoloToggle.checked = !!s.launchYolo;
    var autoCheckToggle = document.getElementById("toggle-auto-check-claude-settings");
    if (autoCheckToggle) autoCheckToggle.checked = s.autoCheckClaudeSettings !== false;
    var autoRepairToggle = document.getElementById("toggle-auto-repair-claude-settings");
    if (autoRepairToggle) autoRepairToggle.checked = s.autoRepairClaudeSettings !== false;
    autoCheckClaudeSettings = s.autoCheckClaudeSettings !== false;
    autoRepairClaudeSettings = s.autoRepairClaudeSettings !== false;
    updateClaudeSettingsToggleState();
  } catch (e) {
    flashFoot("加载设置失败: " + (e && e.message ? e.message : e));
  }
  document.getElementById("settings-overlay").classList.remove("hidden");
  window.switchSettingsCat("general");
};

window.hideSettings = function() {
  document.getElementById("settings-overlay").classList.add("hidden");
};

window.switchSettingsCat = function(cat) {
  var items = document.querySelectorAll(".settings-nav-item");
  for (var i = 0; i < items.length; i++) {
    items[i].classList.toggle("active", items[i].dataset.cat === cat);
  }
  document.getElementById("settings-general").classList.toggle("hidden", cat !== "general");
  document.getElementById("settings-system").classList.toggle("hidden", cat !== "system");
  document.getElementById("settings-about").classList.toggle("hidden", cat !== "about");
};

window.saveSetting = async function(key, val) {
  var closeQuits = document.getElementById("toggle-close-quits").checked;
  var autoStart = document.getElementById("toggle-auto-start").checked;
  var launchMode = document.getElementById("select-launch-mode").value;
  var enterToSend = document.getElementById("toggle-enter-to-send").checked;
  var launchYolo = document.getElementById("toggle-launch-yolo").checked;
  var autoCheck = document.getElementById("toggle-auto-check-claude-settings").checked;
  var autoRepair = document.getElementById("toggle-auto-repair-claude-settings").checked;
  try {
    await Call.ByID(ID_SAVE_SETTINGS, closeQuits, autoStart, launchMode, enterToSend, launchYolo, autoCheck, autoRepair);
    autoCheckClaudeSettings = !!autoCheck;
    autoRepairClaudeSettings = !!autoRepair;
    updateClaudeSettingsToggleState();
    if (key === "enterToSend") {
      sendOnEnter = !!enterToSend;
      updateSendHints();
    }
    var labels = {
      closeQuits: "关闭按钮行为",
      autoStart: "开机启动",
      launchMode: "终端窗口设置",
      enterToSend: "发送键设置",
      launchYolo: "新建实例权限设置",
      autoCheckClaudeSettings: "自动检查 Claude settings.json",
      autoRepairClaudeSettings: "自动修复 Claude settings.json"
    };
    flashFoot("✓  " + (labels[key] || "设置") + "已保存");
  } catch (e) {
    if (key === "closeQuits") document.getElementById("toggle-close-quits").checked = !val;
    else if (key === "autoStart") document.getElementById("toggle-auto-start").checked = !val;
    else if (key === "enterToSend") document.getElementById("toggle-enter-to-send").checked = !val;
    else if (key === "launchYolo") document.getElementById("toggle-launch-yolo").checked = !val;
    else if (key === "autoCheckClaudeSettings") document.getElementById("toggle-auto-check-claude-settings").checked = !val;
    else if (key === "autoRepairClaudeSettings") document.getElementById("toggle-auto-repair-claude-settings").checked = !val;
    updateClaudeSettingsToggleState();
    flashFoot("保存失败: " + (e && e.message ? e.message : e));
  }
};

window.showClaudeSettingsRules = async function() {
  var content = document.getElementById('claude-settings-rules-content');
  content.innerHTML = '<div class="chat-empty">加载中...</div>';
  document.getElementById('claude-settings-rules-overlay').classList.remove('hidden');
  try {
    var rules = await Call.ByID(ID_GET_BRIDGE_RULES);
    content.innerHTML = buildRulesHTML(rules);
  } catch (e) {
    content.innerHTML = '<div class="chat-empty">加载失败：' + escapeHtml(e && e.message ? e.message : e) + '</div>';
  }
};

window.hideClaudeSettingsRules = function() {
  document.getElementById('claude-settings-rules-overlay').classList.add('hidden');
};

window.openSettingsGithub = async function() {
  try {
    await Call.ByID(ID_OPEN_URL, "https://github.com/pie-tk/claude-code-monitor");
  } catch (e) {
    flashFoot("打开失败: " + (e && e.message ? e.message : e));
  }
};

// ---- Update ----
let pendingDownloadURL = "";

function showUpdateStatus(which) {
  // 互斥：只显示一个状态 span，移除 hidden 类并用 display 控制
  var area = document.getElementById("update-area");
  area.classList.remove("hidden");
  var states = ["update-checking", "update-available", "update-uptodate", "update-error"];
  for (var i = 0; i < states.length; i++) {
    var el = document.getElementById(states[i]);
    if (el) el.classList.toggle("hidden", states[i] !== which);
  }
}

window.checkUpdateManually = async function() {
  var btn = document.getElementById("update-check-btn");
  if (btn.disabled) return;

  var label = btn.textContent;
  btn.disabled = true;
  btn.textContent = label + " (冷却 10s)";

  // 倒计时显示
  var remain = 10;
  var timer = setInterval(function() {
    remain--;
    if (remain <= 0) {
      clearInterval(timer);
      btn.textContent = label;
      btn.disabled = false;
    } else {
      btn.textContent = label + " (冷却 " + remain + "s)";
    }
  }, 1000);

  showUpdateStatus("update-checking");
  try {
    var info = await Call.ByID(ID_CHECK_UPDATE);
    if (info) {
      document.getElementById("update-version").textContent = "v" + (info.version || "");
      document.getElementById("update-notes").textContent = info.body || "";
      pendingDownloadURL = info.downloadUrl || "";
      showUpdateStatus("update-available");
    } else {
      showUpdateStatus("update-uptodate");
    }
  } catch (e) {
    var errMsg = e && e.message ? e.message : "检查失败，请检查网络";
    var errEl = document.getElementById("update-error");
    if (errMsg.indexOf("限流") >= 0 || errMsg.indexOf("网络请求失败") >= 0) {
      errEl.innerHTML = '⚠ ' + errMsg + '<br><button class="about-link" style="margin-top:6px" onclick="window.openSettingsGithub()">在浏览器中查看 Releases</button>';
    } else {
      errEl.textContent = "⚠ " + errMsg;
    }
    showUpdateStatus("update-error");
  }
};

window.downloadUpdate = async function() {
  if (!pendingDownloadURL) {
    flashFoot("没有可用的下载地址");
    return;
  }
  if (!confirm("确定要下载并安装更新吗？\n\n应用将在下载完成后自动重启。")) return;

  var btn = document.getElementById("update-download-btn");
  var bar = document.getElementById("update-progress-bar");
  var fill = document.getElementById("update-progress-fill");
  btn.textContent = "⬇ 下载中…";
  btn.disabled = true;
  bar.classList.remove("hidden");
  fill.style.width = "0%";

  Events.Off("update:progress");
  Events.On("update:progress", function(evt) {
    var d = evt.data;
    if (d.status === "downloading") {
      var pct = d.percent || 0;
      btn.textContent = "⬇ 下载中 " + pct + "%";
      fill.style.width = pct + "%";
    } else if (d.status === "error") {
      Events.Off("update:progress");
      flashFoot("更新失败: " + (d.message || "未知错误"));
      btn.textContent = "⬇ 下载并安装更新";
      btn.disabled = false;
      bar.classList.add("hidden");
      fill.style.width = "0%";
    }
  });

  try {
    await Call.ByID(ID_DOWNLOAD_UPDATE, pendingDownloadURL);
  } catch (e) {
    Events.Off("update:progress");
    flashFoot("更新失败: " + (e && e.message ? e.message : e));
    btn.textContent = "⬇ 下载并安装更新";
    btn.disabled = false;
    bar.classList.add("hidden");
    fill.style.width = "0%";
  }
};

// Bind the check button
document.getElementById("update-check-btn").addEventListener("click", function() {
  window.checkUpdateManually();
});
// Bind the download button
document.getElementById("update-download-btn").addEventListener("click", function() {
  window.downloadUpdate();
});
