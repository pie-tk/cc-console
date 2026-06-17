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
const ID_OPEN_URL         = 2662437060;
const ID_CHECK_UPDATE     = 2276698880;
const ID_DOWNLOAD_UPDATE  = 1405235130;
const ID_GET_CHAT_HISTORY  = 3915737321;
const ID_GET_RECENT_DIRS   = 3059062206;
const ID_LAUNCH_INSTANCE   = 3964521291;
const ID_PICK_DIRECTORY    = 3885139809;
const ID_GET_COMMANDS      = 4004856507; // GetCommandSuggestions(pid)

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
// AskUserQuestion 多问追踪:同一 tool_use 内多个问题按序展示。
// 活跃会话 jsonl 滞后(答完一题 jsonl 不更新),没有外部信号告知「现在问到第几题」,
// 只能本地追踪——用户在消息框点选一题后推进到下一题。tool_use ID 变化或交互消失则重置。
let askToolUseId = '';
let askQuestionIndex = 0;
let askQuestionCount = 0;
let instanceMeta = {}; // pid → {topic, model}
let newInstanceSelected = -1; // 新建实例面板当前选中项索引
let newInstanceItems = [];    // 新建实例面板项：[{type:'dir',path}, {type:'pick'}]
let sendOnEnter = true;       // 消息框发送键：true=回车发送(Shift+回车换行)；false=回车换行(Shift+回车发送)

// ---- 斜杠命令自动补全状态 ----
let slashList = [];       // 全量命令/技能建议缓存
let slashFiltered = [];   // 当前筛选结果
let slashIdx = 0;         // 选中下标
let slashOpen = false;    // 下拉是否展开
let slashInput = null;    // 当前绑定的 textarea（chat-input 或 prompt-input）

