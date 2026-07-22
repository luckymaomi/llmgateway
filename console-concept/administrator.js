const shell = document.querySelector(".shell");
const pageTitle = document.querySelector("#page-title");
const content = document.querySelector("#admin-view");
const toast = document.querySelector("#toast");
const detailDialog = document.querySelector("#admin-detail");
const detailTitle = document.querySelector("#detail-title");
const detailBody = document.querySelector("#detail-body");

function render(viewID) {
  const view = window.adminViews[viewID];
  if (!view) return;
  content.innerHTML = view.content;
  pageTitle.textContent = view.title;
  document
    .querySelectorAll(".nav-item")
    .forEach((item) =>
      item.classList.toggle("active", item.dataset.view === viewID),
    );
  shell.classList.remove("menu-open");
  bindContentActions();
}

document
  .querySelectorAll(".nav-item")
  .forEach((item) =>
    item.addEventListener("click", () => render(item.dataset.view)),
  );
document
  .querySelector(".mobile-menu")
  .addEventListener("click", () => shell.classList.toggle("menu-open"));
document
  .querySelectorAll(".sidebar-close, .menu-backdrop")
  .forEach((button) =>
    button.addEventListener("click", () => shell.classList.remove("menu-open")),
  );

let timer;
function notify(message) {
  toast.textContent = message;
  toast.classList.add("show");
  clearTimeout(timer);
  timer = setTimeout(() => toast.classList.remove("show"), 1800);
}

function bindContentActions() {
  content
    .querySelectorAll("[data-refresh]")
    .forEach((button) =>
      button.addEventListener("click", () => notify("已读取最新运行事实")),
    );
  content
    .querySelectorAll("[data-action]")
    .forEach((button) =>
      button.addEventListener("click", () =>
        notify(`${button.textContent.trim()}操作将在正式页面打开`),
      ),
    );
  content
    .querySelectorAll("[data-save]")
    .forEach((button) =>
      button.addEventListener("click", () => notify("站点资料已保存")),
    );
  content
    .querySelectorAll("[data-view-link]")
    .forEach((button) =>
      button.addEventListener("click", () => render(button.dataset.viewLink)),
    );
  content.querySelectorAll("[data-probe]").forEach((button) =>
    button.addEventListener("click", () => {
      detailTitle.textContent = "测试上游 API Key";
      detailBody.innerHTML = `<div class="probe-summary"><div><span>API Key</span><strong>${button.dataset.probe}</strong></div><div><span>测试模型</span><strong>${button.dataset.model}</strong></div></div><button class="button full-button" data-run-probe>发送最小生成请求</button><div class="probe-result" data-probe-result>等待测试</div>`;
      detailDialog.showModal();
      detailBody
        .querySelector("[data-run-probe]")
        .addEventListener("click", (event) => {
          event.currentTarget.disabled = true;
          detailBody.querySelector("[data-probe-result]").innerHTML =
            '<span class="badge positive">连接成功</span><strong>上游已返回生成内容和权威 usage</strong><small>Base URL、TLS、鉴权与模型均可用</small>';
        });
    }),
  );
  content.querySelectorAll("[data-request-detail]").forEach((button) =>
    button.addEventListener("click", () => {
      detailTitle.textContent = "请求详情";
      detailBody.innerHTML = `<div class="fact-list"><div><span>状态</span><b>${button.dataset.state}</b></div><div><span>Request ID</span><b><code>${button.dataset.requestDetail}…</code></b></div><div><span>发送边界</span><b>${button.dataset.state === "待确认" ? "上游结果未知，不自动重放" : "已确认"}</b></div><div><span>错误类别</span><b>${button.dataset.state === "失败" ? "upstream_unavailable" : "—"}</b></div></div>`;
      detailDialog.showModal();
    }),
  );
  const requestStatus = content.querySelector("[data-request-status]");
  if (requestStatus) {
    requestStatus.addEventListener("change", () => {
      let visible = 0;
      content.querySelectorAll("[data-request-row]").forEach((row) => {
        const show =
          requestStatus.value === "all" ||
          row.dataset.requestRow === requestStatus.value;
        row.hidden = !show;
        if (show) visible += 1;
      });
      content.querySelector("[data-request-count]").textContent =
        `${visible} 条记录`;
    });
  }
}

detailDialog
  .querySelector("[data-close]")
  .addEventListener("click", () => detailDialog.close());

render("admin-dashboard");
