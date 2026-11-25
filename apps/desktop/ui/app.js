const $ = id => document.getElementById(id);
const pages = [...document.querySelectorAll("main section")];
function navigate(focus) {
  const page = pages.find(section => "#" + section.id === location.hash) || pages[0];
  for (const section of pages) section.hidden = section !== page;
  for (const link of document.querySelectorAll("nav a")) {
    if (link.hash === "#" + page.id) link.setAttribute("aria-current", "page");
    else link.removeAttribute("aria-current");
  }
  if (focus) page.querySelector("h1").focus();
}
window.addEventListener("hashchange", () => navigate(true));
navigate(false);

const invoke = window.__TAURI__?.core.invoke;
const messages = {
  invite_invalid: "邀请链接格式无效，请重新粘贴完整链接。",
  invite_rejected: "邀请已被领取、已过期或已被撤销，请向分享人要一条新的邀请。",
  node_unreachable: "无法连接节点，请检查网络和节点状态。节点 IP 可能被屏蔽，或被防火墙拦截。",
  local_port: "本机代理端口无法监听，请检查端口占用或本机权限。",
  key_missing: "本机设备密钥已丢失，请移除此设备身份并用新邀请登记。",
  no_identity: "本机尚未登记，请粘贴 V2 邀请链接。",
  missing: "找不到 nexus-core.exe，请将它与桌面程序放在同一目录。",
  routing: "分流规则无法加载，请检查核心程序。",
  busy: "请先断开连接，再移除此设备身份。",
  identity_changed: "当前已登记节点已更改，请确认节点后再移除。",
  identity_busy: "请先断开所有正在使用此设备身份的连接，再移除。",
  "proxy-busy": "另一个 Nexus 窗口正在管理系统代理。",
  "proxy-changed": "系统代理已由其他程序修改，已保留当前设置。",
  "proxy-error": "系统代理设置或恢复失败。请检查权限，必要时重新启动 Nexus 以重试恢复。",
  subscription_download: "订阅下载失败，请检查链接和网络，然后重试。",
  subscription_format: "链接或订阅格式不受支持，或内容超出限制。",
  subscription_storage: "无法读取或保存订阅，请检查本机用户资料。",
  subscription_changed: "订阅或节点已变更，请重新选择。",
  tun_admin: "未获得管理员权限。请在 Windows 授权提示中选择允许，或关闭 TUN 模式后连接。",
  tun_driver: "无法加载 Wintun 驱动。请检查安装包中的驱动文件后重试。",
  tun_conflict: "TUN 与现有虚拟网卡或路由冲突。请先关闭其他应用的 TUN 模式，再重试。",
  tun_session: "TUN 数据通道已中断，请检查网络或其他 VPN 的状态后重新连接。",
  tun_recovery: "未能恢复上次 TUN 会话的网络设置。请重新打开 Nexus 并允许恢复。",
  tun_version: "核心程序未确认支持 TUN。请使用与桌面程序同一版本的 nexus-core.exe 后重试。",
  tun_recovering: "正在检查上次 TUN 会话，请稍后再连接。",
  generic: "操作失败，请检查本机设备记录和节点授权。"
};
const proxy = $("proxy");
const mode = $("mode");
const tun = $("tun");
const toggle = $("toggle");
const invite = $("invite");
proxy.checked = localStorage.getItem("systemProxy") !== "off";
mode.value = localStorage.getItem("routingMode") === "global" ? "global" : "split";
let snapshot = ["stopped", 0, 0, false, ""];
let busy = false;
let revision = 0;
let identity = "";
let identityToken = "";
let removingToken = "";
let localError = "";
let previous = null;
const source = $("source");
source.value = localStorage.getItem("connectionSource") === "subscription" ? "subscription" : "v2";
let chosenSub = localStorage.getItem("chosenSubscription") || "";
let chosenNode = localStorage.getItem("chosenNode") || "";
let catalog = [];
let subBusy = false;
let loaded = false;
let deletingId = "";
const connected = state => state === "h2" || state === "h3" || state === "subscription";
const running = state => connected(state) || state === "connecting" || state === "stopping";

function render() {
  const [state, socks, http, enabled, notice, sessionTun] = snapshot;
  const ready = connected(state);
  const active = running(state);
  const label = state === "subscription" ? "代理已就绪" : ready ? "已连接" : ({stopped:"未连接", connecting:"连接中", stopping:"正在断开"})[state] || "连接失败";
  if ($("status").textContent !== label) $("status").textContent = label;
  const error = localError || messages[state] || messages[notice] || "";
  if ($("feedback").textContent !== error) $("feedback").textContent = error;
  $("feedback").hidden = !error;
  const fromSubscription = source.value === "subscription";
  const selected = catalog.find(sub => sub.id === chosenSub);
  const node = selected?.nodes.find(item => item[0] === chosenNode);
  source.disabled = active || busy;
  $("enrollment").hidden = active || fromSubscription;
  $("route").textContent = state === "subscription" ? "当前节点 · " + (node?.[1] || "订阅节点") : ready ? "当前线路 · " + state.toUpperCase() : "";
  $("selected-sub").hidden = !fromSubscription || active;
  $("selected-node").textContent = node ? selected.name + " · " + node[1] : loaded ? "尚未选择节点" : "正在读取订阅…";
  $("ports").hidden = !ready;
  if (ready) {
    $("socks").textContent = "127.0.0.1:" + socks;
    $("http").textContent = "127.0.0.1:" + http;
  }
  if (active) {
    proxy.checked = enabled;
    tun.checked = sessionTun;
  }
  tun.disabled = active || busy || !invoke;
  toggle.textContent = active ? "断开" : "连接";
  toggle.disabled = busy || state === "stopping" || !invoke || (!active && fromSubscription && (!node || subBusy));
  subControls();
  proxy.disabled = busy || state === "stopping" || !invoke;
  mode.disabled = active || busy;
  $("mode-hint").hidden = !active;
  $("remove").hidden = !identity;
  $("remove").disabled = active || busy;
}

