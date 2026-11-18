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
