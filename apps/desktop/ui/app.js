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
  "proxy-busy": "另一个 Nexus 窗口正在管理系统代理。",
  "proxy-changed": "系统代理已由其他程序修改，已保留当前设置。",
  "proxy-error": "系统代理设置或恢复失败。请检查权限，必要时重新启动 Nexus 以重试恢复。",
  generic: "操作失败，请检查本机设备记录和节点授权。"
};
const proxy = $("proxy");
const mode = $("mode");
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
const connected = state => state === "h2" || state === "h3";
const running = state => connected(state) || state === "connecting" || state === "stopping";

function render() {
  const [state, socks, http, enabled, notice] = snapshot;
  const ready = connected(state);
  const active = running(state);
  const label = ready ? "已连接" : ({stopped:"未连接", connecting:"连接中", stopping:"正在断开"})[state] || "连接失败";
  if ($("status").textContent !== label) $("status").textContent = label;
  $("route").textContent = ready ? "当前线路 · " + state.toUpperCase() : "";
  const error = localError || messages[state] || messages[notice] || "";
  if ($("feedback").textContent !== error) $("feedback").textContent = error;
  $("feedback").hidden = !error;
  $("enrollment").hidden = active;
  $("ports").hidden = !ready;
  if (ready) {
    $("socks").textContent = "127.0.0.1:" + socks;
    $("http").textContent = "127.0.0.1:" + http;
  }
  if (active) proxy.checked = enabled;
  toggle.textContent = active ? "断开" : "连接";
  toggle.disabled = busy || state === "stopping" || !invoke;
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
      await invoke("start_core", {invite:invite.value.trim(), mode:mode.value, proxy:proxy.checked});
      invite.value = "";
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
refresh();
