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

// A segmented control behaves like the select element it replaced.
function radios(name) {
  const inputs = [...document.querySelectorAll(`input[name="${name}"]`)];
  return {
    get value() { return inputs.find(input => input.checked)?.value; },
    set value(next) { for (const input of inputs) input.checked = input.value === next; },
    set disabled(off) { for (const input of inputs) input.disabled = off; },
    addEventListener(type, handler) { for (const input of inputs) input.addEventListener(type, handler); },
  };
}

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
  update_busy: "正在处理软件更新，请稍后再试。",
  update_check: "暂时无法检查更新，请稍后重试。",
  update_download: "更新下载失败，请检查网络后重试。",
  update_signature: "更新包签名验证失败，已取消安装。",
  update_cleanup: "网络设置尚未恢复。请先断开连接并关闭其他 Nexus 窗口，再重试更新。",
  update_install: "无法启动安装程序，请重试。",
  generic: "操作失败，请检查本机设备记录和节点授权。"
};
const modeText = {
  split: "国内网站和局域网直连，其余流量经节点。",
  global: "除局域网外，所有流量都经节点。",
};
const protocolName = protocol => ({hysteria:"Hysteria2", vless:"VLESS", vmess:"VMess", trojan:"Trojan", shadowsocks:"Shadowsocks"})[protocol] || String(protocol).toUpperCase();

const proxy = $("proxy");
const mode = radios("mode");
const tun = $("tun");
const toggle = $("toggle");
const invite = $("invite");
const source = radios("source");
proxy.checked = localStorage.getItem("systemProxy") !== "off";
mode.value = localStorage.getItem("routingMode") === "global" ? "global" : "split";
source.value = localStorage.getItem("connectionSource") === "subscription" ? "subscription" : "v2";
let snapshot = ["stopped", 0, 0, false, ""];
let busy = false;
let revision = 0;
let identity = "";
let identityToken = "";
let removingToken = "";
let localError = "";
let previous = null;
let chosenSub = localStorage.getItem("chosenSubscription") || "";
let chosenNode = localStorage.getItem("chosenNode") || "";
let catalog = [];
let shownSub = "";
let subBusy = false;
let loaded = false;
let deletingId = "";
const connected = state => state === "h2" || state === "h3" || state === "subscription";
const running = state => connected(state) || state === "connecting" || state === "stopping";
const setText = (element, text) => { if (element.textContent !== text) element.textContent = text; };

function render() {
  const [state, socks, http, enabled, notice, sessionTun] = snapshot;
  const ready = connected(state);
  const active = running(state);
  const label = state === "subscription" ? "代理已就绪" : ready ? "已连接" : ({stopped:"未连接", connecting:"正在连接", stopping:"正在断开"})[state] || "连接失败";
  setText($("status"), label);
  const error = localError || messages[state] || messages[notice] || "";
  setText($("feedback"), error);
  $("feedback").hidden = !error;
  toggle.dataset.link = ready ? "on" : active ? "busy" : (state !== "stopped" || localError) ? "failed" : "idle";

  const fromSubscription = source.value === "subscription";
  const selected = catalog.find(sub => sub.id === chosenSub);
  const node = selected?.nodes.find(item => item[0] === chosenNode);
  const lane = {h3:"HTTP/3", h2:"HTTP/2"}[state];
  const modeName = mode.value === "global" ? "全局代理" : "绕过大陆";
  setText($("route"), state === "subscription" ? (node?.[1] || "订阅节点") + " · " + modeName
    : lane ? "V2 · " + lane + " · " + modeName : "");
  setText($("toggle-hint"), ready ? "点击圆环断开" : active ? "" : "点击圆环连接");
  setText($("toggle-label"), active ? "断开" : "连接");

  source.disabled = active || busy;
  $("enrollment").hidden = active || fromSubscription;
  $("selected-sub").hidden = !fromSubscription;
  $("selected-sub").classList.toggle("empty-pick", !node);
  setText($("selected-node"), node ? node[1] : loaded ? "尚未选择节点" : "正在读取订阅…");
  setText($("selected-from"), node ? selected.name + " · " + protocolName(node[2]) : "前往「我的订阅」选择一个节点");
  $("ports").hidden = !ready;
  if (ready) {
    setText($("socks"), "127.0.0.1:" + socks);
    setText($("http"), "127.0.0.1:" + http);
  }
  if (active) {
    proxy.checked = enabled;
    tun.checked = sessionTun;
  }
  tun.disabled = active || busy || !invoke;
  toggle.disabled = busy || state === "stopping" || !invoke || (!active && fromSubscription && (!node || subBusy));
  proxy.disabled = busy || state === "stopping" || !invoke;
  mode.disabled = active || busy;
  $("mode-hint").hidden = !active;
  setText($("mode-desc"), modeText[mode.value]);
  $("remove").hidden = !identity;
  $("remove").disabled = active || busy;
  subControls();
}

