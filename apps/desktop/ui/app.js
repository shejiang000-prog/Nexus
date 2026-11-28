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
    if (link.hash === "#" + (page.dataset.nav || page.id)) { link.setAttribute("aria-current", "page"); current = link; }
    else link.removeAttribute("aria-current");
  }
  placePill(document.querySelector(".nav-pill"), current);
  if (focus) page.querySelector("h1").focus({preventScroll:true});
  document.querySelector("main").scrollTop = 0;
}
window.addEventListener("hashchange", () => { navigate(true); if (location.hash === "#servers") openServers(); });

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
  generic: "出了点问题，再试一次。",
  invalid_endpoint: "IP 格式不对。端口不是 22 的话，写成 IP:端口。",
  host_key_changed: "这台服务器的身份和之前不一样，为了安全已经停下。如果你刚重装过系统，先找服务商确认一下。",
  ssh_timeout: "连不上服务器。检查一下 IP 对不对，也可能是这个 IP 被墙了。",
  ssh_refused: "服务器拒绝连接，检查一下 SSH 端口。",
  ssh_auth: "root 密码不对。",
  ssh_failed: "和服务器的连接出了问题，再试一次。",
  not_installed: "这台服务器还没搭过节点。",
  unsupported_arch: "这台服务器的 CPU 类型不支持，换一台 x86 或 ARM 的 Linux 服务器。",
  node_download: "服务器下载节点程序失败，看看它能不能访问 GitHub。",
  node_checksum: "下载的节点程序没通过校验，已经停止安装。",
  install_port_occupied: "服务器上需要的端口被别的程序占了（通常是 443），先停掉它再试。",
  install_failed: "安装没成功，再试一次。",
  server_not_found: "找不到这台服务器的记录，重新添加一次。",
  ledger_failed: "服务器没有正常回应，再试一次。",
  owner_storage: "这台电脑上的服务器记录读不出来了。",
  owner_busy: "另一个 Nexus 窗口正在操作服务器，稍等一下。",
  enrollment_failed: "节点已经装好，但这台电脑没加入成功。再点一次「开始搭建」会接着完成。",
  cancelled: "已取消。",
  owner_failed: "没成功，再试一次。"
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

  if (!active) autoPick();
  const fromSubscription = source === "subscription";
  const selected = catalog.find(sub => sub.id === chosenSub);
  const node = selected?.nodes.find(item => item[0] === chosenNode);
  setText($("current-name"), fromSubscription ? (node ? node[1] : "选一个节点") : identity || !identityRead || active ? "私人节点" : "还没有节点");
  setText($("current-from"), fromSubscription ? (node ? selected.name : loaded ? "点这里挑一个" : "")
    : identity || (active ? "正在加入…" : identityRead ? "点这里添加订阅，或粘贴朋友的邀请" : ""));
  showLatency($("current-latency"), fromSubscription ? latency.get(chosenSub + ":" + chosenNode) : latency.get("v2"));
  $("enrollment").hidden = !identityRead || !!identity;
  $("pick-v2").hidden = !identity;
  document.querySelector('[data-probe="v2"]').hidden = !identity;
  $("join").disabled = active || busy || !invoke;
  setText($("v2-name"), "私人节点 · " + identity);

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

function pick(subId, nodeId) {
  source = subId ? "subscription" : "v2";
  localStorage.setItem("connectionSource", source);
  if (subId) {
    chosenSub = subId;
    chosenNode = nodeId;
    localStorage.setItem("chosenSubscription", chosenSub);
    localStorage.setItem("chosenNode", chosenNode);
  }
}
// Never leave the home card empty while a usable node exists.
function autoPick() {
  if (!loaded || !identityRead) return;
  const usable = catalog.filter(sub => sub.nodes.length);
  if (source === "subscription") {
    const sub = catalog.find(row => row.id === chosenSub);
    if (sub?.nodes.some(node => node[0] === chosenNode)) return;
    const next = sub?.nodes.length ? sub : usable[0];
    if (next) pick(next.id, next.nodes[0][0]);
    else if (identity) pick("", "");
  } else if (!identity && !invite.value.trim() && usable.length) {
    pick(usable[0].id, usable[0].nodes[0][0]);
  }
}
function choose(subId, nodeId) {
  pick(subId, nodeId);
  localError = "";
  render();
  setTimeout(() => { location.hash = "home"; }, reduceMotion ? 0 : 260);
}
$("pick-v2").addEventListener("click", () => choose("", ""));
$("enrollment").addEventListener("submit", event => {
  event.preventDefault();
  if ($("join").disabled || !invite.value.trim()) return;
  pick("", "");
  location.hash = "home";
  $("connection-form").requestSubmit();
});

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
    const done = {import_subscription:"添加好了",update_subscription:"已更新",remove_subscription:"已删除"}[action] || "";
    status.textContent = done;
    if (done) setTimeout(() => { if (status.textContent === done) status.textContent = ""; }, 2500);
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

