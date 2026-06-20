/* ====================================================================
   实验报告管理系统 - 前端入口
   组织：单页应用，根据当前用户角色加载对应模板（admin/teacher/student）
   ==================================================================== */

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

const state = {
  user: null,
  selectedCourseId: null,
  selectedLabId: null,
  courses: [],
  labs: [],
  students: [],
  reports: [],
};

const ROLE_LABEL = { admin: "DBA 管理员", teacher: "教师", student: "学生" };

/* -------------------------------------------------------------------- */
/* 工具函数                                                              */
/* -------------------------------------------------------------------- */

function toast(msg, kind = "info") {
  const el = $("#toast");
  el.textContent = msg;
  el.dataset.kind = kind;
  el.classList.add("show");
  clearTimeout(toast._t);
  toast._t = setTimeout(() => el.classList.remove("show"), 2800);
}

async function api(path, opts = {}) {
  const init = { credentials: "same-origin", ...opts };
  if (init.body && !(init.body instanceof FormData) && !init.headers) {
    init.headers = { "Content-Type": "application/json" };
  }
  const res = await fetch(path, init);
  const ct = res.headers.get("content-type") || "";
  if (!res.ok) {
    let detail = res.statusText;
    let payload = null;
    if (ct.includes("application/json")) {
      try { payload = await res.json(); detail = payload.error || detail; } catch (_) {}
    }
    // 后端返回 mustChangePassword 字段 → 强制跳转到改密界面
    if (payload && payload.mustChangePassword) {
      try { state.user = await fetch("/api/auth/me").then((r) => r.ok ? r.json() : null); } catch (_) {}
      showForcePwd();
    }
    throw new Error(detail);
  }
  if (ct.includes("application/json")) return res.json();
  return res;
}

function fmtTime(s) {
  if (!s) return "—";
  return new Date(s).toLocaleString("zh-CN", { hour12: false });
}

function fmtSize(bytes) {
  if (bytes == null) return "—";
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
  return (bytes / 1024 / 1024).toFixed(2) + " MB";
}

function statusLabel(s) {
  return { open: "开放中", closed: "已关闭", ended: "已关闭" }[s] || s;
}
function statusTag(s) {
  return `<span class="tag ${s}">${statusLabel(s)}</span>`;
}

