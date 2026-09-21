/*
 * lib/kernelsu.js —— KernelSU API 的唯一封装。
 *
 * # 桥的真实调用约定（在本机 Re:KernelSU 上验证，不是猜的）
 *
 *   window.ksu.exec(command, optionsJson, callbackName)
 *   callback(errno, stdout, stderr)
 *
 * 两条证据：
 *   1. 管理器 APK（com.resukisu.resukisu）里有 com.resukisu.zako.IKsuInterface，
 *      暴露 exec / spawn / toast / fullScreen / moduleInfo / listPackages，
 *      通过 addJavascriptInterface 注入；
 *   2. 同一台设备上已验证可用的模块（proxypin-cert-installer）就是这么调的。
 *
 * 历史教训：本文件初版按"Promise 风格"调 `k.exec(command)`。对三参数的
 * Java 方法少传参数时，WebView 的 JS 桥不会报错，而是把缺的参数当 null 传下去
 * —— 于是命令根本没执行、回调也没来，`Promise.resolve(undefined)` 被规范化成
 * {errno:0, stdout:''}，上层把"空输出"误读成"文件不存在"。
 * **参数个数不匹配在这里是静默失败，必须按回调约定调用。**
 *
 * 兼容性策略：先按回调约定调；如果桥其实是 Promise 风格（返回 thenable），
 * 直接改用返回值；如果同步抛错（Promise 风格的桥校验参数个数），退回
 * Promise 模式。三条路都收口到同一个 {errno, stdout, stderr}。
 */
(function (global) {
  'use strict';

  var bridge = null;      // 探测成功后的桥对象
  var bridgeType = null;  // 桥的来源，出错时报给用户
  var readyPromise = null;

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

  /** 回调约定。返回 Promise<{errno, stdout, stderr}>。 */
  function execViaCallback(k, command) {
    return new Promise(function (resolve, reject) {
      var cb = '_nova_cb_' + Date.now().toString(36) + Math.random().toString(36).slice(2, 8);
      var settled = false;

      var timer = setTimeout(function () {
        if (settled) { return; }
        settled = true;
        delete global[cb];
        var e = new Error('KernelSU 桥没有回调 ' + cb + ' —— 命令没有被执行。' +
          '桥类型：' + bridgeType);
        e.__noCallback = true;
        reject(e);
      }, 30000);

      global[cb] = function (errno, stdout, stderr) {
        if (settled) { return; }
        settled = true;
        clearTimeout(timer);
        delete global[cb];
        resolve({
          errno: typeof errno === 'number' ? errno : 0,
          stdout: stdout || '',
          stderr: stderr || ''
        });
      };

      try {
        var rv = k.exec(command, '{}', cb);
        // 桥如果是 Promise 风格，这里的返回值就是 thenable —— 直接采用，
        // 不等回调（否则要白等一个超时周期）。
        if (rv && typeof rv.then === 'function') {
          if (settled) { return; }
          settled = true;
          clearTimeout(timer);
          delete global[cb];
          Promise.resolve(rv).then(function (r) {
            r = r || {};
            resolve({
              errno: typeof r.errno === 'number' ? r.errno : (r.code || 0),
              stdout: r.stdout || '',
              stderr: r.stderr || ''
            });
          }, function (err) {
            global[cb] = undefined;
            reject(err);
          });
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
  function execViaPromise(k, command) {
    return Promise.resolve().then(function () {
      return k.exec(command);
    }).then(function (r) {
      r = r || {};
      return {
        errno: typeof r.errno === 'number' ? r.errno : (r.code || 0),
        stdout: r.stdout || '',
        stderr: r.stderr || ''
      };
    });
  }

  /**
   * 执行一条 shell 命令。返回 Promise<{errno, stdout, stderr}>。
   *
   * errno 非 0 不抛异常 —— 调用方通常需要读 stderr 判断"文件不存在"这类
   * 正常情况。桥彻底缺失才抛，因为那时什么都做不了。
   */
  function exec(command) {
    return ready().then(function (k) {
      if (!k) {
        throw new Error('未检测到 KernelSU 桥：请在 KernelSU 管理器里打开本页面');
      }
      return execViaCallback(k, command).catch(function (e) {
        // 只有"同步抛错"才值得换一种调用方式重试；回调没来（超时）说明
        // 桥根本没执行命令，重试只会再等一遍。
        if (e && e.__noCallback) { throw e; }
        return execViaPromise(k, command);
      });
    });
  }

  /** 显示一条提示。桥不支持时退化为 console。 */
  function toast(message) {
    return ready().then(function (k) {
      if (k && typeof k.toast === 'function') {
        try { return k.toast(String(message)); } catch (e) { /* 退化到 console */ }
      }
      console.log('[toast] ' + message);
    });
  }

  /** 列出已安装应用的包名（用于 intent 类型的 launch 表单）。 */
  function listPackages() {
    return ready().then(function (k) {
      if (!k || typeof k.listPackages !== 'function') { return []; }
      return Promise.resolve(k.listPackages()).then(function (r) {
        return Array.isArray(r) ? r : [];
      });
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
    toast: toast,
    listPackages: listPackages,
    moduleInfo: moduleInfo
  };
})(window);
