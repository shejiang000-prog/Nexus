const $ = id => document.getElementById(id);
const pages = [...document.querySelectorAll("main section")];
function navigate(focus) {
  const page = pages.find(section => "#" + section.id === location.hash) || pages[0];
  for (const section of pages) section.hidden = section !== page;
  for (const link of document.querySelectorAll("nav a")) {
    if (link.hash === "#" + page.id) link.setAttribute("aria-current", "page");
    else link.removeAttribute("aria-current");
  }
  if (focus) page.querySelector("h1").focus({preventScroll:true});
  window.scrollTo(0, 0);
}
window.addEventListener("hashchange", () => navigate(true));
navigate(false);

const invoke = window.__TAURI__?.core.invoke;
const messages = {
  invite_invalid: "邀请链接格式无效，请重新粘贴完整链接。",
  invite_rejected: "邀请已被领取、已过期或已被撤销，请向分享人要一条新的邀请。",
  node_unreachable: "无法连接节点，请检查网络和节点状态。节点 IP 可能被屏蔽，或被防火墙拦截。",
  local_port: "本机代理端口无法监听，请检查端口占用或本机权限。",
  key_missing: "本机设备密钥已丢失，请在设置里移除设备身份，再用新邀请登记。",
  no_identity: "本机尚未登记，请先粘贴 V2 邀请链接。",
  missing: "找不到 nexus-core.exe，请重新安装 Nexus。",
  routing: "分流规则无法加载，请重新安装 Nexus。",
  busy: "请先断开连接，再移除设备身份。",
  identity_changed: "已登记的节点已更改，请确认后再移除。",
  identity_busy: "请先断开所有正在使用此设备身份的连接，再移除。",
  "proxy-busy": "另一个 Nexus 窗口正在管理系统代理。",
  "proxy-changed": "系统代理已由其他程序修改，已保留当前设置。",
  "proxy-error": "系统代理设置或恢复失败。请检查权限，必要时重新启动 Nexus 以重试恢复。",
  subscription_download: "订阅下载失败，请检查链接和网络，然后重试。",
  subscription_format: "链接或订阅格式不受支持，或内容超出限制。",
  subscription_storage: "无法读取或保存订阅，请检查本机用户资料。",
  subscription_changed: "订阅或节点已变更，请重新选择。",
  tun_admin: "未获得管理员权限。请在 Windows 授权提示中选择允许，或在设置里关闭 TUN 模式。",
  tun_driver: "无法加载 Wintun 驱动，请重新安装 Nexus。",
  tun_conflict: "TUN 与其他应用的虚拟网卡或路由冲突，请先关闭其他应用的 TUN 模式。",
  tun_session: "TUN 数据通道已中断，请检查网络或其他 VPN 后重新连接。",
  tun_recovery: "未能恢复上次 TUN 会话的网络设置。请重新打开 Nexus 并允许恢复。",
  tun_version: "核心程序版本不匹配，请重新安装 Nexus。",
  tun_recovering: "正在检查上次 TUN 会话，请稍后再连接。",
  update_busy: "正在处理软件更新，请稍后再试。",
  update_check: "暂时无法检查更新。",
  update_download: "更新下载失败，请检查网络后重试。",
  update_signature: "更新包签名验证失败，已取消安装。",
  update_cleanup: "网络设置尚未恢复。请先断开连接并关闭其他 Nexus 窗口。",
  update_install: "无法启动安装程序，请重试。",
  generic: "操作失败，请检查本机设备记录和节点授权。"
};
const protocolName = protocol => ({hysteria:"Hysteria2", vless:"VLESS", vmess:"VMess", trojan:"Trojan", shadowsocks:"Shadowsocks"})[protocol] || String(protocol).toUpperCase();
const modeInputs = [...document.querySelectorAll('input[name="mode"]')];
const mode = {
  get value() { return modeInputs.find(input => input.checked)?.value; },
  set value(next) { for (const input of modeInputs) input.checked = input.value === next; },
  set disabled(off) { for (const input of modeInputs) input.disabled = off; },
};
const proxy = $("proxy");
const tun = $("tun");
const toggle = $("toggle");
const invite = $("invite");
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
const connected = state => state === "h2" || state === "h3" || state === "subscription";
const running = state => connected(state) || state === "connecting" || state === "stopping";
const setText = (element, text) => { if (element.textContent !== text) element.textContent = text; };
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
  setText($("status"), ready ? "已连接" : ({stopped:"未连接", connecting:"正在连接", stopping:"正在断开"})[state] || "连接失败");
  const error = localError || messages[state] || messages[notice] || "";
  setText($("feedback"), error);
  $("feedback").hidden = !error;
  $("hero").dataset.link = ready ? "on" : active ? "busy" : (state !== "stopped" || localError) ? "failed" : "idle";
  setText($("toggle-label"), active ? "断开" : "连接");
  const lane = {h3:"HTTP/3", h2:"HTTP/2"}[state] || "";
  setText($("lane"), ready ? lane || (sessionTun ? "TUN" : "代理") : "");
  if (ready && !since) since = Date.now();
  if (!ready) since = 0;
  setText($("clock"), ready ? clock() : active ? "" : "点击圆环连接");

  const fromSubscription = source === "subscription";
  const selected = catalog.find(sub => sub.id === chosenSub);
  const node = selected?.nodes.find(item => item[0] === chosenNode);
  setText($("current-name"), fromSubscription ? (node ? node[1] : "选择节点") : "我的 V2 节点");
  setText($("current-from"), fromSubscription ? (node ? selected.name + " · " + protocolName(node[2]) : loaded ? "还没有选中的订阅节点" : "")
    : identity || (identityRead ? "尚未登记" : ""));
  $("enrollment").hidden = active || fromSubscription || !identityRead || !!identity;
  setText($("v2-name"), identity ? "我的 V2 节点 · " + identity : "我的 V2 节点（粘贴邀请后连接）");

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