// 把 ISO 字符串转换为 <input type="datetime-local"> 需要的本地时间格式 YYYY-MM-DDTHH:MM
function toLocalDatetimeInput(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  const pad = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// 默认截止时间：从现在起 7 天后的 23:59
function defaultDeadlineInput() {
  const d = new Date();
  d.setDate(d.getDate() + 7);
  d.setHours(23, 59, 0, 0);
  return toLocalDatetimeInput(d.toISOString());
}

function bindModal(title, html, onOpen) {
  const modal = $("#modal");
  $('[data-slot="title"]', modal).textContent = title;
  $('[data-slot="body"]', modal).innerHTML = html;
  modal.hidden = false;
  const closer = () => { modal.hidden = true; };
  $$("[data-modal-close]", modal).forEach((b) => b.addEventListener("click", closer));
  modal.addEventListener("click", (e) => { if (e.target === modal) closer(); });
  if (onOpen) onOpen($('[data-slot="body"]', modal), closer);
}

/* -------------------------------------------------------------------- */
/* 认证                                                                  */
/* -------------------------------------------------------------------- */

async function bootstrap() {
  try {
    state.user = await api("/api/auth/me");
    if (state.user.mustChangePassword) {
      showForcePwd();
    } else {
      showApp();
    }
  } catch (_) {
    showLogin();
  }
}

function hideAllScreens() {
  $("#loginView").hidden = true;
  $("#forcePwdView").hidden = true;
  $("#appView").hidden = true;
}

function showLogin() {
  hideAllScreens();
  $("#loginView").hidden = false;
}

function showForcePwd() {
  hideAllScreens();
  $("#forcePwdView").hidden = false;
  $("#forcePwdForm").reset();
  setTimeout(() => $('#forcePwdForm [name="oldPassword"]').focus(), 50);
}

function showApp() {
  hideAllScreens();
  $("#appView").hidden = false;
  $("#brandRole").textContent = ROLE_LABEL[state.user.role] || state.user.role;
  $("#profileName").textContent = state.user.displayName;
  let meta = state.user.username;
  if (state.user.role === "student" && state.user.extra?.sno) meta = state.user.extra.sno;
  if (state.user.role === "teacher" && state.user.extra?.department) meta = state.user.extra.department;
  $("#profileMeta").textContent = meta;
  renderNav();
  loadDefaultView();
}

function renderNav() {
  const nav = $("#navBar");
  nav.innerHTML = "";
  const items = {
    admin: [
      ["dashboard", "系统概览"],
      ["labs", "实验管理"],
    ],
    teacher: [["workbench", "教学工作台"]],
    student: [["workbench", "我的报告"]],
  }[state.user.role] || [];
  items.forEach(([key, label]) => {
    const b = document.createElement("button");
    b.textContent = label;
    b.dataset.view = key;
    b.addEventListener("click", () => renderView(key));
    nav.appendChild(b);
  });
}

async function loadDefaultView() {
  if (state.user.role === "admin") renderView("dashboard");
  else renderView("workbench");
}

function renderView(key) {
  $$("#navBar button").forEach((b) => b.classList.toggle("active", b.dataset.view === key));
  const main = $("#mainArea");
  main.innerHTML = "";
  if (state.user.role === "admin" && key === "dashboard") {
    main.appendChild(document.importNode($("#tpl-admin").content, true));
    initAdminView(main);
  } else if (state.user.role === "admin" && key === "labs") {
    main.appendChild(document.importNode($("#tpl-teacher").content, true));
    initTeacherView(main, /* asAdmin */ true);
  } else if (state.user.role === "teacher") {
    main.appendChild(document.importNode($("#tpl-teacher").content, true));
    initTeacherView(main, false);
  } else {
    main.appendChild(document.importNode($("#tpl-student").content, true));
    initStudentView(main);
  }
}

/* -------------------------------------------------------------------- */
/* DBA 视图                                                              */
/* -------------------------------------------------------------------- */

async function initAdminView(root) {
  root.querySelector('[data-action="refresh-stats"]').addEventListener("click", () => loadAdminStats(root));
  root.querySelector('[data-action="refresh-audit"]').addEventListener("click", () => loadAdminAudit(root));
  root.querySelector('[data-action="open-user"]').addEventListener("click", () => openCreateUserModal(root));

  // 审计日志默认折叠：第一次展开时才发请求拉取
  const toggleBtn = root.querySelector('[data-action="toggle-audit"]');
  const refreshBtn = root.querySelector('[data-action="refresh-audit"]');
  const auditBox = root.querySelector('[data-section="audit"]');
  let auditLoaded = false;
  toggleBtn.addEventListener("click", async () => {
    const willShow = auditBox.hidden;
    auditBox.hidden = !willShow;
    toggleBtn.textContent = willShow ? "折叠" : "展开";
    refreshBtn.disabled = !willShow;
    if (willShow && !auditLoaded) {
      await loadAdminAudit(root);
      auditLoaded = true;
    }
  });

  await Promise.all([
    loadAdminStats(root),
    loadAdminUsers(root),
    loadAdminCourses(root),
  ]);
}

async function loadAdminStats(root) {
  try {
    const s = await api("/api/admin/stats");
    Object.entries(s).forEach(([k, v]) => {
      const el = root.querySelector(`[data-stat="${k}"]`);
      if (el) el.textContent = v;
    });
  } catch (e) { toast(e.message); }
}

async function loadAdminUsers(root) {
  const tbody = root.querySelector('[data-list="users"]');
  try {
    const users = await api("/api/admin/users");
    tbody.innerHTML = users.map((u) => `
      <tr>
        <td>${u.id}</td>
        <td>${u.username}</td>
        <td>${u.displayName}</td>
        <td>${ROLE_LABEL[u.role] || u.role}</td>
        <td>${fmtTime(u.createdAt)}</td>
        <td>
          <button class="secondary" data-rid="${u.id}" data-do="pwd">重置密码</button>
          <button class="warn" data-rid="${u.id}" data-do="del">删除</button>
        </td>
      </tr>`).join("");
    tbody.querySelectorAll("button").forEach((b) => {
      b.addEventListener("click", () => userAction(b.dataset.do, b.dataset.rid, root));
    });
  } catch (e) { toast(e.message); }
}

function userAction(action, id, root) {
  if (action === "pwd") {
    bindModal("重置密码", `
      <label>新密码<input type="text" id="newPwd" required></label>
      <div class="row"><button class="secondary" data-modal-close>取消</button><button id="confirmPwd">确认</button></div>
    `, (body, close) => {
      body.querySelector("#confirmPwd").addEventListener("click", async () => {
        const pwd = body.querySelector("#newPwd").value.trim();
        if (!pwd) return toast("请输入新密码");
        try {
          await api(`/api/admin/users/${id}/password`, { method: "PATCH", body: JSON.stringify({ password: pwd }) });
          toast("密码已重置"); close();
        } catch (e) { toast(e.message); }
      });
    });
  } else if (action === "del") {
    if (!confirm("确定删除该用户？")) return;
    api(`/api/admin/users/${id}`, { method: "DELETE" })
      .then(() => { toast("已删除"); loadAdminUsers(root); loadAdminStats(root); })
      .catch((e) => toast(e.message));
  }
}

function openCreateUserModal(root) {
  bindModal("新增用户", `
    <label>用户名<input id="nu_username" required></label>
    <label>初始密码<input id="nu_password" required></label>
    <label>姓名<input id="nu_display" required></label>
    <label>角色
      <select id="nu_role"><option value="teacher">教师</option><option value="admin">管理员</option></select>
    </label>
    <label>所在部门<input id="nu_dept" placeholder="教师可选"></label>
    <div class="row"><button class="secondary" data-modal-close>取消</button><button id="nu_submit">创建</button></div>
  `, (body, close) => {
    body.querySelector("#nu_submit").addEventListener("click", async () => {
      const payload = {
        username: body.querySelector("#nu_username").value.trim(),
        password: body.querySelector("#nu_password").value,
        displayName: body.querySelector("#nu_display").value.trim(),
        role: body.querySelector("#nu_role").value,
        department: body.querySelector("#nu_dept").value.trim(),
      };
      if (!payload.username || !payload.password || !payload.displayName) {
        return toast("请填写完整");
      }
      try {
        await api("/api/admin/users", { method: "POST", body: JSON.stringify(payload) });
        toast("已创建"); close();
        await loadAdminUsers(root);
        await loadAdminStats(root);
      } catch (e) { toast(e.message); }
    });
  });
}

async function loadAdminCourses(root) {
  const tbody = root.querySelector('[data-list="courses"]');
  try {
    const courses = await api("/api/admin/courses");
    tbody.innerHTML = courses.map((c) => `
      <tr>
        <td>${c.name}</td>
        <td>${c.semester}</td>
        <td>${c.teacher}</td>
        <td>${c.studentCount}</td>
        <td>${c.labCount}</td>
        <td><code>${c.folderName}</code></td>
        <td>${fmtTime(c.createdAt)}</td>
      </tr>`).join("");
  } catch (e) { toast(e.message); }
}

async function loadAdminAudit(root) {
  const tbody = root.querySelector('[data-list="audit"]');
  try {
    const list = await api("/api/admin/audit");
    tbody.innerHTML = list.map((e) => `
      <tr>
        <td>${fmtTime(e.createdAt)}</td>
        <td>${e.username || "-"}</td>
        <td>${e.action}</td>
        <td>${e.detail || ""}</td>
      </tr>`).join("");
  } catch (e) { toast(e.message); }
}

/* -------------------------------------------------------------------- */
/* 教师视图                                                              */
/* -------------------------------------------------------------------- */

async function initTeacherView(root, asAdmin = false) {
  // 管理员视图：去掉"导入实验课信息"和标题文案做差异化
  const head = root.querySelector(".page-head");
  if (asAdmin) {
    head.querySelector("h1").textContent = "实验管理（管理员）";
    head.querySelector("p").textContent = "查看全部教师创建的课程，调整实验项目状态、查看下载报告、导出成绩。课程创建仍由对应教师负责。";
    const importPanel = root.querySelector('form[data-action="import"]')?.closest(".panel");
    if (importPanel) importPanel.remove();
  }

  root.querySelector('[data-action="refresh-courses"]').addEventListener("click", () => loadTeacherCourses(root));
  const importForm = root.querySelector('form[data-action="import"]');
  if (importForm) importForm.addEventListener("submit", (e) => doImport(e, root));
  root.querySelector('[data-action="download-zip"]').addEventListener("click", () => {
    if (state.selectedLabId) {
      window.location.href = `/api/teacher/labs/${state.selectedLabId}/reports/download`;
    }
  });
  root.querySelector('[data-action="export-grades"]').addEventListener("click", () => {
    if (state.selectedCourseId) {
      window.location.href = `/api/teacher/courses/${state.selectedCourseId}/grades/export`;
    } else {
      toast("请先选择课程");
    }
  });
  await loadTeacherCourses(root);
}

async function doImport(e, root) {
  e.preventDefault();
  const form = e.currentTarget;
  const data = new FormData(form);
  try {
    const r = await api("/api/teacher/courses/import", { method: "POST", body: data });
    toast(`导入完成：${r.studentCount} 学生，${r.labCount} 个实验`);
    form.reset();
    await loadTeacherCourses(root);
  } catch (err) { toast(err.message); }
}

async function loadTeacherCourses(root) {
  try {
    state.courses = await api("/api/teacher/courses");
    renderTeacherCourses(root);
    if (state.courses.length) {
      const first = state.courses[0].id;
      if (!state.courses.some((c) => c.id === state.selectedCourseId)) {
        state.selectedCourseId = first;
      }
      await selectTeacherCourse(root, state.selectedCourseId);
    } else {
      root.querySelector('[data-list="labs"]').innerHTML = `<p class="muted">先导入实验课信息</p>`;
    }
  } catch (e) { toast(e.message); }
}

function renderTeacherCourses(root) {
  const box = root.querySelector('[data-list="courses"]');
  if (!state.courses.length) {
    box.innerHTML = `<p class="muted">尚未导入课程</p>`;
    return;
  }
  box.innerHTML = "";
  state.courses.forEach((c) => {
    const item = document.createElement("div");
    item.className = "item" + (c.id === state.selectedCourseId ? " active" : "");
    item.innerHTML = `
      <div class="item-head"><strong>${c.name}</strong><small>${c.semester}</small></div>
      <small>教师：${c.teacher} · ${c.studentCount} 名学生 · ${c.labCount} 个实验</small>
      <small class="muted">目录：${c.folderName}</small>
    `;
    item.addEventListener("click", () => selectTeacherCourse(root, c.id));
    box.appendChild(item);
  });
}

async function selectTeacherCourse(root, courseID) {
  state.selectedCourseId = courseID;
  state.selectedLabId = null;
  renderTeacherCourses(root);
  const course = state.courses.find((c) => c.id === courseID);
  root.querySelector('[data-slot="currentCourse"]').textContent = course ? course.name : "未选择课程";
  const exportBtn = root.querySelector('[data-action="export-grades"]');
  if (exportBtn) exportBtn.disabled = !course;
  try {
    state.labs = await api(`/api/teacher/courses/${courseID}/labs`);
    renderTeacherLabs(root);
    state.students = await api(`/api/teacher/courses/${courseID}/students`);
    renderTeacherStudents(root);
    renderTeacherReports(root, []);
    root.querySelector('[data-action="download-zip"]').disabled = true;
    root.querySelector('[data-slot="currentLab"]').textContent = "未选择";
  } catch (e) { toast(e.message); }
}

function renderTeacherLabs(root) {
  const box = root.querySelector('[data-list="labs"]');
  if (!state.labs.length) {
    box.innerHTML = `<p class="muted">该课程暂无实验项目</p>`;
    return;
  }
  box.innerHTML = "";
  state.labs.forEach((lab) => {
    const item = document.createElement("div");
    item.className = "item" + (lab.id === state.selectedLabId ? " active" : "");
    const isOpen = lab.status === "open";
    item.innerHTML = `
      <div class="item-head">
        <strong>${lab.name}</strong>
        ${statusTag(lab.status)}
      </div>
      <small>已提交 ${lab.submittedCount}/${lab.studentCount} · 截止时间 ${fmtTime(lab.deadline)}</small>
      <div class="actions">
        <button data-act="open" title="${isOpen ? "已开放，可调整截止时间" : "允许学生上传报告"}">开放</button>
        <button class="secondary" data-act="close" title="停止接收学生上传">关闭</button>
        <button class="danger" data-act="delete" title="永久删除本实验项目及其所有报告">删除</button>
        <button class="secondary" data-act="view" style="margin-left:auto">查看报告</button>
      </div>
    `;
    item.querySelector('[data-act="open"]').addEventListener("click", (e) => {
      e.stopPropagation();
      openDeadlineModal(root, lab);
    });
    item.querySelector('[data-act="close"]').addEventListener("click", (e) => {
      e.stopPropagation();
      closeLab(root, lab);
    });
    item.querySelector('[data-act="delete"]').addEventListener("click", (e) => {
      e.stopPropagation();
      deleteLab(root, lab);
    });
    item.querySelector('[data-act="view"]').addEventListener("click", (e) => {
      e.stopPropagation();
      selectTeacherLab(root, lab.id);
    });
    item.addEventListener("click", () => selectTeacherLab(root, lab.id));
    box.appendChild(item);
  });
}

// 弹窗：开放实验项目并选择截止时间
function openDeadlineModal(root, lab) {
  const dlValue = toLocalDatetimeInput(lab.deadline) || defaultDeadlineInput();
  bindModal(`开放实验项目：${lab.name}`, `
    <p class="muted">开放后学生可在截止时间之前上传 PDF 实验报告（≤ 8MB）。</p>
    <label>截止时间<input type="datetime-local" id="dl_input" value="${dlValue}"></label>
    <label class="check-line"><input type="checkbox" id="dl_none"> 不设置截止时间（一直开放直到手动关闭）</label>
    <div class="row">
      <button class="secondary" data-modal-close>取消</button>
      <button id="dl_confirm">确认开放</button>
    </div>
  `, (body, close) => {
    const input = body.querySelector("#dl_input");
    const none = body.querySelector("#dl_none");
    none.addEventListener("change", () => { input.disabled = none.checked; });
    body.querySelector("#dl_confirm").addEventListener("click", async () => {
      const deadline = none.checked
        ? ""
        : (input.value ? new Date(input.value).toISOString() : "");
      try {
        await api(`/api/teacher/labs/${lab.id}`, {
          method: "PATCH",
          body: JSON.stringify({ status: "open", deadline, description: lab.description || "" }),
        });
        toast(deadline ? `已开放，截止 ${fmtTime(deadline)}` : "已开放（无截止时间）");
        close();
        await selectTeacherCourse(root, state.selectedCourseId);
      } catch (e) { toast(e.message); }
    });
  });
}

async function closeLab(root, lab) {
  try {
    await api(`/api/teacher/labs/${lab.id}`, {
      method: "PATCH",
      body: JSON.stringify({ status: "closed", deadline: "", description: lab.description || "" }),
    });
    toast(`已关闭：${lab.name}`);
    await selectTeacherCourse(root, state.selectedCourseId);
  } catch (e) { toast(e.message); }
}

async function deleteLab(root, lab) {
  const tip = lab.submittedCount > 0
    ? `\n注意：该实验已有 ${lab.submittedCount} 份学生报告，删除后无法恢复。`
    : "";
  if (!confirm(`确定删除实验项目「${lab.name}」？${tip}`)) return;
  try {
    const r = await api(`/api/teacher/labs/${lab.id}`, { method: "DELETE" });
    toast(`已删除${r.removedReports ? `（同时清理 ${r.removedReports} 份报告）` : ""}`);
    if (state.selectedLabId === lab.id) state.selectedLabId = null;
    await selectTeacherCourse(root, state.selectedCourseId);
  } catch (e) { toast(e.message); }
}

function renderTeacherStudents(root) {
  const tbody = root.querySelector('[data-list="students"]');
  root.querySelector('[data-slot="studentCount"]').textContent = `${state.students.length} 人`;
  tbody.innerHTML = state.students.map((s) => `
    <tr><td>${s.sno}</td><td>${s.name}</td><td>${s.className || "—"}</td></tr>
  `).join("");
}

async function selectTeacherLab(root, labID) {
  state.selectedLabId = labID;
  renderTeacherLabs(root);
  const lab = state.labs.find((l) => l.id === labID);
  root.querySelector('[data-slot="currentLab"]').textContent = lab ? lab.name : "未选择实验项目";
  root.querySelector('[data-action="download-zip"]').disabled = false;
  try {
    const reports = await api(`/api/teacher/labs/${labID}/reports`);
    state.reports = reports;
    renderTeacherReports(root, reports);
  } catch (e) { toast(e.message); }
}

function renderTeacherReports(root, reports) {
  const tbody = root.querySelector('[data-list="reports"]');
  if (!reports.length) {
    tbody.innerHTML = `<tr><td colspan="8" class="muted">暂无报告</td></tr>`;
    return;
  }
  tbody.innerHTML = "";
  reports.forEach((r) => {
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>${r.sno}</td>
      <td>${r.studentName}</td>
      <td><a href="/api/teacher/reports/${r.id}/file" target="_blank">${r.storedName}</a></td>
      <td>${fmtSize(r.sizeBytes)}</td>
      <td>${fmtTime(r.submittedAt)}</td>
      <td><input type="number" min="0" max="100" step="0.5" value="${r.score ?? ""}" style="width:90px"></td>
      <td><input type="text" value="${r.comment || ""}"></td>
      <td><button>保存</button></td>
    `;
    tr.querySelector("button").addEventListener("click", async () => {
      const inputs = tr.querySelectorAll("input");
      const score = inputs[0].value === "" ? null : Number(inputs[0].value);
      try {
        await api(`/api/teacher/reports/${r.id}/grade`, {
          method: "PATCH",
          body: JSON.stringify({ score, comment: inputs[1].value }),
        });
        toast("评分已保存");
      } catch (e) { toast(e.message); }
    });
    tbody.appendChild(tr);
  });
}

/* -------------------------------------------------------------------- */
/* 学生视图                                                              */
/* -------------------------------------------------------------------- */

async function initStudentView(root) {
  root.querySelector('[data-action="refresh-courses"]').addEventListener("click", () => loadStudentCourses(root));
  await loadStudentCourses(root);
}

async function loadStudentCourses(root) {
  try {
    state.courses = await api("/api/student/courses");
    const box = root.querySelector('[data-list="courses"]');
    if (!state.courses.length) {
      box.innerHTML = `<p class="muted">尚未被分配到任何课程</p>`;
      root.querySelector('[data-list="labs"]').innerHTML = `<tr><td colspan="6" class="muted">请等待教师导入名单</td></tr>`;
      return;
    }
    box.innerHTML = "";
    state.courses.forEach((c) => {
      const item = document.createElement("div");
      item.className = "item" + (c.id === state.selectedCourseId ? " active" : "");
      item.innerHTML = `
        <div class="item-head"><strong>${c.name}</strong><small>${c.semester}</small></div>
        <small>教师：${c.teacher}</small>
      `;
      item.addEventListener("click", () => selectStudentCourse(root, c.id));
      box.appendChild(item);
    });
    if (!state.selectedCourseId || !state.courses.some((c) => c.id === state.selectedCourseId)) {
      state.selectedCourseId = state.courses[0].id;
    }
    await selectStudentCourse(root, state.selectedCourseId);
  } catch (e) { toast(e.message); }
}

async function selectStudentCourse(root, courseID) {
  state.selectedCourseId = courseID;
  await loadStudentCourses_redraw(root);
  const course = state.courses.find((c) => c.id === courseID);
  root.querySelector('[data-slot="currentCourse"]').textContent = course ? course.name : "未选择课程";
  try {
    state.labs = await api(`/api/student/courses/${courseID}/labs`);
    renderStudentLabs(root);
  } catch (e) { toast(e.message); }
}

async function loadStudentCourses_redraw(root) {
  const box = root.querySelector('[data-list="courses"]');
  $$(".item", box).forEach((el, i) => {
    const c = state.courses[i];
    el.classList.toggle("active", c && c.id === state.selectedCourseId);
  });
}

function renderStudentLabs(root) {
  const tbody = root.querySelector('[data-list="labs"]');
  if (!state.labs.length) {
    tbody.innerHTML = `<tr><td colspan="7" class="muted">无实验项目</td></tr>`;
    return;
  }
  tbody.innerHTML = "";
  state.labs.forEach((lab) => {
    const submitted = lab.submittedCount > 0;
    const canUpload = lab.status === "open";
    let scoreCell;
    if (!submitted) {
      scoreCell = `<span class="muted">—</span>`;
    } else if (lab.myGradedAt) {
      const s = lab.myScore != null ? Number(lab.myScore).toFixed(1) : "—";
      scoreCell = `<strong class="score">${s}</strong>`;
    } else {
      scoreCell = `<span class="tag closed">待评分</span>`;
    }
    const commentCell = lab.myComment
      ? `<small>${escapeHtml(lab.myComment)}</small>`
      : (submitted && lab.myGradedAt ? `<span class="muted">—</span>` : `<span class="muted">尚无</span>`);

    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td><strong>${lab.name}</strong><div><small class="muted">${lab.description || ""}</small></div></td>
      <td>${statusTag(lab.status)}</td>
      <td>${fmtTime(lab.deadline)}</td>
      <td>${submitted
        ? `<span class="tag">已提交</span><div><small class="muted">${fmtTime(lab.mySubmittedAt)}</small></div>`
        : '<span class="tag closed">未提交</span>'}</td>
      <td>${scoreCell}</td>
      <td>${commentCell}</td>
      <td>
        <button ${canUpload ? "" : "disabled"} data-upload>${submitted ? "重新上传" : "上传报告"}</button>
        ${submitted ? `<a href="/api/student/labs/${lab.id}/report/file" target="_blank"><button class="secondary">查看PDF</button></a>` : ""}
      </td>
    `;
    tr.querySelector("[data-upload]").addEventListener("click", () => openStudentUpload(root, lab));
    tbody.appendChild(tr);
  });
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  })[c]);
}

