/*
 * lib/kernelsu.js —— KernelSU API 的唯一封装。
 *
 * # 与官方 npm 包（kernelsu@3.0.2）的关系
 *
 * 官方库（registry.npmjs.org/kernelsu，KernelSU 仓库 userspace/ksuweb）
 * 的 exec 是这样实现的（照抄其 index.js，逐字核对过）：
 *
 *   window[callbackFuncName] = (errno, stdout, stderr) => { ... };
 *   try { ksu.exec(command, JSON.stringify(options), callbackFuncName); }
 *   catch (error) { ... }
 *
 * 即：**外层 Promise，内层三参数回调**。本文件是它的双模式超集 ——
 *   1. 回调约定与官方完全一致（参数形态、回调签名、window 注册与清理）；
 *   2. 额外兼容把 exec 实现成 Promise 风格的管理器（官方源码没有这条路，
 *      但个别管理器提供）；
 *   3. 额外加回调超时守卫（官方没有 —— 回调不来就永远挂起）。
 *
 * 为什么不直接用官方 npm 包：本页面必须在 file:// 与虚拟源两种环境下
 * 以经典脚本运行（无打包器、零依赖），而官方包是 ES module（官方指南
 * 自己也建议用 parcel 打包）。API 面与官方对齐（见下方各函数与
 * docs/webui.md 的对照表），将来若引入打包器可直接替换为官方包。
 *
 * # 为什么必须按回调约定调用（血泪教训）
 *
 * WebView 的 JS->Java 桥对参数个数不匹配**不报错**：缺的参数按 null 传下去。
 * 曾按 Promise 风格调 `k.exec(command)`，命令根本没执行、回调也没来，
 * `Promise.resolve(undefined)` 被规范化成 `{errno:0, stdout:''}`，
 * 上层把"空输出"误读成"config.json 文件不存在"。
 * **参数个数不匹配在这里是静默失败，必须按回调约定调用。**
 */