// My servers: deploy a node with a root password, then manage who may use it.
let servers = [];
let serverId = "";
let ownerBusy = false;
let ownerOperation = 0;
let formKind = "deploy";
let devices = [];
let deviceTarget = null;
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const serverLabel = label => label.endsWith(":22") ? label.slice(0, -3) : label;
const chipValue = name => { const v = document.querySelector(`input[name="${name}"]:checked`)?.value; return v ? Number(v) : null; };

async function owner(fields, onPhase) {
  ownerBusy = true;
  serverControls();
  try {
    const id = await invoke("owner_start", fields);
    ownerOperation = id;
    for (;;) {
      await sleep(400);
      const {phase, error} = await invoke("owner_status", {operationId:id});
      onPhase?.(phase);
      if (phase === "done") return await invoke("owner_result", {operationId:id});
      if (phase === "failed" || phase === "cancelled") throw error || "owner_failed";
    }
  } finally {
    ownerOperation = 0;
    ownerBusy = false;
    serverControls();
  }
}

function serverFeedback(text, error) {
  const status = $("server-feedback");
  status.textContent = text;
  status.classList.toggle("error", !!error);
}
const ownerMessage = (err, managing) => err === "ssh_auth" && managing
  ? "服务器不认这台电脑的管理权了。点「添加服务器」，用 root 密码重新接管一次。"
  : messages[err] || messages.owner_failed;

function showServerView(view) {
  $("server-home").hidden = view !== "home";
  $("server-progress").hidden = view !== "progress";
  $("server-detail").hidden = view !== "detail";
  const server = servers.find(row => row.serverId === serverId);
  setText($("servers-title"), view === "detail" && server ? serverLabel(server.label) : "我的服务器");
  document.querySelector("main").scrollTop = 0;
}

function serverControls() {
  $("server-start").disabled = ownerBusy || !invoke;
  $("show-server-form").disabled = ownerBusy;
  $("make-invite").disabled = ownerBusy;
  $("devices-refresh").disabled = ownerBusy;
  for (const button of document.querySelectorAll("#server-list .server, #device-list .btn")) button.disabled = ownerBusy;
  $("servers-back").disabled = ownerBusy && !$("server-progress").hidden;
}

function setFormKind(kind) {
  formKind = kind;
  setText($("server-start"), kind === "bind" ? "接管" : "开始搭建");
}
function closeServerForm() {
  $("server-form").hidden = true;
  $("show-server-form").hidden = false;
  $("server-password").value = "";
  setFormKind("deploy");
}

function showServers() {
  const template = $("server-template").content.firstElementChild;
  const rows = servers.map((server, i) => {
    const row = template.cloneNode(true);
    row.querySelector("b").textContent = serverLabel(server.label);
    row.style.setProperty("--i", String(i));
    row.addEventListener("click", () => openServer(server.serverId));
    return row;
  });
  $("server-list").replaceChildren(...rows);
  $("server-list").hidden = !servers.length;
  $("server-empty").hidden = !!servers.length;
  setText($("servers-summary"), servers.length ? servers.length + " 台服务器 · 管理设备、邀请朋友" : "有一台 VPS 就行，点一下自动装好");
  setText($("to-servers").querySelector("b"), servers.length ? "我的服务器" : "用自己的服务器搭节点");
  serverControls();
}

async function loadServers() {
  if (!invoke || ownerBusy) return;
  try { servers = await owner({op:"list"}); }
  catch (err) { serverFeedback(ownerMessage(err), true); }
  showServers();
}

function openServers() {
  if (ownerBusy) return;
  serverFeedback("");
  showServerView("home");
  loadServers();
}

$("servers-back").addEventListener("click", () => {
  if (!$("server-home").hidden) location.hash = "nodes";
  else { serverFeedback(""); showServerView("home"); loadServers(); }
});
$("show-server-form").addEventListener("click", () => {
  $("server-form").hidden = false;
  $("show-server-form").hidden = true;
  serverFeedback("");
  $("server-address").focus();
});
$("server-form-cancel").addEventListener("click", () => { closeServerForm(); serverFeedback(""); showServers(); });