function openStudentUpload(root, lab) {
  bindModal(`上传：${lab.name}`, `
    <p class="muted">
      实验项目“${lab.name}”，文件大小 ≤ 8MB，仅支持 PDF。
      上传后系统会自动重命名为“${state.user.extra?.sno}-${state.user.displayName}-${lab.name}.pdf”。
    </p>
    <label class="file-field">选择 PDF<input type="file" id="su_file" accept=".pdf,application/pdf" required></label>
    <div class="row"><button class="secondary" data-modal-close>取消</button><button id="su_submit">上传</button></div>
  `, (body, close) => {
    body.querySelector("#su_submit").addEventListener("click", async () => {
      const file = body.querySelector("#su_file").files[0];
      if (!file) return toast("请选择 PDF 文件");
      if (file.size > 8 * 1024 * 1024) return toast("文件超过 8MB");
      const form = new FormData();
      form.append("file", file);
      try {
        const r = await api(`/api/student/labs/${lab.id}/report`, { method: "POST", body: form });
        toast(`上传成功：${r.filename}`); close();
        await selectStudentCourse(root, state.selectedCourseId);
      } catch (err) { toast(err.message); }
    });
  });
}

/* -------------------------------------------------------------------- */
/* 全局事件                                                              */
/* -------------------------------------------------------------------- */