(function (global) {
  'use strict';

  var bridge = null;      // 探测成功后的桥对象
  var bridgeType = null;  // 桥的来源，出错时报给用户
  var readyPromise = null;
  var callbackCounter = 0;

  function fromWindowKsu() {
    var k = global.ksu;
    return (k && typeof k.exec === 'function') ? { k: k, type: 'window.ksu' } : null;
  }

  function fromWindowKernelsu() {
    var k = global.kernelsu;
    return (k && typeof k.exec === 'function') ? { k: k, type: 'window.kernelsu' } : null;
  }

  function fromModuleImport() {
    // 裸标识符 'kernelsu' 只有 KernelSU 的 WebView 才解析得到；
    // 解析不到就 reject —— 那只意味着"当前不在 KernelSU 里"。
    return import('kernelsu').then(function (m) {
      return (m && typeof m.exec === 'function') ? { k: m, type: 'module:kernelsu' } : null;
    }).catch(function () { return null; });
  }

  /** 探测桥。只探测一次，后续调用复用同一个 Promise。 */
  function ready() {
    if (bridge) { return Promise.resolve(bridge); }
    if (readyPromise) { return readyPromise; }

    var direct = fromWindowKsu() || fromWindowKernelsu();
    if (direct) {
      bridge = direct.k;
      bridgeType = direct.type;
      readyPromise = Promise.resolve(bridge);
      return readyPromise;
    }
    readyPromise = fromModuleImport().then(function (found) {
      bridge = found ? found.k : null;
      bridgeType = found ? found.type : null;
      return bridge;
    });
    return readyPromise;
  }

  /** 是否已在 KernelSU 里（必须在 ready() 完成后读才有意义）。 */
  function available() { return !!bridge; }

  /** 桥来自哪里。诊断用。 */
  function type() { return bridgeType; }

  // -------------------------------------------------- 结果规范化（共用）

  function normalize(r) {
    r = r || {};
    return {
      errno: typeof r.errno === 'number' ? r.errno : (r.code || 0),
      stdout: r.stdout || '',
      stderr: r.stderr || ''
    };
  }

  function execViaCallback(k, command, options) {
    return new Promise(function (resolve, reject) {
      var cb = 'nova_callback_' + Date.now() + '_' + (callbackCounter++);
      var settled = false;

      // 官方实现没有超时：回调不来就永远挂起。这里给 120 秒守卫 ——
      // 足够容纳最慢的合法命令（tail 大日志），又不会无限挂死页面。
      var timer = setTimeout(function () {
        if (settled) { return; }
        settled = true;
        delete global[cb];
        var e = new Error('KernelSU 桥没有回调 —— 命令没有被执行（桥类型：' + bridgeType + '）');
        e.__noCallback = true;
        reject(e);
      }, 120000);

      global[cb] = function (errno, stdout, stderr) {
        if (settled) { return; }
        settled = true;
        clearTimeout(timer);
        delete global[cb];
        resolve({ errno: errno, stdout: stdout || '', stderr: stderr || '' });
      };

      try {
        // 与官方 index.js 逐字一致的调用形态。
        var rv = k.exec(command, JSON.stringify(options || {}), cb);
        // 桥若是 Promise 风格，返回值就是 thenable —— 就地采用，不等回调。
        if (rv && typeof rv.then === 'function') {
          if (settled) { return; }
          settled = true;
          clearTimeout(timer);
          delete global[cb];
          Promise.resolve(rv).then(function (r) { resolve(normalize(r)); }, reject);
        }
      } catch (e) {
        if (settled) { return; }
        settled = true;
        clearTimeout(timer);
        delete global[cb];
        e.__syncThrow = true;
        reject(e);
      }
    });
  }

  /** Promise 风格的兜底（回调方式同步抛错时才走）。 */
  function execViaPromise(k, command, options) {
    return Promise.resolve().then(function () {
      return k.exec(command, JSON.stringify(options || {}));
    }).then(normalize);
  }

  /**
   * 执行一条 shell 命令（root）。返回 Promise<{errno, stdout, stderr}>。
   *
   * options: { cwd, env } —— 与官方 npm 包的 ExecOptions 一致，序列化后
   * 作为桥的第二个参数。
   *
   * errno 非 0 不抛异常 —— 调用方通常需要读 stderr 判断"文件不存在"这类
   * 正常情况。桥彻底缺失才抛，因为那时什么都做不了。
   */
  function exec(command, options) {
    return ready().then(function (k) {
      if (!k) {
        throw new Error('未检测到 KernelSU 桥：请在 KernelSU 管理器里打开本页面');
      }
      return execViaCallback(k, command, options).catch(function (e) {
        // 只有"同步抛错"才值得换一种调用方式重试；回调没来（超时）说明
        // 桥根本没执行命令，重试只会再等一遍。
        if (e && e.__noCallback) { throw e; }
        return execViaPromise(k, command, options);
      });
    });
  }

  // ------------------------------------------------------- spawn（官方约定）

  function Stdio() { this.listeners = {}; }
  Stdio.prototype.on = function (event, listener) {
    (this.listeners[event] = this.listeners[event] || []).push(listener);
  };
  Stdio.prototype.emit = function (event) {
    var ls = this.listeners[event] || [], args = Array.prototype.slice.call(arguments, 1);
    for (var i = 0; i < ls.length; i++) { ls[i].apply(null, args); }
  };

  function ChildProcess() {
    this.listeners = {};
    this.stdin = new Stdio();
    this.stdout = new Stdio();
    this.stderr = new Stdio();
  }
  ChildProcess.prototype.on = Stdio.prototype.on;
  ChildProcess.prototype.emit = Stdio.prototype.emit;

  /**
   * 流式启动一个 root 进程。与官方 npm 包的 spawn 同一约定：
   *
   *   window[childCallbackName] = child;
   *   ksu.spawn(command, JSON.stringify(args), JSON.stringify(options), childCallbackName);
   *
   * 管理器随后按名字找到 child，向 stdout/stderr emit 'data'、
   * 向 child emit 'exit'/'error'。退出后自动清理 window 上的引用。
   *
   * 用途：日志页的实时 follow（logcat 不经 tail 一次取）。
   */
  function spawn(command, args, options) {
    if (Object.prototype.toString.call(args) !== '[object Array]') {
      options = args;
      args = [];
    }
    if (!options) { options = {}; }

    var child = new ChildProcess();
    var childCallbackName = 'nova_spawn_' + Date.now() + '_' + (callbackCounter++);
    global[childCallbackName] = child;

    child.on('exit', function () { delete global[childCallbackName]; });
    child.on('error', function () { delete global[childCallbackName]; });

    try {
      // ready() 是异步的，而 spawn 按官方契约同步返回 ChildProcess。
      // 桥没就绪时先排队，ready 后立刻发起。
      ready().then(function (k) {
        if (!k || typeof k.spawn !== 'function') {
          child.emit('error', new Error('当前 KernelSU 桥不支持 spawn'));
          return;
        }
        k.spawn(command, JSON.stringify(args || []), JSON.stringify(options), childCallbackName);
      }).catch(function (e) { child.emit('error', e); });
    } catch (error) {
      child.emit('error', error);
      delete global[childCallbackName];
    }
    return child;
  }

  // ------------------------------------------------------- 其余官方 API

  /** 显示一条提示。桥不支持时退化为 console。 */
  function toast(message) {
    return ready().then(function (k) {
      if (k && typeof k.toast === 'function') {
        try { return k.toast(String(message)); } catch (e) { /* 退化到 console */ }
      }
      console.log('[toast] ' + message);
    });
  }

  /**
   * 列出已安装应用的包名。type: "user" | "system" | "all"（官方签名）。
   * 返回 Promise<string[]>。配合 ksu://icon/{包名} 可取应用图标。
   */
  function listPackages(type) {
    return ready().then(function (k) {
      if (!k || typeof k.listPackages !== 'function') { return []; }
      return Promise.resolve(k.listPackages(type)).then(function (r) {
        if (typeof r === 'string') {
          try { r = JSON.parse(r); } catch (e) { return []; }
        }
        return Array.isArray(r) ? r : [];
      }).catch(function () { return []; });
    });
  }

  /**
   * 批量取应用信息（packageName/versionName/appLabel/isSystem/uid）。
   * 官方签名 getPackagesInfo(packages: string[])。可用于启动表单里
   * 展示应用名与图标，替代让用户手填包名。
   */
  function getPackagesInfo(packages) {
    return ready().then(function (k) {
      if (!k || typeof k.getPackagesInfo !== 'function') { return []; }
      var arg = Array.isArray(packages) ? JSON.stringify(packages) : packages;
      return Promise.resolve(k.getPackagesInfo(arg)).then(function (r) {
        if (typeof r === 'string') {
          try { r = JSON.parse(r); } catch (e) { return []; }
        }
        return Array.isArray(r) ? r : [];
      }).catch(function () { return []; });
    });
  }

  /** 模块信息（版本、id）。取不到时返回 null。 */
  function moduleInfo() {
    return ready().then(function (k) {
      if (!k || typeof k.moduleInfo !== 'function') { return null; }
      return Promise.resolve(k.moduleInfo()).catch(function () { return null; });
    });
  }

  global.NovaKsu = {
    ready: ready,
    available: available,
    type: type,
    exec: exec,
    spawn: spawn,
    toast: toast,
    listPackages: listPackages,
    getPackagesInfo: getPackagesInfo,
    moduleInfo: moduleInfo
  };
})(window);