// ---- Boot ----
async function boot() {
  try {
    await applyTheme();
  } catch (e) {
    console.error("Theme init error:", e);
  }
  loadSendMode(); // 加载消息框发送键设置，更新占位符/提示文案
  initSlashAutocomplete(); // 绑定消息框斜杠命令自动补全
  refresh();
  setInterval(refresh, 1000);
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
  const newMeta = {};
  for (var i = 0; i < all.length; i++) {
    var inst = all[i];
    newMeta[inst.pid] = {
      topic: inst.topic || '',
      model: inst.model || '',
      branch: inst.gitBranch || '',
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

  if (all.length === 0) {
    container.innerHTML = "";
    emptyState.classList.remove("hidden");
    currentPids = [];
    return;
  }
  emptyState.classList.add("hidden");

  const newPids = all.map(i => i.pid).join(",");
  const oldPids = currentPids.join(",");

  if (newPids !== oldPids) {
    container.innerHTML = all.map(cardHTML).join("");
    currentPids = all.map(i => i.pid);
    container.querySelectorAll(".card-history").forEach(function(h) { h.scrollTop = h.scrollHeight; });
  } else {
    all.forEach((inst, i) => {
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
    + '<span class="card-title" data-field="title" title="' + escAttr(cwd) + '">' + escHtml(title) + '</span>'
    + '<span class="card-branch" data-field="branch">' + escHtml(branchDisplay(inst)) + '</span>'
    + '<span class="card-status ' + statusClass + '" data-field="status">' + label + '</span>'
    + '<span class="card-bridge-tag' + (inst.bridgeConnected ? '' : ' show') + '" data-field="bridge" title="statusline 桥接尚未生效，实时数据待接入（新会话刷新后自动接入）">⏳ 未接入</span>'
    + '<span class="card-pid-subtle">PID ' + inst.pid + '</span>'
    + '<span class="card-model" data-field="model">' + model + '</span>'
    + '<span class="card-duration" data-field="duration">' + humanDuration(inst.startedAt) + '</span>'
    + '</div>'
    + '<div class="card-row card-topic-row">'
    + '<span class="card-topic" data-field="topic">💬 ' + topic + '</span>'
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
  set("[data-field=title]", cwdTitle(inst.cwd || ""));
  set("[data-field=branch]", branchDisplay(inst));
  set("[data-field=status]", statusLabel(inst.status));
  set("[data-field=model]", modelDisplay(inst));
  set("[data-field=duration]", humanDuration(inst.startedAt));
  set("[data-field=topic]", "💬 " + topicDisplay(inst));
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
  if (titleEl) titleEl.title = inst.cwd || "";
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
  case 'system': return '<div class="cc-block cc-system"><span class="cc-label">系统</span>' + escHtml(body) + '</div>';
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
      if (end < 0) { html += escHtml(text.slice(i)); break; }
      try { html += renderCommandCard(JSON.parse(text.slice(i + 4, end))); } catch (e) { /* 跳过 */ }
      i = end + 1;
      continue;
    }
    var lt = text.indexOf('<', i);
    if (lt < 0) { html += escHtml(text.slice(i)); break; }
    var m = tagRe.exec(text.slice(lt));
    if (!m || !CC_BLOCK_TAGS[m[1]]) {
      // 非已知标签（含代码里的 <）：当普通文本，仅吃掉这个 '<' 继续扫描
      html += escHtml(text.slice(i, lt + 1));
      i = lt + 1;
      continue;
    }
    html += escHtml(text.slice(i, lt)); // 前导文本
    var name = m[1];
    var afterOpen = lt + m[0].length;
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

  document.getElementById("chat-messages").innerHTML = '<div class="chat-empty">加载中...</div>';
  document.getElementById("chat-input").value = "";
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

// renderChatStats 渲染聊天面板底部 context/tokens 信息条，复用卡片的显示函数与配色。
function renderChatStats(pid) {
  var statsEl = document.getElementById("chat-stats");
  if (!statsEl) return;
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
}

window.closeChatPanel = function() {
  document.getElementById("chat-overlay").classList.add("hidden");
  document.getElementById("chat-waiting").classList.add("hidden");
  document.getElementById("chat-quick-replies").classList.add("hidden");
  hideSlash();
  chatPanelPid = null;
  chatHistoryHash = 0;
  lastChatMessages = [];
  lastReplySignature = '';
  // 不重置 ask 多问追踪(保留中途进度,重开面板可续上)
  if (chatRefreshTimer) { clearInterval(chatRefreshTimer); chatRefreshTimer = null; }
};

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
        + '<div class="chat-msg-tool-input">' + escHtml(m.content || '') + '</div>'
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
  container.innerHTML = html;
  // 检测交互式提示并注入快速回复按钮
  injectInteractivePrompts(messages);
  // 滚动到底部
  var body = container.parentNode;
  body.scrollTop = body.scrollHeight;
}

window.sendChatMessage = async function() {
  if (!chatPanelPid) return;
  var input = document.getElementById("chat-input");
  var text = input.value.trim();
  if (!text) return;

  var btn = document.getElementById("chat-send-btn");
  btn.disabled = true;
  btn.textContent = "发送中...";

  try {
    await Call.ByID(ID_ACT_PROMPT, chatPanelPid, text);
    input.value = "";
    input.style.height = ""; // 重置 textarea 高度
    // 乐观显示已发送的消息
    var container = document.getElementById("chat-messages");
    var optHTML = '<div class="chat-msg chat-msg-user">'
      + '<span class="chat-msg-label">📝 用户（已发送）</span>'
      + escHtml(text)
      + '</div>';
    container.insertAdjacentHTML("beforeend", optHTML);
    var body = container.parentNode;
    body.scrollTop = body.scrollHeight;
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
    }
    var qs = info.askQuestions || [];
    if (askQuestionIndex < qs.length) {
      var cur = qs[askQuestionIndex];
      info.hint = '❓ ' + (cur.question || cur.header || '请选择：')
        + (qs.length > 1 ? '  （' + (askQuestionIndex + 1) + '/' + qs.length + '）' : '');
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

  // 生成快速回复按钮(签名去重:每秒轮询重评估时,内容不变就不重写 innerHTML)
  var sig = info.kind;
  if (info.kind === 'ask') sig += '#' + askQuestionIndex; // 含题号,切换题目必触发重渲染
  sig += '|' + info.buttons.map(function(b) { return b.value; }).join(',');
  if (sig !== lastReplySignature) {
    var btnsHTML = '';
    // AskUserQuestion 多问时加 ‹ › 导航:活跃会话 jsonl 滞后,无法自动得知终端
    // 当前问到第几题(用户可能在终端答过),让用户手动对齐消息框与终端的当前题。
    if (info.kind === 'ask' && askQuestionCount > 1) {
      btnsHTML += '<button class="quick-reply-btn nav" onclick="navAskQuestion(-1)"'
        + (askQuestionIndex <= 0 ? ' disabled' : '') + '>‹</button>';
    }
    for (var j = 0; j < info.buttons.length; j++) {
      var b = info.buttons[j];
      var cls = b.cls || '';
      btnsHTML += '<button class="quick-reply-btn ' + cls + '" onclick="sendQuickReply(\'' + escAttr(String(b.value)) + '\')">' + escHtml(b.label) + '</button>';
    }
    if (info.kind === 'ask' && askQuestionCount > 1) {
      btnsHTML += '<button class="quick-reply-btn nav" onclick="navAskQuestion(1)"'
        + (askQuestionIndex >= askQuestionCount ? ' disabled' : '') + '>›</button>';
    }
    repliesEl.innerHTML = '<span class="chat-msg-label" style="margin-right:6px">' + escHtml(info.hint) + '</span>' + btnsHTML;
    lastReplySignature = sig;
  }
  repliesEl.classList.remove("hidden");
}

// buildAskButtons 由一个 AskUserQuestion 问题(question)构造快速回复按钮。
// value 用选项序号(1-based)——Claude Code 终端 UI 靠数字键选择选项，
// 发送标签文本无法可靠匹配（含 emoji 等特殊字符时尤其容易误选第一项）。
function buildAskButtons(question) {
  var opts = (question && question.options) || [];
  var btns = [];
  var num = 0;
  for (var oi = 0; oi < opts.length; oi++) {
    var opt = opts[oi];
    var label = opt.label || opt;
    // 「Type something.」是自由输入项,不是可点选的预设,跳过(交给终端输入)
    if (typeof opt === 'string' ? opt === 'Type something.' : (opt.label === 'Type something.')) {
      continue;
    }
    num++;
    btns.push({
      label: label,
      value: String(num),
      cls: oi === 0 ? 'primary' : ''
    });
  }
  return btns;
}

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

// navAskQuestion 手动切换 AskUserQuestion 多问的当前题(不发送任何内容,仅本地推进/回退)。
// 用于消息框与终端当前题对齐——活跃会话 jsonl 滞后,无法自动得知终端问到第几题。
window.navAskQuestion = function(delta) {
  if (!askToolUseId) return;
  var next = askQuestionIndex + delta;
  if (next < 0) next = 0;
  if (next > askQuestionCount) next = askQuestionCount;
  if (next === askQuestionIndex) return;
  askQuestionIndex = next;
  lastReplySignature = ''; // 强制重新注入(换题后按钮变了)
  injectInteractivePrompts(lastChatMessages);
};

// sendQuickReply 发送快速回复（y/n 或选项文本）。
window.sendQuickReply = async function(text) {
  if (!chatPanelPid) return;
  try {
    await Call.ByID(ID_ACT_PROMPT, chatPanelPid, text);
    // 若答的是 AskUserQuestion 的某题,推进本地多问进度到下一题
    // (jsonl 不记录答题进度,只能本地追踪;injectInteractivePrompts 据此显示下一题)。
    if (askToolUseId && askQuestionIndex < askQuestionCount) {
      askQuestionIndex++;
    }
    // 乐观显示
    var container = document.getElementById("chat-messages");
    var optHTML = '<div class="chat-msg chat-msg-user">'
      + '<span class="chat-msg-label">📝 快速回复</span>'
      + escHtml(text)
      + '</div>';
    container.insertAdjacentHTML("beforeend", optHTML);
    var body = container.parentNode;
    body.scrollTop = body.scrollHeight;
    // 隐藏交互 UI(下一题会在随后刷新时按新 index 重新注入)
    document.getElementById("chat-waiting").classList.add("hidden");
    document.getElementById("chat-quick-replies").classList.add("hidden");
    lastReplySignature = '';
    // 快速刷新
    refreshChatMessages(chatPanelPid);
    setTimeout(function() { if (chatPanelPid) refreshChatMessages(chatPanelPid); }, 2000);
  } catch (e) {
    alert("发送失败: " + (e && e.message ? e.message : e));
  }
};

// ---- 斜杠命令/技能自动补全 ----
// 复刻 Claude Code 终端体验：消息框输入 / 后弹出可用命令/技能列表，
// 上下键选中、Enter/Tab 补全为 /name + 空格（不发送），Esc 关闭。
// 下拉为 body 级浮层，按 textarea 的 getBoundingClientRect 定位，兼容对话面板与发送对话框。

// initSlashAutocomplete 绑定两个消息框的 input 事件 + 窗口缩放重定位。
function initSlashAutocomplete() {
  var chatInput = document.getElementById("chat-input");
  if (chatInput) {
    chatInput.addEventListener("input", onSlashInput);
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
  try {
    await Call.ByID(ID_SAVE_SETTINGS, closeQuits, autoStart, launchMode, enterToSend, launchYolo);
    // 发送键设置需即时生效：更新内存状态并刷新输入框提示文案
    if (key === "enterToSend") {
      sendOnEnter = !!enterToSend;
      updateSendHints();
    }
    var labels = {
      closeQuits: "关闭按钮行为",
      autoStart: "开机启动",
      launchMode: "终端窗口设置",
      enterToSend: "发送键设置",
      launchYolo: "新建实例权限设置"
    };
    flashFoot("✓  " + (labels[key] || "设置") + "已保存");
  } catch (e) {
    if (key === "closeQuits") document.getElementById("toggle-close-quits").checked = !val;
    else if (key === "autoStart") document.getElementById("toggle-auto-start").checked = !val;
    else if (key === "enterToSend") document.getElementById("toggle-enter-to-send").checked = !val;
    else if (key === "launchYolo") document.getElementById("toggle-launch-yolo").checked = !val;
    flashFoot("保存失败: " + (e && e.message ? e.message : e));
  }
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
    Off("update:progress");
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