async function readIdentity() {
  try {
    [identity, identityToken] = await invoke("device_identity", {remove:false});
    setText($("identity"), identity || "尚未登记");
  } catch {
    identity = "";
    identityToken = "";
    setText($("identity"), "无法读取本机身份");
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
mode.addEventListener("change", () => {
  localStorage.setItem("routingMode", mode.value);
  render();
});
source.addEventListener("change", () => {
  localStorage.setItem("connectionSource", source.value);
  localError = "";
  render();
});
for (const button of document.querySelectorAll(".copy")) {
  button.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText($(button.dataset.copy).textContent);
      button.classList.add("done");
      button.querySelector("use").setAttribute("href", "#i-check");
      setTimeout(() => {
        button.classList.remove("done");
        button.querySelector("use").setAttribute("href", "#i-copy");
      }, 1400);
    } catch { /* The address stays visible for manual copying. */ }
  });
}
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

const displayed = () => catalog.find(row => row.id === shownSub);

function subControls() {
  const sub = displayed();
  const active = running(snapshot[0]);
  const inUse = active && source.value === "subscription" && sub?.id === chosenSub;
  $("subscription-url").disabled = subBusy;
  $("add-sub").disabled = subBusy || !invoke;
  for (const tab of $("sub-list").children) tab.disabled = subBusy;
  for (const node of $("node-list").children) node.disabled = subBusy || active || busy;
  $("refresh-sub").disabled = subBusy || !sub?.refreshable || inUse;
  $("delete-sub").disabled = subBusy || !sub || inUse;
  const hint = $("selected-hint");
  if (active) setText(hint, "断开连接后可切换节点。");
  else if (sub && sub.id === chosenSub && source.value === "subscription" && sub.nodes.some(node => node[0] === chosenNode)) {
    if (!hint.querySelector("a")) {
      const link = Object.assign(document.createElement("a"), {href:"#home", textContent:"回首页连接"});
      hint.replaceChildren("已选用，", link);
    }
  } else setText(hint, "点击节点即可选用。");
}

function formatBytes(bytes) {
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let amount = bytes, index = 0;
  while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++; }
  return amount.toLocaleString("zh-CN", {maximumFractionDigits:2}) + " " + units[index];
}

function showTabs() {
  const tabs = catalog.map(sub => {
    const tab = document.createElement("button");
    tab.type = "button";
    tab.className = "tab";
    tab.setAttribute("role", "tab");
    tab.setAttribute("aria-selected", String(sub.id === shownSub));
    tab.tabIndex = sub.id === shownSub ? 0 : -1;
    const name = Object.assign(document.createElement("span"), {textContent:sub.name});
    const count = Object.assign(document.createElement("span"), {className:"count", textContent:String(sub.nodes.length)});
    tab.append(name, count);
    tab.addEventListener("click", () => { shownSub = sub.id; showTabs(); showNodes(); });
    return tab;
  });
  $("sub-list").replaceChildren(...tabs);
}

