/*
 * lib/mcp-client.js —— 一个极小的 MCP 客户端（JSON-RPC over HTTP）。
 *
 * WebUI 就是本服务的一个普通 MCP 客户端：它走的是同一个
 * http://127.0.0.1:5322/mcp，没有特权通道、没有私有接口。
 * 这条约束是有意的 —— 页面能做的事，任何同网段的 MCP 客户端也能做，
 * 所以 WebUI 不需要（也不应该有）独立的权限模型。
 *
 * 本服务不鉴权，因此不发任何认证头。
 */
(function (global) {
  'use strict';

  // 默认端口。与 config.Default().listen 一致；若用户改过 listen，
  // 页面顶部的端口框可以覆盖它，并且会持久化在 localStorage 里。
  var DEFAULT_BASE = 'http://127.0.0.1:5322/mcp';
  var baseUrl = DEFAULT_BASE;

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

  var nextId = 1;

  /**
   * 发一次 JSON-RPC 请求。
   *
   * 与协议层的约定（见 docs/errors.md）：
   *   - 请求本身不合法 → 顶层 error；
   *   - 工具执行失败   → result.isError = true，原因在 content[0].text。
   * 两者都在返回值里原样带出，由调用方决定怎么显示。
   */
  function request(method, params, timeoutMs) {
    var id = nextId++;
    var ctrl = (typeof AbortController !== 'undefined') ? new AbortController() : null;
    var timer = null;
    if (ctrl && timeoutMs) {
      timer = setTimeout(function () { ctrl.abort(); }, timeoutMs);
    }

    var body = { jsonrpc: '2.0', id: id, method: method };
    if (params !== undefined) { body.params = params; }

    return fetch(baseUrl, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      signal: ctrl ? ctrl.signal : undefined
    }).then(function (resp) {
      if (timer) { clearTimeout(timer); }
      // 中间件与协议层一律用 JSON-RPC error 表达拒绝，HTTP 状态恒为 200。
      // 所以状态码异常本身就是"有东西不对"的信号，要单独报出来。
      return resp.text().then(function (text) {
        var json;
        try {
          json = JSON.parse(text);
        } catch (e) {
          throw new Error('响应不是 JSON（HTTP ' + resp.status + '）：' + text.slice(0, 200));
        }
        return json;
      });
    }).catch(function (err) {
      if (timer) { clearTimeout(timer); }
      if (err && err.name === 'AbortError') {
        throw new Error('请求超时（' + (timeoutMs / 1000) + ' 秒）');
      }
      throw new Error('连不上服务 ' + baseUrl + '：' + (err && err.message ? err.message : err));
    });
  }

  /** tools/list。上游工具以 `{upstream}__{tool}` 混在同一个数组里。 */
  function listTools() {
    return request('tools/list', undefined, 15000).then(function (r) {
      if (r.error) { throw new Error(r.error.message || 'tools/list 失败'); }
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

  /** 便利方法：拼 listen 地址。 */
  function baseForPort(port) {
    return 'http://127.0.0.1:' + port + '/mcp';
  }

  global.NovaMcp = {
    DEFAULT_BASE: DEFAULT_BASE,
    getBase: getBase,
    setBase: setBase,
    restoreBase: restoreBase,
    baseForPort: baseForPort,
    request: request,
    listTools: listTools,
    callTool: callTool
  };
})(window);
