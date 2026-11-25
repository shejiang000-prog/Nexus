const $ = id => document.getElementById(id);
const tauri = window.__TAURI__;
const invoke = tauri?.core.invoke;
const reduceMotion = matchMedia("(prefers-reduced-motion: reduce)").matches;

// Frameless window controls.
const appWindow = tauri?.window?.getCurrentWindow?.();
$("win-min").addEventListener("click", () => appWindow?.minimize());
$("win-close").addEventListener("click", () => appWindow?.close());

// A sliding pill sits under the active choice of a segmented control.
function placePill(pill, target) {
  if (!target) return;
  pill.style.width = target.offsetWidth + "px";
  pill.style.transform = `translateX(${target.offsetLeft}px)`;
}

const pages = [...document.querySelectorAll("main section")];
function navigate(focus) {
  const page = pages.find(section => "#" + section.id === location.hash) || pages[0];
  for (const section of pages) section.hidden = section !== page;
  let current = null;
  for (const link of document.querySelectorAll("nav a")) {
    if (link.hash === "#" + page.id) { link.setAttribute("aria-current", "page"); current = link; }
    else link.removeAttribute("aria-current");
  }
  placePill(document.querySelector(".nav-pill"), current);
  if (focus) page.querySelector("h1").focus({preventScroll:true});
  document.querySelector("main").scrollTop = 0;
}
window.addEventListener("hashchange", () => navigate(true));

const messages = {
  invite_invalid: "这条邀请链接不完整，请重新复制一遍。",
  invite_rejected: "这条邀请已经用过、过期或被收回了，找分享的人再要一条吧。",
  node_unreachable: "连不上节点。可能是网络不通，或者节点的 IP 被墙了。",
  local_port: "本机端口被占满了，重启电脑后再试试。",
  key_missing: "这台电脑的密钥丢了。请在设置里退出私人节点，再用新邀请加入。",
  no_identity: "还没有私人节点，先粘贴一条邀请链接。",
  missing: "程序文件不完整，请重新安装 Nexus。",
  routing: "分流规则加载失败，请重新安装 Nexus。",
  busy: "先断开连接，再退出私人节点。",
  identity_changed: "节点信息刚变过，请再确认一次。",
  identity_busy: "还有连接在用这个节点，先断开再退出。",
  "proxy-busy": "另一个 Nexus 窗口正在管系统代理。",
  "proxy-changed": "系统代理被别的软件改了，已保留它的设置。",
  "proxy-error": "系统代理没设置成功。重新打开 Nexus 会自动恢复原来的设置。",
  subscription_download: "订阅下载失败，检查一下链接和网络再试。",
  subscription_format: "这个链接认不出来，或者订阅内容太大。",
  subscription_storage: "订阅没能保存，检查一下磁盘空间。",
  subscription_changed: "订阅刚更新过，请重新选一下节点。",
  tun_admin: "没拿到管理员授权。在弹出的提示里点「是」，或者在设置里关掉「接管全部流量」。",
  tun_driver: "虚拟网卡驱动加载失败，请重新安装 Nexus。",
  tun_conflict: "和别的 VPN 冲突了，先关掉它的 TUN 模式。",
  tun_session: "全部流量的通道断了，检查网络后重新连接。",
  tun_recovery: "上次的网络设置没能恢复，重新打开 Nexus 并允许恢复。",
  tun_version: "程序版本不一致，请重新安装 Nexus。",
  tun_recovering: "正在恢复上次的网络设置，稍等一下再连。",
  update_busy: "正在更新，稍等一下。",
  update_check: "暂时检查不了更新",
  update_download: "更新下载失败，检查网络后再试",
  update_signature: "更新包校验没通过，已取消安装",
  update_cleanup: "先断开连接、关掉其他 Nexus 窗口，再更新",
  update_install: "安装程序没能启动，再试一次",
  probe_unsupported: "这个版本还不支持测速。",
  probe_busy: "正在测速，稍等。",
  probe_failed: "测速没完成，再试一次。",
  generic: "出了点问题，再试一次。"
};
const protocolName = protocol => ({hysteria:"Hysteria2", vless:"VLESS", vmess:"VMess", trojan:"Trojan", shadowsocks:"Shadowsocks"})[protocol] || String(protocol).toUpperCase();
const modeInputs = [...document.querySelectorAll('input[name="mode"]')];
const mode = {
  get value() { return modeInputs.find(input => input.checked)?.value; },
  set value(next) { for (const input of modeInputs) input.checked = input.value === next; },
  set disabled(off) { for (const input of modeInputs) input.disabled = off; },
};
const placeModePill = () => placePill(document.querySelector(".seg-pill"), modeInputs.find(input => input.checked)?.parentElement);
const proxy = $("proxy");
const tun = $("tun");
const toggle = $("toggle");
const invite = $("invite");
const hero = $("hero");
proxy.checked = localStorage.getItem("systemProxy") !== "off";
mode.value = localStorage.getItem("routingMode") === "global" ? "global" : "split";
let source = localStorage.getItem("connectionSource") === "subscription" ? "subscription" : "v2";
let snapshot = ["stopped", 0, 0, false, ""];
let busy = false;
let revision = 0;
let identity = "";
let identityToken = "";
let identityRead = false;
let removingToken = "";
let localError = "";
let previous = null;
let chosenSub = localStorage.getItem("chosenSubscription") || "";
let chosenNode = localStorage.getItem("chosenNode") || "";
let catalog = [];
let subBusy = false;
let loaded = false;
let deletingId = "";
let lastLink = "idle";
const latency = new Map();
const connected = state => state === "h2" || state === "h3" || state === "subscription";
const running = state => connected(state) || state === "connecting" || state === "stopping";
const setText = (element, text) => { if (element.textContent !== text) element.textContent = text; };
function restart(element, className, duration) {
  if (reduceMotion) return;
  element.classList.remove(className);
  void element.offsetWidth;
  element.classList.add(className);
  setTimeout(() => element.classList.remove(className), duration);
}

