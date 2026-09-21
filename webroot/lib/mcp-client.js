/*
 * lib/mcp-client.js —— MCP 客户端（JSON-RPC over HTTP）。
 *
 * 走的是 http://127.0.0.1:5322/mcp，没有特权通道、没有私有接口。
 *
 * # 为什么传输层有两套，而且 fetch 是**结构性不可用**的那套
 *
 * 页面由 KernelSU 的 WebUI 以虚拟源载入（本机 Re:KernelSU 是
 * https://appassets.androidplatform.net），与 http://127.0.0.1:5322 不同源。
 * 从 HTTPS 源 fetch HTTP 本机地址要过三道闸，每一道都独立致命：
 *
 *   1. 混合内容：HTTPS 页面请求 HTTP 资源，WebView 默认禁止；
 *   2. CORS 预检：Content-Type: application/json 触发 OPTIONS 预检，
 *      daemon 不返回任何 Access-Control-* 头 —— 预检必失败；
 *   3. Origin 拒绝：即使前两道都放行，跨源 fetch 必带 Origin 头，而
 *      daemon 的 hostMiddleware 对任何带 Origin 头的请求一律 -32001。
 *
 * 三道闸合起来：浏览器看到的永远是 "TypeError: Failed to fetch"，
 * 服务其实活得好好的。这不是配置问题，是这条传输路径在当前设计下
 * 不可能通 —— 所以 fetch 只在同源（daemon 自己托管 webroot）时启用。
 *
 * 主路径是桥 + curl：命令在 root shell 里执行，没有浏览器、没有 Origin、
 * 没有 CORS。device 上已验证 /system/bin/curl 存在且 daemon 可达。
 *
 * # 请求体为什么用 printf 而不是 heredoc
 *
 * KSU 桥的 shell 执行器对多行 heredoc 会报 "unclosed"（设备上实测）。
 * printf 单行搞定，顺带让整段脚本变成一行 —— 桥是否保留换行都不再影响结果。
 */