function showNodes() {
  const sub = displayed();
  const list = $("node-list");
  list.replaceChildren();
  if (sub) {
    setText($("sub-name"), sub.name);
    const meta = [[sub.nodes.length + " 个节点", ""]];
    if (sub.remaining !== null && sub.remaining >= 0) meta.push(["剩余 " + formatBytes(sub.remaining), sub.remaining < 1024 ** 3 ? "warn" : ""]);
    if (sub.expire > 0) {
      const end = new Date(sub.expire * 1000);
      if (!Number.isNaN(end.valueOf())) meta.push(["到期 " + end.toLocaleDateString("zh-CN"), end - Date.now() < 7 * 864e5 ? "warn" : ""]);
    }
    $("sub-meta").replaceChildren(...meta.map(([text, tone]) => Object.assign(document.createElement("span"), {textContent:text, className:tone})));
    const template = $("node-template").content.firstElementChild;
    for (const [id, name, protocol] of sub.nodes) {
      const item = template.cloneNode(true);
      item.querySelector(".node-name").textContent = name;
      item.querySelector(".badge").textContent = protocolName(protocol);
      item.title = name;
      item.setAttribute("aria-checked", String(sub.id === chosenSub && id === chosenNode));
      item.addEventListener("click", () => choose(sub.id, id));
      list.append(item);
    }
  }
  subControls();
}

function choose(subId, nodeId) {
  chosenSub = subId;
  chosenNode = nodeId;
  source.value = "subscription";
  localStorage.setItem("chosenSubscription", chosenSub);
  localStorage.setItem("chosenNode", chosenNode);
  localStorage.setItem("connectionSource", source.value);
  localError = "";
  showNodes();
  render();
}

function setCatalog(rows, action) {
  const previousIds = new Set(catalog.map(sub => sub.id));
  catalog = rows.map(([id,name,refreshable,remaining,expire,nodes]) => ({id,name,refreshable,remaining,expire,nodes}));
  loaded = true;
  const added = action === "import_subscription" && catalog.find(sub => !previousIds.has(sub.id));
  const next = added || displayed() || catalog.find(sub => sub.id === chosenSub) || catalog[0];
  shownSub = next?.id || "";
  $("sub-details").hidden = !catalog.length;
  $("sub-empty").hidden = !!catalog.length;
  setText($("sub-empty-text"), "还没有订阅。粘贴订阅地址或节点链接，点「添加」。");
  showTabs();
  showNodes();
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
    if (!loaded) { setText($("sub-empty-text"), "订阅列表暂不可用。"); loaded = true; }
  } finally {
    subBusy = false;
    render();
  }
}

$("import-form").addEventListener("submit", event => {
  event.preventDefault();
  if (!$("add-sub").disabled) manage("import_subscription", {url:$("subscription-url").value.trim()});
});
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

const updateAction = $("update-action");
const updateLabel = $("update-label");
const updateFeedback = $("update-feedback");
let availableVersion = "";
async function softwareUpdate(automatic) {
  if (!invoke) return;
  updateAction.disabled = true;
  const installing = Boolean(availableVersion);
  try {
    if (installing) {
      updateFeedback.textContent = "正在下载并验证更新…";
      await invoke("install_update");
    } else {
      if (!automatic) updateFeedback.textContent = "正在检查更新…";
      availableVersion = await invoke("check_update") || "";
      if (availableVersion) {
        updateLabel.textContent = "安装更新 " + availableVersion;
        updateAction.classList.add("available");
        updateFeedback.textContent = "安装前将断开连接并恢复网络设置。";
      } else if (!automatic) updateFeedback.textContent = "当前已是最新版本。";
    }
  } catch (err) {
    if (!automatic) updateFeedback.textContent = messages[err] || messages.update_check;
    if (err === "update_signature" || err === "update_check") {
      availableVersion = "";
      updateLabel.textContent = "检查更新";
      updateAction.classList.remove("available");
    }
  } finally {
    updateAction.disabled = false;
  }
}
updateAction.addEventListener("click", () => softwareUpdate(false));
softwareUpdate(true);
manage("subscriptions");
refresh();