let since = 0;
function clock() {
  const seconds = Math.floor((Date.now() - since) / 1000);
  return [seconds / 3600, seconds / 60 % 60, seconds % 60].map(n => String(Math.floor(n)).padStart(2, "0")).join(":");
}
setInterval(() => { if (since) setText($("clock"), clock()); }, 1000);

function render() {
  const [state, socks, http, enabled, notice, sessionTun] = snapshot;
  const ready = connected(state);
  const active = running(state);
  const status = ready ? "已连接" : ({stopped:"未连接", connecting:"连接中…", stopping:"断开中…"})[state] || "连接失败";
  if ($("status").textContent !== status) { $("status").textContent = status; restart($("status"), "swap", 400); }
  const error = localError || messages[state] || messages[notice] || "";
  setText($("feedback"), error);
  $("feedback").hidden = !error;
  const link = ready ? "on" : active ? "busy" : (state !== "stopped" || localError) ? "failed" : "idle";
  if (link !== lastLink) {
    if (link === "on") restart(hero, "burst", 1600);
    if (link === "failed") restart(hero, "shake", 500);
    lastLink = link;
  }
  hero.dataset.link = link;
  setText($("toggle-label"), active ? "断开" : "连接");
  setText($("lane"), ready ? ({h3:"主线路", h2:"备用线路"}[state] || "") : "");
  if (ready && !since) since = Date.now();
  if (!ready) since = 0;
  setText($("clock"), ready ? clock() : active ? "" : "点一下开始连接");
  $("traffic").hidden = !ready || !trafficSupported;

  const fromSubscription = source === "subscription";
  const selected = catalog.find(sub => sub.id === chosenSub);
  const node = selected?.nodes.find(item => item[0] === chosenNode);
  setText($("current-name"), fromSubscription ? (node ? node[1] : "选一个节点") : identity || !identityRead ? "私人节点" : "还没有节点");
  setText($("current-from"), fromSubscription ? (node ? selected.name : loaded ? "去「节点」页挑一个" : "")
    : identity || (identityRead ? "粘贴朋友发来的邀请，或去导入订阅" : ""));
  showLatency($("current-latency"), fromSubscription ? latency.get(chosenSub + ":" + chosenNode) : latency.get("v2"));
  $("enrollment").hidden = active || fromSubscription || !identityRead || !!identity;
  setText($("v2-name"), identity ? "私人节点 · " + identity : "还没有私人节点");

  if (active) {
    proxy.checked = enabled;
    tun.checked = sessionTun;
  }
  setText($("ports"), ready ? "SOCKS5 127.0.0.1:" + socks + "　HTTP 127.0.0.1:" + http : "连接后显示");
  tun.disabled = active || busy || !invoke;
  toggle.disabled = busy || state === "stopping" || !invoke || (!active && fromSubscription && (!node || subBusy));
  proxy.disabled = busy || state === "stopping" || !invoke;
  mode.disabled = active || busy;
  $("remove").hidden = !identity;
  $("remove").disabled = active || busy;
  nodeControls();
}