$("#loginForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const form = new FormData(e.currentTarget);
  try {
    const r = await api("/api/auth/login", {
      method: "POST",
      body: JSON.stringify({ username: form.get("username"), password: form.get("password") }),
    });
    state.user = await api("/api/auth/me");
    if (r.user.mustChangePassword || state.user.mustChangePassword) {
      toast("首次登录，请先修改密码");
      showForcePwd();
    } else {
      toast(`欢迎，${r.user.displayName}`);
      showApp();
    }
  } catch (err) { toast(err.message); }
});

$("#forcePwdForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const form = new FormData(e.currentTarget);
  const oldPwd = form.get("oldPassword");
  const newPwd = form.get("newPassword");
  const confirmPwd = form.get("confirmPassword");
  if (newPwd !== confirmPwd) { toast("两次输入的新密码不一致"); return; }
  if (newPwd.length < 6) { toast("新密码至少 6 位"); return; }
  if (newPwd === oldPwd) { toast("新密码不能与原密码相同"); return; }
  try {
    await api("/api/auth/password", {
      method: "POST",
      body: JSON.stringify({ oldPassword: oldPwd, newPassword: newPwd }),
    });
    state.user = await api("/api/auth/me");
    toast("密码已修改，欢迎使用系统");
    showApp();
  } catch (err) { toast(err.message); }
});

$("#forcePwdCancel").addEventListener("click", async () => {
  try { await api("/api/auth/logout", { method: "POST" }); } catch (_) {}
  window.location.href = "/";
});

$("#logoutBtn").addEventListener("click", async () => {
  try { await api("/api/auth/logout", { method: "POST" }); } catch (_) {}
  // 强制回到登录页：清空所有 DOM 与状态，最稳妥的方式是整页刷新。
  window.location.href = "/";
});

$("#changePwdBtn").addEventListener("click", () => {
  bindModal("修改密码", `
    <label>原密码<input type="password" id="cp_old" required></label>
    <label>新密码<input type="password" id="cp_new" required></label>
    <div class="row"><button class="secondary" data-modal-close>取消</button><button id="cp_submit">确认</button></div>
  `, (body, close) => {
    body.querySelector("#cp_submit").addEventListener("click", async () => {
      try {
        await api("/api/auth/password", {
          method: "POST",
          body: JSON.stringify({
            oldPassword: body.querySelector("#cp_old").value,
            newPassword: body.querySelector("#cp_new").value,
          }),
        });
        toast("密码已更新"); close();
      } catch (e) { toast(e.message); }
    });
  });
});

bootstrap();