(function (global) {
  'use strict';

  var DEFAULT_BASE = 'http://127.0.0.1:5322/mcp';
  var baseUrl = DEFAULT_BASE;

  // 与 lib/config.js 的 STATE_DIR 保持一致。
  var STATE_DIR = '/data/adb/novaai-mcp';
  var TMP_DIR = STATE_DIR + '/tmp';

  var nextId = 1;
  var lastTransport = '未发起';

  // 只接受形如 http(s)://host:port/path 的地址。地址会被拼进 shell 命令，
  // 所以这里必须挡死引号、空格、分号这类能改变命令语义的字符。
  var URL_RE = /^https?:\/\/[A-Za-z0-9._-]+:\d{1,5}\/[A-Za-z0-9._\-\/]*$/;

  function setBase(url) {
    baseUrl = url;
    try { global.localStorage.setItem('novaai.mcpBase', url); } catch (e) { /* 隐私模式下忽略 */ }
  }

  function restoreBase() {
    try {
      var saved = global.localStorage.getItem('novaai.mcpBase');
      if (saved) { baseUrl = saved; }
    } catch (e) { /* 忽略 */ }
    return baseUrl;
  }

  function getBase() { return baseUrl; }

  function assertSafeBase(url) {
    if (!URL_RE.test(url)) {
      throw new Error('地址不合法，拒绝发请求：' + url);
    }
    return url;
  }

  /** 便利方法：拼 listen 地址。 */
  function baseForPort(port) {
    return 'http://127.0.0.1:' + port + '/mcp';
  }

  // ------------------------------------------------------------ 通用小件

  function withTimeout(promise, ms, msg) {
    return new Promise(function (resolve, reject) {
      var settled = false;
      var timer = setTimeout(function () {
        if (settled) { return; }
        settled = true;
        reject(new Error(msg + '（' + Math.round(ms / 1000) + ' 秒）'));
      }, ms);

      Promise.resolve(promise).then(function (v) {
        if (settled) { return; }
        settled = true;
        clearTimeout(timer);
        resolve(v);
      }, function (e) {
        if (settled) { return; }
        settled = true;
        clearTimeout(timer);
        reject(e);
      });
    });
  }

  function noBridgeError() {
    var e = new Error('未检测到 KernelSU 桥');
    e.__noBridge = true;
    return e;
  }

  /** fetch 只在"页面与接口同源"时有意义（daemon 自己托管 webroot 的情形）。 */
  function sameOriginWithBase() {
    try {
      return global.location && global.location.origin === new URL(baseUrl).origin;
    } catch (e) { return false; }
  }

  // ------------------------------------------------------ 传输层 A：桥 + curl

  function buildScript(url, body, timeoutMs, mark) {
    var secs = Math.max(1, Math.ceil((timeoutMs || 60000) / 1000));
    var json = JSON.stringify(body);

    if (json.indexOf('\n') >= 0 || json.indexOf('\r') >= 0) {
      // JSON.stringify 不会产出真实换行；真出现了说明有东西在骗我们。
      throw new Error('拒绝执行：请求体含换行，无法安全嵌入脚本');
    }

    // 单引号字符串里只有 ' 一个字符需要转义（'\''）。$ 和反引号在单引号里
    // 都是字面量；\ 也是 —— JSON.stringify 产出的 \" 与 \\ 原样落盘即可。
    var safeJson = json.replace(/'/g, "'\\''");

    // 全程单行 + 分号串联：桥是否保留换行都不影响语义。
    return 'mkdir -p ' + TMP_DIR + '; ' +
      'req=' + TMP_DIR + '/mcp-req.$$.json; ' +
      'res=' + TMP_DIR + '/mcp-res.$$.json; ' +
      "printf '%s' '" + safeJson + "' > \"$req\"; " +
      "curl -sS -m " + secs + " -o \"$res\" -w '%{http_code}' " +
      "-X POST '" + url + "' " +
      "-H 'Content-Type: application/json' " +
      "--data-binary @\"$req\"; " +
      'rc=$?; echo; echo ' + mark + '; ' +
      'cat "$res" 2>/dev/null; echo; echo ' + mark + '_RC=$rc; ' +
      'rm -f "$req" "$res"';
  }

  function curlFailure(rc, httpCode, body) {
    var known = {
      6: '域名解析失败',
      7: '连不上服务（端口没人监听？）',
      28: '请求超时',
      52: '服务返回了空响应',
      56: '接收响应失败',
      127: '找不到 curl（这台设备没有 /system/bin/curl？）'
    };
    var why = known[rc] || ('curl 退出码 ' + rc);
    var extra = (body || '').trim();
    return new Error(why + (httpCode ? '（HTTP ' + httpCode + '）' : '') +
      (extra ? '：' + extra.slice(0, 200) : ''));
  }

  function parseShellOutput(stdout, mark) {
    // stdout 形如：200\n<mark>\n<body>\n\n<mark>_RC=0\n
    var parts = String(stdout).split(mark);
    if (parts.length < 3) {
      throw new Error('桥返回了无法解析的输出：' + String(stdout).slice(0, 200));
    }

    var httpCode = parseInt((parts[0] || '').trim(), 10) || 0;
    var body = (parts[1] || '').replace(/^\n/, '').replace(/\n$/, '');
    var m = /_RC=(-?\d+)/.exec(parts[2] || '');
    var rc = m ? parseInt(m[1], 10) : 0;

    if (rc !== 0 || !body.trim()) {
      throw new Error(curlFailure(rc, httpCode, body).message);
    }

    try {
      return JSON.parse(body);
    } catch (e) {
      throw new Error('响应不是 JSON（HTTP ' + httpCode + '）：' + body.slice(0, 200));
    }
  }

  function viaShell(body, timeoutMs) {
    var script;
    var mark = '---NOVA_MCP_' + Date.now().toString(36) +
               Math.random().toString(36).slice(2, 8) + '---';
    try {
      script = buildScript(assertSafeBase(baseUrl), body, timeoutMs, mark);
    } catch (e) {
      return Promise.reject(e);
    }

    var exec = global.NovaKsu.exec(script).then(function (r) {
      r = r || {};
      var out = r.stdout || '';
      // 桥自己都跑不起来时 stdout 是空的，这时把 stderr 原样带出去，
      // 比报一句"解析失败"有用得多。
      if (!out.trim()) {
        if (r.errno !== 0) {
          var e = new Error('桥执行失败：' + (r.stderr.trim() || ('errno=' + r.errno)));
          e.__noBridge = true;
          throw e;
        }
        throw new Error('桥没有返回任何输出（exec 调用约定可能不匹配）');
      }
      return parseShellOutput(out, mark);
    });

    return withTimeout(exec, (timeoutMs || 60000) + 10000, '桥执行超时');
  }

  // ------------------------------------------------------ 传输层 B：fetch

  function viaFetch(body, timeoutMs) {
    var ctrl = (typeof AbortController !== 'undefined') ? new AbortController() : null;
    if (ctrl && timeoutMs) {
      setTimeout(function () { ctrl.abort(); }, timeoutMs);
    }

    return fetch(assertSafeBase(baseUrl), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      signal: ctrl ? ctrl.signal : undefined
    }).then(function (resp) {
      // 中间件与协议层一律用 JSON-RPC error 表达拒绝，HTTP 状态恒为 200。
      // 所以状态码异常本身就是"有东西不对"的信号，要单独报出来。
      return resp.text().then(function (text) {
        try {
          return JSON.parse(text);
        } catch (e) {
          throw new Error('响应不是 JSON（HTTP ' + resp.status + '）：' + text.slice(0, 200));
        }
      });
    }).catch(function (err) {
      if (err && err.name === 'AbortError') {
        throw new Error('请求超时（' + (timeoutMs / 1000) + ' 秒）');
      }
      throw new Error('连不上服务 ' + baseUrl + '：' + (err && err.message ? err.message : err));
    });
  }

  // ------------------------------------------------------------ 对外接口

  /** 最近一次请求实际走的传输方式。诊断用。 */
  function transport() {
    return lastTransport;
  }

  /**
   * 发一次 JSON-RPC 请求。
   *
   * 传输层选择（顺序即优先级）：
   *   1. KernelSU 桥可用        -> 桥 + curl（root shell，无 CORS 问题）；
   *   2. 页面与接口同源         -> fetch（daemon 自己托管 webroot 的情形）；
   *   3. 都不是（普通浏览器打开）-> 明确报错，不做注定失败的 fetch。
   */
  function request(method, params, timeoutMs) {
    var body = { jsonrpc: '2.0', id: nextId++, method: method };
    if (params !== undefined) { body.params = params; }

    return global.NovaKsu.ready().then(function (k) {
      if (k) {
        lastTransport = 'ksu-curl';
        return viaShell(body, timeoutMs);
      }
      if (sameOriginWithBase()) {
        lastTransport = 'fetch';
        return viaFetch(body, timeoutMs);
      }
      lastTransport = '不可用';
      throw new Error('页面不在 KernelSU 管理器里，也不与 ' + baseUrl +
        ' 同源：fetch 直连会被 WebView 的混合内容与 CORS 拦截（服务本身是好的）。' +
        '请在 KernelSU 管理器中打开本模块的 WebUI。');
    }).then(function (r) {
      if (r && r.error) {
        var e = new Error(r.error.message || ('JSON-RPC 错误 ' + r.error.code));
        e.__rpcCode = r.error.code;
        throw e;
      }
      return r;
    });
  }

  /** tools/list。上游工具以 `{upstream}__{tool}` 混在同一个数组里。 */
  function listTools() {
    return request('tools/list', undefined, 15000).then(function (r) {
      return (r.result && r.result.tools) || [];
    });
  }

  /**
   * tools/call。
   *
   * 统一返回 { ok, text, structured, raw }：
   *   ok      = 不是协议错误、且 result.isError 不为 true
   *   text    = content 里所有 text 块拼起来（通常就是结果的 JSON 串）
   *   raw     = 原始响应，排障时用
   */
  function callTool(name, args, timeoutMs) {
    return request('tools/call', { name: name, arguments: args || {} }, timeoutMs || 60000)
      .then(function (r) {
        if (r.error) {
          return { ok: false, text: r.error.message || 'JSON-RPC 错误',
                   structured: null, raw: r, protocolError: true };
        }
        var res = r.result || {};
        var text = (res.content || []).map(function (c) {
          return c && typeof c.text === 'string' ? c.text : '';
        }).join('\n');
        return {
          ok: res.isError !== true,
          text: text,
          structured: res.structuredContent || null,
          raw: r,
          protocolError: false
        };
      });
  }

  global.NovaMcp = {
    DEFAULT_BASE: DEFAULT_BASE,
    getBase: getBase,
    setBase: setBase,
    restoreBase: restoreBase,
    baseForPort: baseForPort,
    transport: transport,
    request: request,
    listTools: listTools,
    callTool: callTool
  };
})(window);