// Live traffic: the core reports cumulative bytes; the page derives rates.
let trafficSupported = true;
const history = [];
let lastSample = null;
const shown = {down:0, up:0};
function formatRate(bytesPerSecond) {
  const units = ["B/s", "KB/s", "MB/s", "GB/s"];
  let value = bytesPerSecond, index = 0;
  while (value >= 1024 && index < units.length - 1) { value /= 1024; index++; }
  return [value < 10 && index ? value.toFixed(1) : String(Math.round(value)), units[index]];
}
function drawSpark() {
  const points = history.slice(-40);
  const peak = Math.max(16 * 1024, ...points.map(p => Math.max(p.down, p.up)));
  // The newest sample sits at the right edge, so the line scrolls in from the right.
  const x = i => (300 - (points.length - 1 - i) * 300 / 39).toFixed(1);
  const path = key => points.map((p, i) => `${i ? "L" : "M"}${x(i)},${(42 - 38 * p[key] / peak).toFixed(1)}`).join("");
  const down = points.length > 1 ? path("down") : "";
  $("spark-down").setAttribute("d", down);
  $("spark-up").setAttribute("d", points.length > 1 ? path("up") : "");
  $("spark-down-fill").setAttribute("d", down ? down + `L300,44L${x(0)},44Z` : "");
}
function tweenRates(target) {
  const from = {...shown}, start = performance.now();
  const step = now => {
    const t = reduceMotion ? 1 : Math.min(1, (now - start) / 600);
    for (const key of ["down", "up"]) {
      shown[key] = from[key] + (target[key] - from[key]) * (1 - (1 - t) ** 3);
      const [value, unit] = formatRate(shown[key]);
      setText($("rate-" + key), value);
      setText($("unit-" + key), unit);
    }
    if (t < 1) requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
}
async function sampleTraffic() {
  if (!trafficSupported || !invoke || !connected(snapshot[0]) || document.hidden) {
    if (!connected(snapshot[0])) { history.length = 0; lastSample = null; }
    return;
  }
  try {
    const [up, down] = await invoke("traffic_stats");
    const now = performance.now();
    if (lastSample) {
      const seconds = Math.max(.25, (now - lastSample.time) / 1000);
      const rate = {down:Math.max(0, (down - lastSample.down) / seconds), up:Math.max(0, (up - lastSample.up) / seconds)};
      history.push(rate);
      if (history.length > 40) history.shift();
      drawSpark();
      tweenRates(rate);
    }
    lastSample = {time:now, up, down};
  } catch (err) {
    // A poll can race a disconnect; only a missing command disables the panel.
    history.length = 0;
    lastSample = null;
    if (err !== "not_connected") { trafficSupported = false; render(); }
  }
}
setInterval(sampleTraffic, 1000);

// Node latency.
function showLatency(element, value) {
  if (value === undefined) { element.hidden = true; return; }
  element.hidden = false;
  const tone = value === "testing" ? "testing" : value === null ? "fail" : value < 150 ? "good" : value < 300 ? "fair" : "slow";
  const text = value === "testing" ? "···" : value === null ? "超时" : value + " ms";
  if (element.textContent !== text || !element.classList.contains(tone)) {
    element.className = "latency " + tone;
    element.textContent = text;
  }
}
async function probe(button, target) {
  if (button.classList.contains("spinning") || !invoke) return;
  const keys = target.v2 ? ["v2"] : catalog.find(sub => sub.id === target.subscriptionId)?.nodes.map(node => target.subscriptionId + ":" + node[0]) || [];
  for (const key of keys) latency.set(key, "testing");
  button.classList.add("spinning");
  render();
  try {
    const results = await invoke("probe_nodes", target);
    for (const key of keys) latency.delete(key);
    for (const [id, ms] of results) latency.set(target.v2 ? "v2" : target.subscriptionId + ":" + id, ms);
  } catch (err) {
    for (const key of keys) latency.delete(key);
    const status = $("sub-feedback");
    status.textContent = messages[err] || messages.probe_unsupported;
    status.classList.add("error");
  } finally {
    button.classList.remove("spinning");
    render();
  }
}
document.querySelector('[data-probe="v2"]').addEventListener("click", event => probe(event.currentTarget, {v2:true}));

async function readIdentity() {
  try {
    [identity, identityToken] = await invoke("device_identity", {remove:false});
    setText($("identity"), identity ? "已加入私人节点 " + identity : "还没加入私人节点");
  } catch {
    identity = "";
    identityToken = "";
    setText($("identity"), "无法读取");
  }
  identityRead = true;
}

async function update() {
  const version = revision;
  const next = await invoke("core_status");
  if (version !== revision) return;
  snapshot = next;
  if (previous !== next[0]) {
    previous = next[0];
    if (next[0] !== "connecting" && next[0] !== "stopping") await readIdentity();
  }
  if (version === revision) render();
}

async function act(action) {
  if (busy) return;
  busy = true;
  revision++;
  localError = "";
  render();
  try { await action(); }
  catch (err) { localError = messages[err] || messages.generic; }
  try { await update(); }
  catch { localError = "Nexus 后台没有响应，请重新打开 Nexus。"; }
  busy = false;
  render();
}

$("connection-form").addEventListener("submit", event => {
  event.preventDefault();
  if (toggle.disabled) return;
  act(async () => {
    if (running(snapshot[0])) await invoke("stop_core");
    else {
      const selection = source === "subscription"
        ? {source:"subscription", subscriptionId:chosenSub, nodeId:chosenNode}
        : {source:"v2", invite:invite.value.trim()};
      await invoke("start_core", {...selection, mode:mode.value, proxy:proxy.checked, tun:tun.checked});
      if (selection.source === "v2") invite.value = "";
    }
  });
});
proxy.addEventListener("change", () => {
  const enabled = proxy.checked;
  act(async () => {
    await invoke("set_proxy", {enabled});
    proxy.checked = enabled;
    localStorage.setItem("systemProxy", enabled ? "on" : "off");
  });
});
for (const input of modeInputs) input.addEventListener("change", () => { localStorage.setItem("routingMode", mode.value); placeModePill(); });
$("remove").addEventListener("click", () => {
  removingToken = identityToken;
  $("confirm").returnValue = "";
  $("confirm").showModal();
});
$("confirm").addEventListener("close", () => {
  if ($("confirm").returnValue !== "remove") return;
  act(async () => {
    try { await invoke("device_identity", {remove:true, expected:removingToken}); }
    finally { await readIdentity(); }
  });
});

async function refresh() {
  if (!busy && !document.hidden) {
    try { await update(); }
    catch { localError = "Nexus 后台没有响应，请重新打开 Nexus。"; render(); }
  }
  setTimeout(refresh, 750);
}

function choose(subId, nodeId) {
  source = subId ? "subscription" : "v2";
  localStorage.setItem("connectionSource", source);
  if (subId) {
    chosenSub = subId;
    chosenNode = nodeId;
    localStorage.setItem("chosenSubscription", chosenSub);
    localStorage.setItem("chosenNode", chosenNode);
  }
  localError = "";
  render();
  setTimeout(() => { location.hash = "home"; }, reduceMotion ? 0 : 260);
}
$("pick-v2").addEventListener("click", () => choose("", ""));

function nodeControls() {
  const active = running(snapshot[0]);
  $("pick-v2").setAttribute("aria-checked", String(source === "v2"));
  $("pick-v2").disabled = active || busy;
  showLatency($("pick-v2").querySelector(".latency"), latency.get("v2"));
  for (const group of $("sub-groups").children) {
    const sub = catalog.find(row => row.id === group.dataset.id);
    const inUse = active && source === "subscription" && sub?.id === chosenSub;
    group.querySelector(".refresh").disabled = subBusy || !sub?.refreshable || inUse;
    group.querySelector(".delete").disabled = subBusy || inUse;
    for (const node of group.querySelectorAll(".node")) {
      node.setAttribute("aria-checked", String(source === "subscription" && sub?.id === chosenSub && node.dataset.id === chosenNode));
      node.disabled = subBusy || active || busy;
      showLatency(node.querySelector(".latency"), latency.get(sub?.id + ":" + node.dataset.id));
    }
  }
  $("subscription-url").disabled = subBusy;
  $("add-sub").disabled = subBusy || !invoke;
  setText($("selected-hint"), active ? "断开后才能换节点" : "");
}

function formatBytes(bytes) {
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let amount = bytes, index = 0;
  while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++; }
  return amount.toLocaleString("zh-CN", {maximumFractionDigits:1}) + " " + units[index];
}

