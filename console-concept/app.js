const shell = document.querySelector(".shell");
const title = document.querySelector("#page-title");
const toast = document.querySelector("#toast");
const labels = {
  dashboard: "仪表盘",
  plans: "订阅管理",
  balance: "余额记录",
  keys: "Key 管理",
  requests: "API 日志",
};

function showView(id) {
  document
    .querySelectorAll(".view")
    .forEach((view) => view.classList.toggle("active", view.id === id));
  document
    .querySelectorAll(".nav-item")
    .forEach((item) =>
      item.classList.toggle("active", item.dataset.view === id),
    );
  title.textContent = labels[id];
  shell.classList.remove("menu-open");
}

document
  .querySelectorAll(".nav-item")
  .forEach((item) =>
    item.addEventListener("click", () => showView(item.dataset.view)),
  );
document
  .querySelectorAll("[data-jump]")
  .forEach((item) =>
    item.addEventListener("click", () => showView(item.dataset.jump)),
  );
document
  .querySelector(".mobile-menu")
  .addEventListener("click", () => shell.classList.toggle("menu-open"));
document
  .querySelectorAll(".sidebar-close, .menu-backdrop")
  .forEach((button) =>
    button.addEventListener("click", () => shell.classList.remove("menu-open")),
  );

let toastTimer;
function notify(message) {
  toast.textContent = message;
  toast.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.classList.remove("show"), 1800);
}

document
  .querySelectorAll("[data-refresh]")
  .forEach((button) =>
    button.addEventListener("click", () => notify("已读取最新状态")),
  );

const keyDialog = document.querySelector("#key-dialog");
document
  .querySelector("#create-key")
  .addEventListener("click", () => keyDialog.showModal());
document.querySelector("#confirm-key").addEventListener("click", (event) => {
  event.preventDefault();
  const name = document.querySelector("#key-name").value.trim() || "未命名 Key";
  const row = document.createElement("tr");
  row.innerHTML = `<td><strong>${escapeHTML(name)}</strong></td><td><code>llmgw_new…</code></td><td>1 个</td><td><span class="badge positive">可用</span></td><td>尚未使用</td><td><button class="text-button">测试</button><button class="text-button">更换</button></td>`;
  document.querySelector("#key-rows").prepend(row);
  keyDialog.close();
  notify("Key 已创建，完整值只显示一次");
});

document
  .querySelector("#request-filter")
  .addEventListener("change", (event) => {
    document.querySelectorAll("#request-rows tr").forEach((row) => {
      row.hidden =
        event.target.value !== "all" &&
        row.dataset.status !== event.target.value;
    });
  });

const detailDialog = document.querySelector("#detail-dialog");
document
  .querySelectorAll(".detail")
  .forEach((button) =>
    button.addEventListener("click", () => detailDialog.showModal()),
  );
document
  .querySelector("[data-close-detail]")
  .addEventListener("click", () => detailDialog.close());

function escapeHTML(value) {
  const node = document.createElement("span");
  node.textContent = value;
  return node.innerHTML;
}
