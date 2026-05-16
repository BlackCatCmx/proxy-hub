(function () {
  const $ = (selector) => document.querySelector(selector);

  const state = {
    groups: [],
    currentGroupId: "",
    results: {},
    settings: null,
    selected: new Set(),
    logTimer: null
  };

  document.addEventListener("DOMContentLoaded", () => {
    const page = document.body.dataset.page;
    if (page === "login") {
      initLogin();
    } else {
      initApp();
    }
  });

  function initLogin() {
    $("#loginForm").addEventListener("submit", async (event) => {
      event.preventDefault();
      $("#loginError").textContent = "";
      const key = $("#adminKey").value;
      try {
        await api("./api/auth/login", { method: "POST", body: { key }, authRedirect: false });
        window.location.href = "./";
      } catch (error) {
        $("#loginError").textContent = error.message;
      }
    });
  }

  async function initApp() {
    bindAppEvents();
    try {
      await api("./api/me");
      await Promise.all([loadGroups(), loadSettings(), loadLogs()]);
      renderAll();
    } catch (error) {
      if (error.status === 401) {
        window.location.href = "./login.html";
        return;
      }
      showNotice(error.message, true);
    }
  }

  function bindAppEvents() {
    document.querySelectorAll(".tab").forEach((button) => {
      button.addEventListener("click", () => switchTab(button.dataset.tab));
    });
    $("#logoutBtn").addEventListener("click", async () => {
      await api("./api/auth/logout", { method: "POST" });
      window.location.href = "./login.html";
    });
    $("#groupSelect").addEventListener("change", async (event) => {
      state.currentGroupId = event.target.value;
      state.selected.clear();
      await loadResults();
      renderAll();
    });
    $("#newGroupBtn").addEventListener("click", createGroup);
    $("#renameGroupBtn").addEventListener("click", renameGroup);
    $("#deleteGroupBtn").addEventListener("click", deleteGroup);
    $("#addProxyBtn").addEventListener("click", addProxy);
    $("#bulkProxyBtn").addEventListener("click", openBulk);
    $("#replaceBulkBtn").addEventListener("click", () => submitBulk("replace"));
    $("#appendBulkBtn").addEventListener("click", () => submitBulk("append"));
    $("#latencyBtn").addEventListener("click", () => startJob("latency"));
    $("#echoBtn").addEventListener("click", () => startJob("echo"));
    $("#exportBtn").addEventListener("click", exportGroup);
    $("#copyGroupBtn").addEventListener("click", copyGroup);
    $("#selectAll").addEventListener("change", toggleSelectAll);
    $("#settingsForm").addEventListener("submit", saveSettings);
    $("#testUrlPreset").addEventListener("change", syncPresetToURL);
    $("#expectedMode").addEventListener("change", syncExpectedMode);
    $("#refreshLogsBtn").addEventListener("click", loadLogs);
  }

  async function loadGroups() {
    state.groups = await api("./api/groups");
    if (!state.currentGroupId || !state.groups.some((group) => group.id === state.currentGroupId)) {
      state.currentGroupId = state.groups[0]?.id || "";
    }
    if (state.currentGroupId) {
      await loadResults();
    }
  }

  async function loadResults() {
    if (!state.currentGroupId) {
      state.results = {};
      return;
    }
    state.results = await api(`./api/results?group_id=${encodeURIComponent(state.currentGroupId)}`);
  }

  async function loadSettings() {
    state.settings = await api("./api/settings");
    renderSettings();
  }

  async function loadLogs() {
    const logs = await api("./api/logs?limit=100");
    $("#logBox").textContent = logs.map(formatLog).join("\n");
  }

  function renderAll() {
    renderGroups();
    renderSummary();
    renderRows();
    renderSettings();
  }

  function renderGroups() {
    const select = $("#groupSelect");
    select.innerHTML = "";
    for (const group of state.groups) {
      const option = document.createElement("option");
      option.value = group.id;
      option.textContent = group.name;
      select.appendChild(option);
    }
    select.value = state.currentGroupId;
    const hasGroup = Boolean(currentGroup());
    ["renameGroupBtn", "deleteGroupBtn", "addProxyBtn", "bulkProxyBtn", "latencyBtn", "echoBtn", "exportBtn", "copyGroupBtn"].forEach((id) => {
      $("#" + id).disabled = !hasGroup;
    });
  }

  function renderSummary() {
    const group = currentGroup();
    if (!group) {
      $("#summary").textContent = "暂无分组";
      return;
    }
    const values = Object.values(state.results);
    const latencyOK = values.filter((item) => item.latency?.ok).length;
    const latencyFail = values.filter((item) => item.latency && !item.latency.ok).length;
    const latest = latestTestTime(values);
    $("#summary").textContent = `当前分组: ${group.name}  代理: ${group.proxies.length}  选中: ${state.selected.size}  延迟成功: ${latencyOK}  延迟失败: ${latencyFail}  最近测试: ${latest || "—"}`;
  }

  function renderRows() {
    const tbody = $("#proxyRows");
    tbody.innerHTML = "";
    const group = currentGroup();
    $("#selectAll").checked = Boolean(group?.proxies.length) && group.proxies.every((p) => state.selected.has(p.id));
    if (!group || group.proxies.length === 0) {
      const tr = document.createElement("tr");
      tr.innerHTML = `<td colspan="8" class="muted">暂无代理</td>`;
      tbody.appendChild(tr);
      return;
    }
    for (const item of group.proxies) {
      const result = state.results[item.id] || {};
      const tr = document.createElement("tr");
      tr.innerHTML = `
        <td class="check"><input type="checkbox" data-select="${escapeHTML(item.id)}"></td>
        <td><input class="label-input" data-label="${escapeHTML(item.id)}" value="${escapeHTML(item.label || "")}" placeholder="—"></td>
        <td>${escapeHTML(item.scheme)}</td>
        <td class="mono">${escapeHTML(item.host)}:${item.port}</td>
        <td>${latencyHTML(result.latency)}</td>
        <td>${echoHTML(result.echo)}</td>
        <td>${testTimeHTML(result)}</td>
        <td><div class="cell-actions">
          <button class="secondary" data-test-latency="${escapeHTML(item.id)}">延迟</button>
          <button class="secondary" data-test-echo="${escapeHTML(item.id)}">IP</button>
          <button class="secondary" data-edit="${escapeHTML(item.id)}">编辑</button>
          <button class="secondary" data-copy="${escapeHTML(item.id)}">复制</button>
          <button class="danger" data-delete="${escapeHTML(item.id)}">删除</button>
        </div></td>`;
      tbody.appendChild(tr);
    }
    tbody.querySelectorAll("[data-select]").forEach((box) => {
      box.checked = state.selected.has(box.dataset.select);
      box.addEventListener("change", () => {
        if (box.checked) state.selected.add(box.dataset.select);
        else state.selected.delete(box.dataset.select);
        renderSummary();
      });
    });
    tbody.querySelectorAll("[data-label]").forEach((input) => {
      input.addEventListener("change", () => updateProxy(input.dataset.label, { label: input.value }));
    });
    tbody.querySelectorAll("[data-test-latency]").forEach((button) => {
      button.addEventListener("click", () => startJob("latency", [button.dataset.testLatency]));
    });
    tbody.querySelectorAll("[data-test-echo]").forEach((button) => {
      button.addEventListener("click", () => startJob("echo", [button.dataset.testEcho]));
    });
    tbody.querySelectorAll("[data-edit]").forEach((button) => {
      button.addEventListener("click", () => editProxy(button.dataset.edit));
    });
    tbody.querySelectorAll("[data-copy]").forEach((button) => {
      button.addEventListener("click", () => copyText(findProxy(button.dataset.copy).raw));
    });
    tbody.querySelectorAll("[data-delete]").forEach((button) => {
      button.addEventListener("click", () => deleteProxy(button.dataset.delete));
    });
  }

  function renderSettings() {
    if (!state.settings || !$("#settingsForm")) return;
    const form = $("#settingsForm");
    form.concurrency.value = state.settings.concurrency;
    form.timeout_ms.value = state.settings.timeout_ms;
    form.test_url.value = state.settings.test_url;
    form.expected_status.value = state.settings.expected_status;
    form.low_latency_ms.value = state.settings.low_latency_ms;
    form.log_to_file.checked = state.settings.log_to_file;
    form.log_max_mb.value = state.settings.log_max_mb;
    const preset = $("#testUrlPreset");
    const known = Array.from(preset.options).some((option) => option.value === state.settings.test_url);
    preset.value = known ? state.settings.test_url : "custom";
    $("#expectedMode").value = state.settings.expected_status === 0 ? "2xx" : "status";
  }

  function switchTab(tab) {
    document.querySelectorAll(".tab").forEach((button) => button.classList.toggle("active", button.dataset.tab === tab));
    document.querySelectorAll(".view").forEach((view) => view.classList.remove("active"));
    $(`#${tab}View`).classList.add("active");
    if (tab === "logs") {
      loadLogs();
      clearInterval(state.logTimer);
      state.logTimer = setInterval(loadLogs, 2000);
    } else {
      clearInterval(state.logTimer);
    }
  }

  async function createGroup() {
    const name = prompt("分组名称");
    if (!name) return;
    const group = await api("./api/groups", { method: "POST", body: { name } });
    state.groups.push(group);
    state.currentGroupId = group.id;
    renderAll();
  }

  async function renameGroup() {
    const group = currentGroup();
    if (!group) return;
    const name = prompt("分组名称", group.name);
    if (!name) return;
    const updated = await api(`./api/groups/${group.id}`, { method: "PATCH", body: { name } });
    group.name = updated.name;
    renderAll();
  }

  async function deleteGroup() {
    const group = currentGroup();
    if (!group || !confirm(`删除分组 ${group.name}？`)) return;
    await api(`./api/groups/${group.id}`, { method: "DELETE" });
    state.currentGroupId = "";
    state.selected.clear();
    await loadGroups();
    renderAll();
  }

  async function addProxy() {
    const group = currentGroup();
    const raw = prompt("代理原文");
    if (!group || !raw) return;
    const item = await api(`./api/groups/${group.id}/proxies`, { method: "POST", body: { raw } });
    group.proxies.push(item);
    renderAll();
  }

  function openBulk() {
    const group = currentGroup();
    if (!group) return;
    $("#bulkText").value = group.proxies.map((item) => item.raw).join("\n");
    $("#bulkErrors").textContent = "";
    $("#bulkDialog").showModal();
  }

  async function submitBulk(mode) {
    const group = currentGroup();
    if (!group) return;
    const response = await api(`./api/groups/${group.id}/proxies/bulk`, {
      method: "POST",
      body: { text: $("#bulkText").value, mode }
    });
    Object.assign(group, response.group);
    state.selected.clear();
    $("#bulkErrors").textContent = formatBulkErrors(response.errors);
    if (!response.errors?.length) {
      $("#bulkDialog").close();
    }
    await loadResults();
    renderAll();
    showNotice(`已导入 ${response.imported} 条，重复 ${response.skipped_duplicates} 条`);
  }

  async function updateProxy(id, patch) {
    const group = currentGroup();
    const updated = await api(`./api/groups/${group.id}/proxies/${id}`, { method: "PATCH", body: patch });
    const index = group.proxies.findIndex((item) => item.id === id);
    if (index >= 0) group.proxies[index] = updated;
    renderAll();
  }

  async function editProxy(id) {
    const item = findProxy(id);
    const raw = prompt("代理原文", item.raw);
    if (!raw || raw === item.raw) return;
    await updateProxy(id, { raw });
    delete state.results[id];
  }

  async function deleteProxy(id) {
    const group = currentGroup();
    if (!confirm("删除该代理？")) return;
    await api(`./api/groups/${group.id}/proxies/${id}`, { method: "DELETE" });
    group.proxies = group.proxies.filter((item) => item.id !== id);
    state.selected.delete(id);
    delete state.results[id];
    renderAll();
  }

  async function startJob(kind, onlyIDs) {
    const group = currentGroup();
    if (!group) return;
    const proxy_ids = onlyIDs || Array.from(state.selected);
    const response = await api("./api/test/jobs", {
      method: "POST",
      body: { kind, group_id: group.id, proxy_ids }
    });
    showNotice(`任务已启动: ${response.job_id}`);
    subscribeJob(response.job_id);
  }

  function subscribeJob(id) {
    const source = new EventSource(`./api/test/jobs/${id}/stream`);
    let completed = false;
    source.onmessage = (event) => {
      const data = JSON.parse(event.data);
      if (data.type === "result" && data.result) {
        const current = state.results[data.proxy_id] || { proxy_id: data.proxy_id };
        state.results[data.proxy_id] = { ...current, ...data.result };
        renderSummary();
        renderRows();
      }
      if (data.type === "complete") {
        completed = true;
        source.close();
        loadResults().then(renderAll);
        showNotice(data.job.error ? `任务完成但保存失败: ${data.job.error}` : "任务完成", Boolean(data.job.error));
      }
    };
    source.onerror = () => {
      if (completed) return;
      source.close();
      showNotice("任务流已断开，最终结果以快照为准", true);
      loadResults().then(renderAll);
    };
  }

  function exportGroup() {
    const group = currentGroup();
    if (group) window.location.href = `./api/groups/${group.id}/export`;
  }

  function copyGroup() {
    const group = currentGroup();
    if (!group) return;
    copyText(group.proxies.map((item) => item.raw).join("\n"));
  }

  function toggleSelectAll(event) {
    const group = currentGroup();
    if (!group) return;
    if (event.target.checked) {
      group.proxies.forEach((item) => state.selected.add(item.id));
    } else {
      state.selected.clear();
    }
    renderAll();
  }

  async function saveSettings(event) {
    event.preventDefault();
    const form = event.target;
    const expectedMode = $("#expectedMode").value;
    const settings = {
      concurrency: Number(form.concurrency.value),
      timeout_ms: Number(form.timeout_ms.value),
      test_url: form.test_url.value,
      expected_status: expectedMode === "2xx" ? 0 : Number(form.expected_status.value),
      low_latency_ms: Number(form.low_latency_ms.value),
      log_to_file: form.log_to_file.checked,
      log_max_mb: Number(form.log_max_mb.value)
    };
    state.settings = await api("./api/settings", { method: "PUT", body: settings });
    renderSettings();
    showNotice("设置已保存");
  }

  function syncPresetToURL() {
    const value = $("#testUrlPreset").value;
    if (value !== "custom") {
      $("#settingsForm").test_url.value = value;
      $("#settingsForm").expected_status.value = 204;
      $("#expectedMode").value = "status";
    }
  }

  function syncExpectedMode() {
    if ($("#expectedMode").value === "2xx") {
      $("#settingsForm").expected_status.value = 0;
    } else if (Number($("#settingsForm").expected_status.value) === 0) {
      $("#settingsForm").expected_status.value = 204;
    }
  }

  async function api(path, options = {}) {
    const init = { method: options.method || "GET", headers: {} };
    if (options.body !== undefined) {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(options.body);
    }
    const response = await fetch(path, init);
    if (response.status === 401 && options.authRedirect !== false) {
      const error = new Error("未登录");
      error.status = 401;
      throw error;
    }
    const text = await response.text();
    const data = text ? JSON.parse(text) : null;
    if (!response.ok) {
      const error = new Error(data?.error || `HTTP ${response.status}`);
      error.status = response.status;
      throw error;
    }
    return data;
  }

  function currentGroup() {
    return state.groups.find((group) => group.id === state.currentGroupId);
  }

  function findProxy(id) {
    return currentGroup().proxies.find((item) => item.id === id);
  }

  function latencyHTML(result) {
    if (!result) return "—";
    if (!result.ok) return `<span class="result-error" title="${escapeHTML(result.error || "")}">失败</span>`;
    const low = state.settings?.low_latency_ms || 100;
    const cls = result.latency_ms <= low ? "lat-ok" : result.latency_ms <= low * 2 ? "lat-warn" : "lat-bad";
    return `<span class="${cls}" title="${escapeHTML(result.dns_mode || "")}">${result.latency_ms}ms</span>`;
  }

  function echoHTML(result) {
    if (!result) return "—";
    if (!result.ok) return `<span class="result-error" title="${escapeHTML(result.error || "")}">失败</span>`;
    const parts = [result.echo_ip, result.country_code, result.asn ? `AS${result.asn}` : ""].filter(Boolean);
    return `<span title="${escapeHTML([result.country, result.region, result.city, result.organization || result.asn_organization, result.source].filter(Boolean).join(" / "))}">${escapeHTML(parts.join(" "))}</span>`;
  }

  function testTimeHTML(result) {
    const times = [result.latency?.tested_at, result.echo?.tested_at].filter(Boolean).map((value) => new Date(value).getTime());
    if (!times.length) return "—";
    return formatTime(new Date(Math.max(...times)));
  }

  function latestTestTime(results) {
    const times = [];
    for (const result of results) {
      if (result.latency?.tested_at) times.push(new Date(result.latency.tested_at).getTime());
      if (result.echo?.tested_at) times.push(new Date(result.echo.tested_at).getTime());
    }
    if (!times.length) return "";
    return formatTime(new Date(Math.max(...times)));
  }

  function formatTime(date) {
    return date.toLocaleString("zh-CN", { hour12: false });
  }

  function formatLog(entry) {
    const time = new Date(entry.time).toLocaleTimeString("zh-CN", { hour12: false });
    const fields = entry.fields ? Object.entries(entry.fields).map(([key, value]) => `${key}=${value}`).join(" ") : "";
    return `${time} ${entry.level.padEnd(5)} ${entry.message}${fields ? " " + fields : ""}`;
  }

  function formatBulkErrors(errors) {
    if (!errors || errors.length === 0) return "";
    return errors.map((item) => `第 ${item.line} 行: ${item.error}`).join("\n");
  }

  async function copyText(text) {
    await navigator.clipboard.writeText(text || "");
    showNotice("已复制");
  }

  function showNotice(message, isError) {
    const box = $("#notice");
    box.textContent = message;
    box.classList.toggle("error", Boolean(isError));
    box.classList.remove("hidden");
    clearTimeout(showNotice.timer);
    showNotice.timer = setTimeout(() => box.classList.add("hidden"), 3200);
  }

  function escapeHTML(value) {
    return String(value ?? "").replace(/[&<>"']/g, (char) => ({
      "&": "&amp;",
      "<": "&lt;",
      ">": "&gt;",
      '"': "&quot;",
      "'": "&#39;"
    })[char]);
  }
})();