function showGroups() {
  const groupTemplate = $("group-template").content.firstElementChild;
  const nodeTemplate = $("node-template").content.firstElementChild;
  let index = 0;
  const groups = catalog.map(sub => {
    const group = groupTemplate.cloneNode(true);
    group.dataset.id = sub.id;
    group.querySelector(".group-name").textContent = sub.name;
    const meta = [];
    let warn = false;
    if (sub.remaining !== null && sub.remaining >= 0) { meta.push("剩余 " + formatBytes(sub.remaining)); warn ||= sub.remaining < 1024 ** 3; }
    if (sub.expire > 0) {
      const end = new Date(sub.expire * 1000);
      if (!Number.isNaN(end.valueOf())) { meta.push(end.toLocaleDateString("zh-CN") + " 到期"); warn ||= end - Date.now() < 7 * 864e5; }
    }
    const metaElement = group.querySelector(".group-meta");
    metaElement.textContent = meta.join(" · ");
    metaElement.classList.toggle("warn", warn);
    group.querySelector(".group-nodes").setAttribute("aria-label", sub.name);
    group.querySelector(".probe").addEventListener("click", event => probe(event.currentTarget, {subscriptionId:sub.id}));
    group.querySelector(".refresh").addEventListener("click", () => manage("update_subscription", {id:sub.id}));
    group.querySelector(".delete").addEventListener("click", () => {
      deletingId = sub.id;
      $("delete-hint").textContent = "「" + sub.name + "」和里面的 " + sub.nodes.length + " 个节点都会从这台电脑删掉。";
      $("confirm-sub").returnValue = "";
      $("confirm-sub").showModal();
    });
    for (const [id, name, protocol] of sub.nodes) {
      const item = nodeTemplate.cloneNode(true);
      item.dataset.id = id;
      item.title = name;
      item.style.setProperty("--i", String(Math.min(index++, 14)));
      item.querySelector(".node-name").textContent = name;
      item.querySelector(".badge").textContent = protocolName(protocol);
      item.addEventListener("click", () => choose(sub.id, id));
      group.querySelector(".group-nodes").append(item);
    }
    return group;
  });
  $("sub-groups").replaceChildren(...groups);
  nodeControls();
}