function progress(kind, phase) {
  const order = kind === "bind" ? ["connect", "enroll"] : ["connect", "install", "enroll"];
  // Deploy authorizes before installing and checks again after; "binding" keeps the current step.
  const at = {queued:"connect", connecting:"connect", binding: kind === "bind" ? "enroll" : null, installing:"install", enrolling:"enroll", done:"end"}[phase];
  if (at) {
    const index = at === "end" ? order.length : order.indexOf(at);
    for (const li of $("server-progress").querySelectorAll(".steps li")) {
      const i = order.indexOf(li.dataset.step);
      li.hidden = i < 0;
      li.className = i < index ? "done" : i === index ? "active" : "";
    }
  }
  setText($("progress-hint"), phase === "installing" ? "安装大约要 2 到 5 分钟，别关掉窗口" : "");
}

function startProgress(kind) {
  setText($("progress-title"), kind === "bind" ? "正在接管…" : "正在搭建…");
  setText($("server-progress").querySelector('[data-step="enroll"]'), kind === "bind" ? "接管管理权" : "让这台电脑加入");
  $("progress-cancel").hidden = false;
  $("progress-cancel").disabled = false;
  $("progress-invite").hidden = true;
  $("progress-connect").hidden = true;
  progress(kind, "queued");
  showServerView("progress");
}

async function addServer(kind, address, password) {
  serverFeedback("");
  startProgress(kind);
  try {
    const {pin} = await owner({op:"discover", address});
    const result = await owner({op:kind, address, pin, password}, phase => progress(kind, phase));
    password = "";
    serverId = result.serverId;
    $("server-address").value = "";
    closeServerForm();
    progress(kind, "done");
    $("progress-cancel").hidden = true;
    $("progress-invite").hidden = false;
    if (kind === "deploy") {
      setText($("progress-title"), "搭好了！这台电脑已经加入");
      source = "v2";
      localStorage.setItem("connectionSource", source);
      await readIdentity();
      render();
      $("progress-connect").hidden = false;
    } else setText($("progress-title"), "接管好了");
    servers = await owner({op:"list"}).catch(() => servers);
    showServers();
  } catch (err) {
    password = "";
    showServerView("home");
    if (err === "already_installed" && kind === "deploy") {
      $("confirm-bind").returnValue = "";
      $("confirm-bind").showModal();
      return;
    }
    serverFeedback(ownerMessage(err), err !== "cancelled");
  }
}

$("server-form").addEventListener("submit", event => {
  event.preventDefault();
  if ($("server-start").disabled) return;
  if (formKind === "deploy" && running(snapshot[0])) { serverFeedback("先在「连接」页断开，再开始搭建。", true); return; }
  const password = $("server-password").value;
  $("server-password").value = "";
  addServer(formKind, $("server-address").value.trim(), password);
});
$("confirm-bind").addEventListener("close", () => {
  if ($("confirm-bind").returnValue !== "bind") { closeServerForm(); showServers(); return; }
  // The password was cleared on purpose; ask once more rather than keep it in memory.
  setFormKind("bind");
  serverFeedback("再输一次 root 密码就能接管。");
  $("server-password").focus();
});
$("progress-cancel").addEventListener("click", async () => {
  if (!ownerOperation) return;
  $("progress-cancel").disabled = true;
  setText($("progress-title"), "正在取消…");
  try { await invoke("owner_cancel", {operationId:ownerOperation}); } catch {}
});
$("progress-connect").addEventListener("click", () => { location.hash = "home"; });
$("progress-invite").addEventListener("click", () => openServer(serverId));

function expiryText(device) {
  if (device.revoked) return "已停用";
  if (!device.expiryKnown) return "到期时间未知";
  if (device.expiresAt === null) return "永久可用";
  const end = new Date(device.expiresAt * 1000);
  if (end <= Date.now()) return "已过期";
  const days = Math.ceil((end - Date.now()) / 864e5);
  return end.toLocaleDateString("zh-CN") + " 到期" + (days <= 7 ? "，还剩 " + days + " 天" : "");
}

