// 直接使用 Wails runtime Call.ByID，绕过自动绑定的循环依赖问题
// ID 取自自动生成的 frontend/bindings/claude-monitor/service/monitorservice.js
import { Call } from "@wailsio/runtime";

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
const ID_OPEN_URL      = 2662437060;

// ---- State ----
let currentPids = [];
let promptTargetPid = null;
let footTimer = null;

// ---- Boot ----
async function boot() {
  try {
    await applyTheme();
  } catch (e) {
    console.error("Theme init error:", e);
  }
  refresh();
  setInterval(refresh, 1000);
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
  if (!stats || stats.online === 0) {
    el.textContent = "🌙  当前无实例运行";
    return;
  }
  const parts = ["在线 " + stats.online, "🔴 " + stats.busy + " 忙碌", "🟢 " + stats.idle + " 空闲"];
  if (stats.context > 0) parts.push("📦 " + formatTokens(stats.context) + " context");
  if (stats.stale > 0) parts.push("🌓 " + stats.stale + " 残留");
  el.textContent = parts.join("  ·  ");
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
  const all = [...live, ...stale.map(s => Object.assign({}, s, { _stale: true }))];

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
  } else {
    all.forEach((inst, i) => {
      updateCardText(container.children[i], inst);
    });
  }
}

function cardHTML(inst) {
  const stale = inst._stale ? " stale" : "";
  const emoji = statusEmoji(inst.status);
  const statusClass = inst.status || "unknown";
  const label = statusLabel(inst.status);
  const model = modelDisplay(inst);
  const cwd = inst.cwd || "";
  const topic = topicDisplay(inst);
  const ctxBar = contextBar(inst);
  const ctxDetail = contextDetail(inst);
  const output = outputDisplay(inst);

  return '<div class="card' + stale + '" data-pid="' + inst.pid + '">'
    + '<div class="card-inner">'
    + '<div class="card-row">'
    + '<span class="card-emoji">' + emoji + '</span>'
    + '<span class="card-pid">PID ' + inst.pid + '</span>'
    + '<span class="card-status ' + statusClass + '" data-field="status">' + label + '</span>'
    + '<span class="card-model" data-field="model">' + model + '</span>'
    + '<span class="card-duration" data-field="duration">' + humanDuration(inst.startedAt) + '</span>'
    + '</div>'
    + '<div class="card-row">'
    + '<span class="card-cwd" data-field="cwd">📁 ' + cwd + '</span>'
    + '</div>'
    + '<div class="card-row">'
    + '<span class="card-topic" data-field="topic">💬 ' + topic + '</span>'
    + '</div>'
    + '<div class="card-row card-context">'
    + '<span class="context-bar ' + contextBarClass(inst) + '" data-field="ctxBar">' + ctxBar + '</span>'
    + '<span class="context-pct" data-field="ctxPct">' + contextPct(inst) + '</span>'
    + '<span class="context-detail" data-field="ctxDetail">' + ctxDetail + '</span>'
    + '<span class="card-output" data-field="output">↑ ' + output + '</span>'
    + '</div>'
    + '</div>'
    + '<div class="card-actions">'
    + '<button class="action-btn" onclick="handleClear(' + inst.pid + ')">清空</button>'
    + '<button class="action-btn" onclick="handlePrompt(' + inst.pid + ')">对话</button>'
    + '<button class="action-btn" onclick="handleRewind(' + inst.pid + ')">回溯</button>'
    + '<button class="action-btn" onclick="handleShowWin(' + inst.pid + ')">窗口</button>'
    + '</div>'
    + '</div>';
}

function updateCardText(el, inst) {
  if (!el) return;
  const set = (sel, val) => { const e = el.querySelector(sel); if (e) e.textContent = val; };
  set("[data-field=status]", statusLabel(inst.status));
  set("[data-field=model]", modelDisplay(inst));
  set("[data-field=duration]", humanDuration(inst.startedAt));
  set("[data-field=cwd]", "📁 " + (inst.cwd || ""));
  set("[data-field=topic]", "💬 " + topicDisplay(inst));
  set("[data-field=ctxBar]", contextBar(inst));
  set("[data-field=ctxPct]", contextPct(inst));
  set("[data-field=ctxDetail]", contextDetail(inst));
  set("[data-field=output]", "↑ " + outputDisplay(inst));

  var statusEl = el.querySelector(".card-status");
  if (statusEl) statusEl.className = "card-status " + (inst.status || "unknown");
  var barEl = el.querySelector(".context-bar");
  if (barEl) barEl.className = "context-bar " + contextBarClass(inst);
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

function outputDisplay(inst) {
  if (!inst.hasConversation) return "（新）";
  return formatTokens(inst.outputTokens);
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

// ---- Prompt Modal ----
window.hidePromptModal = function() {
  document.getElementById("prompt-overlay").classList.add("hidden");
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
  var promptOverlay = document.getElementById("prompt-overlay");
  var settingsOverlay = document.getElementById("settings-overlay");

  if (!settingsOverlay.classList.contains("hidden") && e.key === "Escape") {
    hideSettings();
    return;
  }

  if (promptOverlay.classList.contains("hidden")) return;
  if (e.ctrlKey && e.key === "Enter") { sendPrompt(); }
  if (e.key === "Escape") { hidePromptModal(); }
});

// ---- Settings ----
window.showSettings = async function() {
  try {
    var s = await Call.ByID(ID_GET_SETTINGS);
    document.getElementById("toggle-close-quits").checked = s.closeQuits;
    document.getElementById("toggle-auto-start").checked = s.autoStart;
    document.getElementById("about-version").textContent = "版本 " + (s.version || "--");
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
  try {
    await Call.ByID(ID_SAVE_SETTINGS, closeQuits, autoStart);
    var label = key === "closeQuits" ? "关闭按钮行为" : "开机启动";
    flashFoot("✓  " + label + "已保存");
  } catch (e) {
    var el = document.getElementById(key === "closeQuits" ? "toggle-close-quits" : "toggle-auto-start");
    el.checked = !val;
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