async function readIdentity() {
  try {
    [identity, identityToken] = await invoke("device_identity", {remove:false});
    $("identity").textContent = identity || "尚未登记";
  } catch {
    identity = "";
    identityToken = "";
    $("identity").textContent = "无法读取本机身份";
  }
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
      const selection = source.value === "subscription"
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
mode.addEventListener("change", () => localStorage.setItem("routingMode", mode.value));
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
function displayed() {
  return catalog.find(row => row.id === $("sub-list").value);
}

function subControls() {
  const sub = displayed();
  const active = running(snapshot[0]);
  const inUse = active && source.value === "subscription" && sub?.id === chosenSub;
  $("subscription-url").disabled = subBusy;
  $("add-sub").disabled = subBusy || !invoke;
  $("sub-list").disabled = subBusy;
  $("node-list").disabled = subBusy || !sub?.nodes.length;
  $("refresh-sub").disabled = subBusy || !sub?.refreshable || inUse;
  $("delete-sub").disabled = subBusy || !sub || inUse;
  $("use-node").disabled = subBusy || active || busy || !$("node-list").value;
  $("selected-hint").textContent = active ? "断开连接后可切换节点。" :
    sub && sub.id === chosenSub && $("node-list").value === chosenNode && source.value === "subscription" ? "首页当前使用此节点" : "";
}

function showNodes(preferred) {
  const sub = displayed();
  const nodes = $("node-list");
  nodes.replaceChildren();
  if (sub) {
    const meta = [sub.nodes.length + " 个节点"];
    if (sub.remaining !== null && sub.remaining >= 0) {
      const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
      let amount = sub.remaining, index = 0;
      while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++; }
      meta.push("剩余 " + amount.toLocaleString("zh-CN", {maximumFractionDigits:2}) + " " + units[index]);
    }
    if (sub.expire > 0) {
      const end = new Date(sub.expire * 1000);
      if (!Number.isNaN(end.valueOf())) meta.push("到期 " + end.toLocaleDateString("zh-CN"));
    }
    $("sub-meta").textContent = meta.join("　·　");
    const fragment = document.createDocumentFragment();
    for (const [id, name, protocol] of sub.nodes) fragment.append(new Option(name + "　·　" + (protocol === "hysteria" ? "Hysteria2" : protocol.toUpperCase()), id));
    nodes.size = Math.max(2, Math.min(6, sub.nodes.length));
    nodes.append(fragment);
    const desired = preferred || (sub.id === chosenSub ? chosenNode : "");
    if (sub.nodes.some(node => node[0] === desired)) nodes.value = desired;
    else if (nodes.length) nodes.selectedIndex = 0;
  }
  subControls();
}

function setCatalog(rows, action) {
  const previousIds = new Set(catalog.map(s => s.id));
  const prev = displayed()?.id;
  const prevNode = $("node-list").value;
  catalog = rows.map(([id,name,refreshable,remaining,expire,nodes]) => ({id,name,refreshable,remaining,expire,nodes}));
  loaded = true;
  const list = $("sub-list");
  list.replaceChildren(...catalog.map(s => new Option(s.name, s.id)));
  const added = action === "import_subscription" && catalog.find(s => !previousIds.has(s.id));
  const next = added || catalog.find(s => s.id === prev) || catalog.find(s => s.id === chosenSub) || catalog[0];
  if (next) list.value = next.id;
  $("sub-details").hidden = !catalog.length;
  $("sub-empty").hidden = !!catalog.length;
  $("sub-empty").textContent = "尚未添加订阅或节点。";
  showNodes(next?.id === prev ? prevNode : "");
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
    setCatalog(rows, action);
    if (action === "import_subscription") $("subscription-url").value = "";
    status.textContent = {import_subscription:"已添加。",update_subscription:"订阅已刷新。",remove_subscription:"已删除。"}[action] || "";
  } catch (err) {
    status.textContent = messages[err] || "订阅操作失败，请重试。";
    status.classList.add("error");
    if (!loaded) { $("sub-empty").textContent = "订阅列表暂不可用。"; loaded = true; }
  } finally {
    subBusy = false;
    render();
  }
}

source.addEventListener("change", () => {
  localStorage.setItem("connectionSource", source.value);
  localError = "";
  render();
});
$("import-form").addEventListener("submit", event => {
  event.preventDefault();
  if (!$("add-sub").disabled) manage("import_subscription", {url:$("subscription-url").value.trim()});
});
$("sub-list").addEventListener("change", () => showNodes(""));
$("node-list").addEventListener("change", subControls);
$("refresh-sub").addEventListener("click", () => manage("update_subscription", {id:displayed().id}));
$("delete-sub").addEventListener("click", () => {
  const sub = displayed();
  deletingId = sub.id;
  $("delete-hint").textContent = "将从本机删除「" + sub.name + "」及其节点。";
  $("confirm-sub").returnValue = "";
  $("confirm-sub").showModal();
});
$("confirm-sub").addEventListener("close", () => {
  if ($("confirm-sub").returnValue === "delete") manage("remove_subscription", {id:deletingId});
});
$("use-node").addEventListener("click", () => {
  chosenSub = displayed().id;
  chosenNode = $("node-list").value;
  source.value = "subscription";
  localStorage.setItem("chosenSubscription", chosenSub);
  localStorage.setItem("chosenNode", chosenNode);
  localStorage.setItem("connectionSource", source.value);
  localError = "";
  location.hash = "home";
  render();
});
manage("subscriptions");
refresh();