function showDevices() {
  const template = $("device-template").content.firstElementChild;
  const sorted = [...devices].sort((a, b) => a.revoked - b.revoked);
  const rows = sorted.map((device, i) => {
    const row = template.cloneNode(true);
    row.style.setProperty("--i", String(i));
    row.classList.toggle("revoked", device.revoked);
    row.querySelector("b").textContent = device.name;
    const small = row.querySelector("small");
    small.textContent = expiryText(device);
    small.classList.toggle("warn", !device.revoked && device.expiresAt !== null && device.expiresAt * 1000 - Date.now() < 7 * 864e5);
    row.querySelector(".renew").hidden = device.revoked;
    row.querySelector(".revoke").hidden = device.revoked;
    row.querySelector(".renew").addEventListener("click", () => {
      deviceTarget = device;
      setText($("renew-hint"), "「" + device.name + "」" + (device.expiresAt && device.expiresAt * 1000 > Date.now() ? "从到期那天往后加。" : "从今天开始算。"));
      $("confirm-renew").returnValue = "";
      $("confirm-renew").showModal();
    });
    row.querySelector(".revoke").addEventListener("click", () => {
      deviceTarget = device;
      setText($("revoke-hint"), "「" + device.name + "」会马上断开，以后也连不上了。想再用需要一条新的邀请。");
      $("confirm-revoke").returnValue = "";
      $("confirm-revoke").showModal();
    });
    return row;
  });
  if (!rows.length) {
    const empty = document.createElement("p");
    empty.className = "device-empty";
    empty.textContent = "还没有设备";
    rows.push(empty);
  }
  $("device-list").replaceChildren(...rows);
  const live = devices.filter(device => !device.revoked).length;
  setText($("device-meta"), live ? live + " 台" : "");
  serverControls();
}

async function loadDevices() {
  const button = $("devices-refresh");
  button.classList.add("spinning");
  try { devices = await owner({op:"devices", serverId}); showDevices(); }
  catch (err) { serverFeedback(ownerMessage(err, true), true); }
  finally { button.classList.remove("spinning"); }
}

function openServer(id) {
  serverId = id;
  serverFeedback("");
  $("invite-out").hidden = true;
  setText($("make-invite"), "生成邀请");
  $("make-invite").classList.add("primary");
  $("copy-invite").classList.remove("primary");
  devices = [];
  $("device-list").replaceChildren();
  setText($("device-meta"), "");
  showServerView("detail");
  loadDevices();
}
$("devices-refresh").addEventListener("click", loadDevices);

$("make-invite").addEventListener("click", async () => {
  serverFeedback("");
  setText($("make-invite"), "正在生成…");
  try {
    const {text} = await owner({op:"invite", serverId, days:chipValue("invite-days")});
    setText($("invite-text"), text);
    $("invite-out").hidden = false;
    setText($("make-invite"), "再生成一条");
    $("make-invite").classList.remove("primary");
    $("copy-invite").classList.add("primary");
    loadDevices();
  } catch (err) {
    setText($("make-invite"), "生成邀请");
    serverFeedback(ownerMessage(err, true), true);
  }
});
$("copy-invite").addEventListener("click", async () => {
  const label = $("copy-invite").lastChild;
  try {
    await navigator.clipboard.writeText($("invite-text").textContent);
    label.textContent = "已复制";
  } catch {
    getSelection().selectAllChildren($("invite-text"));
    label.textContent = "按 Ctrl+C";
  }
  setTimeout(() => { label.textContent = "复制"; }, 1800);
});

$("confirm-renew").addEventListener("close", async () => {
  if ($("confirm-renew").returnValue !== "renew" || !deviceTarget) return;
  const target = deviceTarget;
  try { await owner({op:"renew", serverId, accountId:target.accountId, days:chipValue("renew-days")}); serverFeedback("续好了"); }
  catch (err) { serverFeedback(ownerMessage(err, true), true); }
  loadDevices();
});
$("confirm-revoke").addEventListener("close", async () => {
  if ($("confirm-revoke").returnValue !== "revoke" || !deviceTarget) return;
  const target = deviceTarget;
  try { await owner({op:"revoke", serverId, deviceId:target.deviceId}); serverFeedback("已停用"); }
  catch (err) { serverFeedback(ownerMessage(err, true), true); }
  loadDevices();
});

navigate(false);
placeModePill();
// Pills are measured after fonts settle so their first position is exact.
document.fonts?.ready.then(() => { navigate(false); placeModePill(); });
softwareUpdate(true);
manage("subscriptions");
if (location.hash === "#servers") openServers(); else loadServers();
refresh();