async function readIdentity() {
  try {
    [identity, identityToken] = await invoke("device_identity", {remove:false});
    setText($("identity"), identity || "尚未登记");
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
  catch { localError = "无法读取核心状态。"; }
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
for (const input of modeInputs) input.addEventListener("change", () => localStorage.setItem("routingMode", mode.value));
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
    catch { localError = "无法读取核心状态。"; render(); }
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
  location.hash = "home";
  render();
}
$("pick-v2").addEventListener("click", () => choose("", ""));

function nodeControls() {
  const active = running(snapshot[0]);
  $("pick-v2").setAttribute("aria-checked", String(source === "v2"));
  $("pick-v2").disabled = active || busy;
  for (const group of $("sub-groups").children) {
    const sub = catalog.find(row => row.id === group.dataset.id);
    const inUse = active && source === "subscription" && sub?.id === chosenSub;
    group.querySelector(".refresh").disabled = subBusy || !sub?.refreshable || inUse;
    group.querySelector(".delete").disabled = subBusy || inUse;
    for (const node of group.querySelectorAll(".node")) {
      node.setAttribute("aria-checked", String(source === "subscription" && sub?.id === chosenSub && node.dataset.id === chosenNode));
      node.disabled = subBusy || active || busy;
    }
  }
  $("subscription-url").disabled = subBusy;
  $("add-sub").disabled = subBusy || !invoke;
  setText($("selected-hint"), active ? "断开连接后可切换节点。" : "");
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
    group.querySelector(".refresh").addEventListener("click", () => manage("update_subscription", {id:sub.id}));
    group.querySelector(".delete").addEventListener("click", () => {
      deletingId = sub.id;
      $("delete-hint").textContent = "将从本机删除「" + sub.name + "」及其 " + sub.nodes.length + " 个节点。";
      $("confirm-sub").returnValue = "";
      $("confirm-sub").showModal();
    });
    for (const [id, name, protocol] of sub.nodes) {
      const item = nodeTemplate.cloneNode(true);
      item.dataset.id = id;
      item.title = name;
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
    status.textContent = {import_subscription:"已添加。",update_subscription:"已刷新。",remove_subscription:"已删除。"}[action] || "";
  } catch (err) {
    status.textContent = messages[err] || "订阅操作失败，请重试。";
    status.classList.add("error");
    loaded = true;
  } finally {
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
      updateFeedback.textContent = "正在下载并验证更新…";
      await invoke("install_update");
    } else {
      if (!automatic) updateFeedback.textContent = "正在检查…";
      availableVersion = await invoke("check_update") || "";
      if (availableVersion) {
        updateAction.textContent = "安装";
        updateAction.classList.add("primary");
        updateFeedback.textContent = "可更新至 " + availableVersion + "，安装前会先断开连接";
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
softwareUpdate(true);
manage("subscriptions");
refresh();