async function manage(action, fields = {}) {
  if (subBusy) return;
  const status = $("sub-feedback");
  subBusy = true;
  status.classList.remove("error");
  status.textContent = {import_subscription:"正在添加…",update_subscription:"正在刷新…",remove_subscription:"正在删除…"}[action] || "";
  const refreshButton = action !== "update_subscription" ? null : document.querySelector(`#sub-groups [data-id="${CSS.escape(fields.id)}"] .refresh`);
  refreshButton?.classList.add("spinning");
  render();
  try {
    const rows = await invoke("subscriptions", {action, ...fields});
    catalog = rows.map(([id,name,refreshable,remaining,expire,nodes]) => ({id,name,refreshable,remaining,expire,nodes}));
    loaded = true;
    showGroups();
    if (action === "import_subscription") {
      $("subscription-url").value = "";
      $("import-form").hidden = true;
      $("show-import").hidden = false;
    }
    status.textContent = {import_subscription:"添加好了",update_subscription:"已更新",remove_subscription:"已删除"}[action] || "";
  } catch (err) {
    status.textContent = messages[err] || "没成功，再试一次。";
    status.classList.add("error");
    loaded = true;
  } finally {
    refreshButton?.classList.remove("spinning");
    subBusy = false;
    render();
  }
}

$("show-import").addEventListener("click", () => {
  $("import-form").hidden = false;
  $("show-import").hidden = true;
  $("subscription-url").focus();
});
$("import-form").addEventListener("submit", event => {
  event.preventDefault();
  if (!$("add-sub").disabled) manage("import_subscription", {url:$("subscription-url").value.trim()});
});
$("confirm-sub").addEventListener("close", () => {
  if ($("confirm-sub").returnValue === "delete") manage("remove_subscription", {id:deletingId});
});

const updateAction = $("update-action");
const updateFeedback = $("update-feedback");
let availableVersion = "";
async function softwareUpdate(automatic) {
  if (!invoke) return;
  updateAction.disabled = true;
  try {
    if (availableVersion) {
      updateFeedback.textContent = "正在下载更新…";
      await invoke("install_update");
    } else {
      if (!automatic) updateFeedback.textContent = "正在检查…";
      availableVersion = await invoke("check_update") || "";
      if (availableVersion) {
        updateAction.textContent = "安装";
        updateAction.classList.add("primary");
        updateFeedback.textContent = "有新版本 " + availableVersion + "，安装时会先断开连接";
      } else updateFeedback.textContent = "已是最新版本";
    }
  } catch (err) {
    updateFeedback.textContent = messages[err] || messages.update_check;
    if (err === "update_signature" || err === "update_check") {
      availableVersion = "";
      updateAction.textContent = "检查更新";
      updateAction.classList.remove("primary");
    }
  } finally {
    updateAction.disabled = false;
  }
}
updateAction.addEventListener("click", () => softwareUpdate(false));

navigate(false);
placeModePill();
// Pills are measured after fonts settle so their first position is exact.
document.fonts?.ready.then(() => { navigate(false); placeModePill(); });
softwareUpdate(true);
manage("subscriptions");
refresh();
