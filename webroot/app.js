/*
 * NovaAI-MCP KernelSU WebUI —— 页面逻辑。
 *
 * 依赖顺序（见 index.html）：lib/kernelsu.js → lib/mcp-client.js →
 * lib/config.js → app.js。全部是经典脚本，不用 ES module —— WebView 以
 * file:// 打开页面时，静态 import 可能被 CORS 拦掉（见 lib/kernelsu.js 注释）。
 *
 * 三条设计约束：
 *   1. 页面只是本服务的一个普通 MCP 客户端，没有特权通道；
 *   2. 只有"改 config.json"这一件事需要 root，且只有 lib/config.js 做；
 *   3. 上游状态一律来自服务端的 novaai_upstream_status / probe_upstreams，
 *      页面**不自己**判活（否则就有了第二份状态判定实现）。
 */
(function () {
  'use strict';

  var $ = function (sel, root) { return (root || document).querySelector(sel); };
  var $$ = function (sel, root) {
    return Array.prototype.slice.call((root || document).querySelectorAll(sel));
  };

  var state = {
    tools: [],
    upstreams: [],
    ksuOk: false,
    busy: false
  };

  // ---------------------------------------------------------------- 小工具

  function toast(msg, ms) {
    var el = $('#toast');
    el.textContent = String(msg);
    el.classList.remove('is-hidden');
    clearTimeout(toast._t);
    toast._t = setTimeout(function () { el.classList.add('is-hidden'); }, ms || 3200);
  }

  function showBanner(msg) {
    var el = $('#upstreamBanner');
    if (!msg) { el.classList.add('is-hidden'); el.textContent = ''; return; }
    el.textContent = msg;
    el.classList.remove('is-hidden');
  }

  function escapeHtml(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }

  function pillClass(status) {
    switch (status) {
      case 'running': return 'pill-ok';
      case 'stopped': return 'pill-idle';
      case 'error': return 'pill-err';
      case 'disabled': return 'pill-warn';
      default: return 'pill-unknown';
    }
  }

  var STATUS_TEXT = {
    running: '运行中',
    stopped: '未启动',
    error: '错误',
    disabled: '已禁用'
  };

  /** 把 MCP tools/call 的结果文本解析成对象（失败时返回原文本）。 */
  function parseResult(res) {
    if (!res) { return null; }
    if (res.structured) { return res.structured; }
    try { return JSON.parse(res.text); } catch (e) { return res.text; }
  }

  /** 拆出上游工具名里的前缀，用于工具目录分组。 */
  function splitToolName(name) {
    var idx = name.indexOf('__');
    if (idx <= 0) { return null; }
    return { upstream: name.slice(0, idx), tool: name.slice(idx + 2) };
  }

  // ------------------------------------------------------------ 服务连通性

  function setSvc(text, cls) {
    var el = $('#svc');
    el.textContent = text;
    el.className = 'pill ' + cls;
  }

  function checkService() {
    return NovaMcp.request('ping', undefined, 6000)
      .then(function () { setSvc('已连接', 'pill-ok'); return true; })
      .catch(function (err) {
        setSvc('未连接', 'pill-err');
        throw err;
      });
  }

  // -------------------------------------------------------------- 上游管理

  function loadConfig() {
    return NovaConfig.read().then(function (cfg) {
      state.upstreams = Array.isArray(cfg.upstreams) ? cfg.upstreams : [];
      return cfg;
    });
  }

  /**
   * 拉取服务端的真实状态。
   *
   * 刻意**不**在页面里自己判活：状态判定（含 error 与 stopped 的区分、
   * stdio 进程存活）是 upstream 包的唯一职责，页面复刻一份必然漂移。
   */
  function refreshStatus() {
    return NovaMcp.callTool('novaai_upstream_status', {}, 20000).then(function (res) {
      var data = parseResult(res) || {};
      state.statusByName = {};
      (data.upstreams || []).forEach(function (s) { state.statusByName[s.name] = s; });
      return data;
    });
  }

  function renderUpstreams() {
    var box = $('#upstreamList');
    if (!state.upstreams.length) {
      box.innerHTML = '<p class="muted">还没有上游。用下面的「添加上游」接入第一个 MCP 服务。</p>';
      return;
    }

    box.innerHTML = state.upstreams.map(function (u, i) {
      var st = (state.statusByName || {})[u.name] || {};
      var status = st.status || 'unknown';
      var target = u.type === 'http' ? u.url : [u.command].concat(u.args || []).join(' ');
      var launch = u.launch && u.launch.type ? u.launch.type : 'manual';
      var canStart = launch !== 'manual';
      var detail = st.reason ? '<div class="tool-desc">原因：' + escapeHtml(st.reason) + '</div>' : '';
      var tools = (typeof st.tools === 'number')
        ? '<div class="tool-src">已合并工具 ' + st.tools + ' 个</div>' : '';

      return '' +
        '<div class="card" data-i="' + i + '">' +
          '<div class="up-head">' +
            '<span class="up-name">' + escapeHtml(u.name) + '</span>' +
            '<span class="pill ' + pillClass(status) + '">' + escapeHtml(STATUS_TEXT[status] || status) + '</span>' +
            '<span class="pill pill-unknown">' + escapeHtml(u.type) + '</span>' +
            (u.enabled ? '' : '<span class="pill pill-warn">未启用</span>') +
          '</div>' +
          '<div class="up-target">' + escapeHtml(target || '') + '</div>' +
          '<div class="tool-src">启动方式：' + escapeHtml(launch) +
            (u.autoLaunch ? ' · autoLaunch' : '') +
            (u.exposeWhenStopped ? ' · 未运行也暴露' : '') + '</div>' +
          tools + detail +
          '<div class="up-actions">' +
            (canStart ? '<button class="btn btn-primary" data-act="start">启动 / 重启</button>' : '') +
            '<button class="btn" data-act="probe">探测</button>' +
            '<button class="btn" data-act="toggle">' + (u.enabled ? '禁用' : '启用') + '</button>' +
            '<button class="btn btn-danger" data-act="remove">删除</button>' +
          '</div>' +
        '</div>';
    }).join('');
  }

  function withBusy(btn, fn) {
    if (state.busy) { return Promise.resolve(); }
    state.busy = true;
    var old = btn ? btn.textContent : '';
    if (btn) { btn.disabled = true; btn.textContent = '处理中…'; }
    return Promise.resolve()
      .then(fn)
      .catch(function (err) {
        toast('失败：' + (err && err.message ? err.message : err), 5000);
      })
      .then(function () {
        state.busy = false;
        if (btn) { btn.disabled = false; btn.textContent = old; }
      });
  }

  /** 改配置 → 写盘 → 让 daemon 立刻重载。三个动作必须成对出现。 */
  function persist(list, successMsg) {
    return NovaConfig.setUpstreams(list)
      .then(function () {
        return NovaMcp.callTool('novaai_config',
          { action: 'reload_upstreams' }, 60000);
      })
      .then(function (res) {
        var data = parseResult(res);
        if (data && data.success === false) {
          throw new Error(data.message || '重载失败');
        }
        return data;
      })
      .then(function (data) {
        state.upstreams = list;
        if (data && data.upstreams) {
          state.statusByName = {};
          data.upstreams.forEach(function (s) { state.statusByName[s.name] = s; });
        }
        renderUpstreams();
        if (successMsg) { toast(successMsg); }
      });
  }

  function handleUpstreamClick(ev) {
    var btn = ev.target.closest('button[data-act]');
    if (!btn) { return; }
    var card = btn.closest('.card');
    var idx = parseInt(card.getAttribute('data-i'), 10);
    var u = state.upstreams[idx];
    if (!u) { return; }
    var act = btn.getAttribute('data-act');

    if (act === 'probe') {
      return withBusy(btn, function () {
        return NovaMcp.callTool('novaai_config',
          { action: 'probe_upstreams', name: u.name }, 30000)
          .then(parseResult)
          .then(function (data) {
            if (data && data.success === false) { throw new Error(data.message); }
            state.statusByName = {};
            ((data && data.upstreams) || []).forEach(function (s) {
              state.statusByName[s.name] = s;
            });
            renderUpstreams();
            toast('已探测 ' + u.name);
          });
      });
    }

    if (act === 'start') {
      // 服务端的 restart_upstream = 关掉旧连接 → 需要时按 launch 拉起 → 重探。
      // 拉起逻辑只有 Go 一侧一份实现，页面不重复 am start。
      return withBusy(btn, function () {
        return NovaMcp.callTool('novaai_config',
          { action: 'restart_upstream', name: u.name }, 60000)
          .then(parseResult)
          .then(function (data) {
            if (data && data.success === false) { throw new Error(data.message); }
            return refreshStatus().then(renderUpstreams);
          });
      });
    }

    if (act === 'toggle') {
      return withBusy(btn, function () {
        var next = state.upstreams.slice();
        next[idx] = Object.assign({}, u, { enabled: !u.enabled });
        return persist(next, (next[idx].enabled ? '已启用 ' : '已禁用 ') + u.name);
      });
    }

    if (act === 'remove') {
      if (!window.confirm('删除上游 ' + u.name + '？\n它的工具会立即从工具列表里消失。')) {
        return;
      }
      return withBusy(btn, function () {
        var next = state.upstreams.filter(function (x) { return x.name !== u.name; });
        return NovaConfig.backup().then(function (bak) {
          return persist(next, '已删除 ' + u.name + (bak ? '（备份 ' + bak + '）' : ''));
        });
      });
    }
  }

  // ------------------------------------------------------------ 添加上游

  function syncFormVisibility() {
    var f = $('#addForm');
    var type = fieldValue(f, 'type');
    $$('[data-when]').forEach(function (el) {
      el.classList.toggle('is-hidden', el.getAttribute('data-when') !== type);
    });
    var lt = fieldValue(f, 'launchType');
    $$('[data-launch]').forEach(function (el) {
      el.classList.toggle('is-hidden', el.getAttribute('data-launch') !== lt);
    });
  }

  function splitArgs(text) {
    if (!text || !text.trim()) { return []; }
    // 极简分词：支持双引号包住带空格的参数。不做转义求值 —— 参数是直接
    // spawn 传给子进程的（Go 侧 exec.Command），这里只负责切分。
    var out = [];
    var cur = '';
    var quoted = false;
    for (var i = 0; i < text.length; i++) {
      var ch = text[i];
      if (ch === '"') { quoted = !quoted; continue; }
      if (!quoted && /\s/.test(ch)) {
        if (cur) { out.push(cur); cur = ''; }
        continue;
      }
      cur += ch;
    }
    if (cur) { out.push(cur); }
    return out;
  }

  /** 添加 HTTP 上游前，提示端口是否已被占用。只是提示，不是拦截。 */
  function checkPort(url) {
    var m = /^https?:\/\/([^/:]+):(\d+)/.exec(url || '');
    if (!m) { return Promise.resolve(); }
    var port = m[2];
    return NovaKsu.exec('(ss -tln 2>/dev/null || netstat -tln 2>/dev/null) | grep -c ":' + port + ' "')
      .then(function (r) {
        var n = parseInt((r.stdout || '0').trim(), 10);
        var el = $('#portHint');
        if (n > 0) {
          el.textContent = '提示：端口 ' + port + ' 当前已在监听。如果要接的正是那个服务，这是正常的；' +
            '否则说明端口被别的进程占了。';
        } else {
          el.textContent = '端口 ' + port + ' 当前没有进程在监听 —— 保存后状态大概率是「未启动」，' +
            '需要配置 launch 才能自动拉起。';
        }
        el.classList.remove('is-hidden');
      })
      .catch(function () { /* 探测失败不影响添加 */ });
  }

  /** 取表单字段。
   *
   * 必须走 form.elements 而不是 form.<name>：HTMLFormElement 自身就有
   * name / action / method 这些属性，`form.name.value` 拿到的是表单的
   * name 属性（一个字符串），而不是那个叫 name 的输入框 —— 它会静默
   * 变成 undefined，然后校验逻辑全体失效。
   */
  function field(form, name) {
    return form.elements.namedItem(name);
  }

  function fieldValue(form, name) {
    var el = field(form, name);
    return el ? el.value : '';
  }

  function fieldChecked(form, name) {
    var el = field(form, name);
    return !!(el && el.checked);
  }

  function buildUpstreamFromForm() {
    var f = $('#addForm');
    var type = fieldValue(f, 'type');
    var launchType = fieldValue(f, 'launchType');

    var u = {
      name: fieldValue(f, 'name').trim(),
      type: type,
      enabled: fieldChecked(f, 'enabled')
    };

    if (type === 'http') {
      u.url = fieldValue(f, 'url').trim();
    } else {
      u.command = fieldValue(f, 'command').trim();
      var args = splitArgs(fieldValue(f, 'args'));
      if (args.length) { u.args = args; }
    }

    var ceiling = parseInt(fieldValue(f, 'riskCeiling'), 10);
    if (!isNaN(ceiling) && ceiling > 0) { u.riskCeiling = ceiling; }

    var deny = fieldValue(f, 'denyTools').split(',')
      .map(function (s) { return s.trim(); }).filter(Boolean);
    if (deny.length) { u.denyTools = deny; }

    if (fieldChecked(f, 'autoLaunch')) { u.autoLaunch = true; }
    if (fieldChecked(f, 'exposeWhenStopped')) { u.exposeWhenStopped = true; }

    if (launchType === 'intent') {
      u.launch = { type: 'intent', package: fieldValue(f, 'package').trim() };
      var act = fieldValue(f, 'action').trim();
      var actv = fieldValue(f, 'activity').trim();
      if (act) { u.launch.action = act; }
      if (actv) { u.launch.activity = actv; }
    } else if (launchType === 'command') {
      u.launch = { type: 'command', command: fieldValue(f, 'launchCommand').trim() };
      var largs = splitArgs(fieldValue(f, 'launchArgs'));
      if (largs.length) { u.launch.args = largs; }
    }

    return u;
  }

  function submitAdd(ev) {
    ev.preventDefault();
    var u = buildUpstreamFromForm();

    // 前端只做"能立刻给出人话提示"的校验；真正的闸门是 config.Validate，
    // 它会把这些规则（以及 name 不能含 __ 这类更细的）再查一遍。
    if (!u.name) { toast('名称必填'); return; }
    if (u.name.indexOf('__') >= 0) { toast('名称不能包含 __（它是工具名前缀分隔符）'); return; }
    if (u.type === 'http' && !u.url) { toast('URL 必填'); return; }
    if (u.type === 'stdio' && !u.command) { toast('命令必填'); return; }
    if (u.launch && u.launch.type === 'intent' &&
        !u.launch.action && !u.launch.activity) {
      toast('intent 启动方式需要 action 或 activity');
      return;
    }
    if (state.upstreams.some(function (x) { return x.name === u.name; })) {
      toast('名称已存在：' + u.name);
      return;
    }

    var submit = $('#addForm button[type=submit]');
    return withBusy(submit, function () {
      return NovaConfig.backup().then(function (bak) {
        var next = state.upstreams.concat([u]);
        return persist(next, '已添加 ' + u.name + (bak ? '（备份 ' + bak + '）' : ''));
      }).then(function () {
        $('#addForm').reset();
        syncFormVisibility();
        $('#portHint').classList.add('is-hidden');
        $('#addBox').open = false;
      });
    });
  }

  // ------------------------------------------------------------ 工具目录

  function renderTools() {
    var q = ($('#toolSearch').value || '').trim().toLowerCase();
    var list = state.tools.filter(function (t) {
      if (!q) { return true; }
      return (t.name + ' ' + (t.description || '')).toLowerCase().indexOf(q) >= 0;
    });

    var groups = { '本地（NovaAI-MCP）': [] };
    list.forEach(function (t) {
      var sp = splitToolName(t.name);
      var key = sp ? ('上游 ' + sp.upstream) : '本地（NovaAI-MCP）';
      (groups[key] = groups[key] || []).push(t);
    });

    var html = '';
    Object.keys(groups).sort(function (a, b) {
      // 本地排最前，其余按名字。
      if (a.indexOf('本地') === 0) { return -1; }
      if (b.indexOf('本地') === 0) { return 1; }
      return a < b ? -1 : 1;
    }).forEach(function (key) {
      var items = groups[key];
      html += '<div class="group-title">' + escapeHtml(key) + ' · ' + items.length + '</div>';
      html += items.map(function (t) {
        var sp = splitToolName(t.name);
        return '<div class="card tool">' +
          '<span class="tool-name">' + escapeHtml(t.name) + '</span>' +
          (t.title ? '<span class="tool-src">' + escapeHtml(t.title) + '</span>' : '') +
          '<span class="tool-desc">' + escapeHtml(t.description || '（无描述）') + '</span>' +
          (sp ? '<span class="tool-src">原始工具名：' + escapeHtml(sp.tool) + '</span>' : '') +
          '</div>';
      }).join('');
    });

    $('#toolList').innerHTML = html || '<p class="muted">没有匹配的工具。</p>';

    var sel = $('#callTool');
    var prev = sel.value;
    sel.innerHTML = state.tools.map(function (t) {
      return '<option value="' + escapeHtml(t.name) + '">' + escapeHtml(t.name) + '</option>';
    }).join('');
    if (prev) { sel.value = prev; }
  }

  function reloadTools() {
    return NovaMcp.listTools().then(function (tools) {
      state.tools = tools;
      renderTools();
    });
  }

  function submitCall(ev) {
    ev.preventDefault();
    var name = $('#callTool').value;
    var raw = $('#callForm').args.value.trim() || '{}';
    var args;
    try {
      args = JSON.parse(raw);
    } catch (e) {
      toast('arguments 不是合法 JSON：' + e.message);
      return;
    }
    if (args === null || typeof args !== 'object' || Array.isArray(args)) {
      toast('arguments 必须是一个 JSON 对象');
      return;
    }

    var box = $('#callResult');
    var btn = $('#callForm button[type=submit]');
    return withBusy(btn, function () {
      box.classList.remove('is-hidden');
      box.textContent = '调用中…';
      return NovaMcp.callTool(name, args, 120000).then(function (res) {
        var head = res.ok ? '✔ 成功' : '✘ 失败';
        var payload = res.structured !== null && res.structured !== undefined
          ? JSON.stringify(res.structured, null, 2)
          : res.text;
        box.textContent = head + ' · ' + name + '\n\n' + payload;
        return refreshStatus().then(function () {
          renderUpstreams();
          // 上游被调用时可能发生懒启动，工具面可能变了。
          return reloadTools();
        });
      });
    });
  }

  // ---------------------------------------------------------------- 日志

  function loadLogFiles() {
    var dir = NovaConfig.paths().stateDir + '/audit';
    return NovaKsu.exec('ls -1t ' + dir + '/audit-*.jsonl 2>/dev/null | head -20')
      .then(function (r) {
        var files = (r.stdout || '').split('\n').map(function (s) { return s.trim(); })
          .filter(Boolean);
        var sel = $('#logFile');
        sel.innerHTML = files.length
          ? files.map(function (f) {
              return '<option value="' + escapeHtml(f) + '">' + escapeHtml(f.replace(/^.*\//, '')) + '</option>';
            }).join('')
          : '<option value="">（没有审计日志）</option>';
        return files;
      });
  }

  function loadLog() {
    var file = $('#logFile').value;
    var view = $('#logView');
    if (!file) { view.textContent = '没有可读的审计日志。'; return Promise.resolve(); }

    return NovaKsu.exec('tail -n 400 ' + file)
      .then(function (r) {
        var onlyUp = $('#logOnlyUpstream').checked;
        var lines = (r.stdout || '').split('\n').filter(Boolean);
        var out = [];
        lines.forEach(function (line) {
          var obj;
          try { obj = JSON.parse(line); } catch (e) { out.push(line); return; }
          if (onlyUp) {
            var ev = obj.event || '';
            var tool = obj.tool || '';
            if (ev.indexOf('upstream') !== 0 && tool.indexOf('upstream:') !== 0) { return; }
          }
          out.push(JSON.stringify(obj));
        });
        view.textContent = out.length ? out.join('\n') : '（没有匹配的记录）';
      });
  }

  // ---------------------------------------------------------------- 装配

  function switchTab(name) {
    $$('.tab').forEach(function (t) {
      t.classList.toggle('is-active', t.getAttribute('data-tab') === name);
    });
    $$('.panel').forEach(function (p) {
      p.classList.toggle('is-active', p.id === 'panel-' + name);
    });
    if (name === 'tools') { return reloadTools().catch(function () {}); }
    if (name === 'logs') {
      return loadLogFiles().then(loadLog).catch(function (e) { toast(e.message); });
    }
  }

  function boot() {
    $('#cfgPath').textContent = '配置：' + NovaConfig.paths().config;

    var savedBase = NovaMcp.restoreBase();
    var pm = /:(\d+)\//.exec(savedBase);
    if (pm) { $('#mcpPort').value = pm[1]; }

    // 标签页
    $$('.tab').forEach(function (t) {
      t.addEventListener('click', function () { switchTab(t.getAttribute('data-tab')); });
    });

    // 端口
    $('#mcpPort').addEventListener('change', function () {
      var p = parseInt(this.value, 10);
      if (!p || p < 1 || p > 65535) { toast('端口非法'); return; }
      NovaMcp.setBase(NovaMcp.baseForPort(p));
      toast('已切换到 ' + NovaMcp.getBase());
      refreshAll();
    });

    $('#addType').addEventListener('change', syncFormVisibility);
    $('#addLaunchType').addEventListener('change', syncFormVisibility);
    $('#addForm').addEventListener('submit', submitAdd);
    $('#addForm').addEventListener('input', function (ev) {
      if (ev.target.name === 'url') { checkPort(ev.target.value); }
    });

    $('#upstreamList').addEventListener('click', handleUpstreamClick);

    $('#toolSearch').addEventListener('input', renderTools);
    $('#btnReloadTools').addEventListener('click', function () {
      withBusy(this, reloadTools);
    });
    $('#callForm').addEventListener('submit', submitCall);

    $('#logFile').addEventListener('change', loadLog);
    $('#logOnlyUpstream').addEventListener('change', loadLog);
    $('#btnReloadLogs').addEventListener('click', function () {
      withBusy(this, function () { return loadLogFiles().then(loadLog); });
    });

    $('#btnRefreshAll').addEventListener('click', function () {
      withBusy(this, refreshAll);
    });

    syncFormVisibility();

    // 先探桥：不在 KernelSU 里时，除了"看"什么都做不了，要立刻说清楚。
    return NovaKsu.ready().then(function (k) {
      state.ksuOk = !!k;
      state.bridgeType = NovaKsu.type();
      if (!state.ksuOk) {
        showBanner('未检测到 KernelSU 桥：页面可以浏览，但读写 config.json、探测上游' +
          '等操作都不可用。请在 KernelSU 管理器里打开本模块的 WebUI。');
      }
      return refreshAll();
    });
  }

  function refreshAll() {
    return checkService()
      .then(function () { return loadConfig(); })
      .then(function () { return refreshStatus(); })
      .then(function () {
        renderUpstreams();
        showBanner('');
        return reloadTools().catch(function () {});
      })
      .catch(function (err) {
        setSvc('未连接', 'pill-err');
        // 排障三要素一起给：走了哪条传输层、桥是什么、错误原文。
        // 初版只报"连不上"，而三条传输路径各自有不同的失败原因
        // （桥调用约定 / 混合内容 / CORS / Origin 拒绝），不报路径就没法定位。
        var tp = NovaMcp.transport();
        var hint = (tp === 'ksu-curl')
          ? '走的是桥 + curl（与 CORS 无关）：确认 daemon 在运行、端口与 config.json 的 listen 一致。'
          : (tp === 'fetch'
              ? 'fetch 只在页面与接口同源时可用；请从 KernelSU 管理器打开本 WebUI。'
              : '当前没有可用的传输层：请在 KernelSU 管理器里打开本 WebUI。');
        showBanner('无法连接 MCP 服务 ' + NovaMcp.getBase() +
          '（传输层：' + tp + '；桥：' + (state.bridgeType || '无') + '）：' +
          (err && err.message ? err.message : err) + '。' + hint);
        return loadConfig().then(renderUpstreams).catch(function () {});
      });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
