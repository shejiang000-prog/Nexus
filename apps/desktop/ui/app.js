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

const labels = {
  stopped: "未连接", connecting: "连接中", stopping: "正在断开",
  "ready:h3": "已连接", "ready:h2": "已连接",
  missing: "找不到 nexus-core.exe", error: "连接失败，请检查邀请、端口和节点",
  invalid: "邀请格式无效", unavailable: "无法读取核心状态",
};
const button = document.getElementById("toggle");
const invite = document.getElementById("invite");
const status = document.getElementById("status");
const route = document.getElementById("route");
const invoke = window.__TAURI__?.core.invoke;
let active = false;
let busy = false;
let revision = 0;

function render(state) {
  const label = labels[state] || labels.unavailable;
  if (status.textContent !== label) status.textContent = label;
  const transport = state === "ready:h3" ? "H3" : state === "ready:h2" ? "H2" : "—";
  const routeLabel = "当前线路 · " + transport;
  if (route.textContent !== routeLabel) route.textContent = routeLabel;
  active = ["connecting", "ready:h3", "ready:h2", "stopping"].includes(state);
  document.getElementById("enrollment").hidden = active;
  button.textContent = active ? "断开" : "连接";
  button.disabled = busy || state === "stopping" || state === "unavailable";
}

document.getElementById("connection-form").addEventListener("submit", async event => {
  event.preventDefault();
  if (button.disabled) return;
  busy = true;
  revision++;
  button.disabled = true;
  let state;
  try {
    state = active ? await invoke("stop_core") : await invoke("start_core", { invite: invite.value.trim() });
    if (state === "connecting") invite.value = "";
  } catch (error) {
    state = error === "invalid" ? "invalid" : "unavailable";
  } finally {
    busy = false;
    render(state);
  }
});

async function refresh() {
  const version = revision;
  if (!busy && !document.hidden) {
    let state;
    try { state = await invoke("core_status"); }
    catch { state = "unavailable"; }
    if (!busy && revision === version) render(state);
  }
  setTimeout(refresh, 750);
}
refresh();
