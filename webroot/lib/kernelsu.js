/*
 * lib/kernelsu.js —— KernelSU API 的唯一封装。
 *
 * 为什么需要这一层（而不是在 app.js 里直接 import 'kernelsu'）：
 *
 * 1. **入口有多种。** KernelSU 的 WebUI 桥在不同版本里叫法不同 ——
 *    新版本把 `window.ksu` 注入页面，同时也提供名为 `kernelsu` 的模块
 *    供 `import` 使用。写死一种会在另一种上直接白屏。
 *
 * 2. **加载方式受限。** 本 WebUI 会被 WebView 以 file:// 打开，ES module
 *    的静态 import 在 file:// 下可能被 CORS 拦掉。所以全部文件都是经典
 *    脚本（挂全局），对 kernelsu 的 `import()` 是**运行时动态**尝试，
 *    失败也只是退化，不影响页面其余部分。
 *
 * 3. **必须能自证失败。** 在普通浏览器里打开本页面时，我们要给出
 *    "请在 KernelSU 管理器里打开"这句人话，而不是一串 TypeError。
 */
(function (global) {
  'use strict';

  var bridge = null; // { exec, spawn?, toast? } —— 探测成功后缓存

  function fromWindowKsu() {
    var k = global.ksu;
    if (k && typeof k.exec === 'function') { return k; }
    return null;
  }

  function fromWindowKernelsu() {
    var k = global.kernelsu;
    if (k && typeof k.exec === 'function') { return k; }
    return null;
  }

  function fromModuleImport() {
    // 动态 import 是异步的，这里返回 Promise。
    // 裸标识符 'kernelsu' 由 KernelSU 的 WebView 解析；解析不到会 reject，
    // 我们在调用侧吞掉这个错误 —— 它只意味着"当前不在 KernelSU 里"。
    return import('kernelsu').then(function (m) {
      if (m && typeof m.exec === 'function') { return m; }
      return null;
    }).catch(function () { return null; });
  }

  /**
   * 探测可用的桥。返回 Promise<bridge|null>。
   * 结果会缓存，重复调用不会重复探测。
   */
  function ready() {
    if (bridge) { return Promise.resolve(bridge); }

    var direct = fromWindowKsu() || fromWindowKernelsu();
    if (direct) { bridge = direct; return Promise.resolve(bridge); }

    return fromModuleImport().then(function (m) {
      bridge = m;
      return m;
    });
  }

  /** 是否运行在 KernelSU 的 WebUI 里。 */
  function available() {
    return !!bridge;
  }

  /**
   * 执行一条 shell 命令。返回 { errno, stdout, stderr }。
   *
   * errno 非 0 不抛异常 —— 调用方通常需要读 stderr 判断"文件不存在"这类
   * 正常情况。真正的桥缺失才抛，因为那时什么都做不了。
   */
  function exec(command) {
    return ready().then(function (k) {
      if (!k) {
        throw new Error('未检测到 KernelSU 桥：请在 KernelSU 管理器里打开本页面');
      }
      // 不同版本返回的字段名略有差异，统一成 errno/stdout/stderr。
      return Promise.resolve(k.exec(command)).then(function (r) {
        r = r || {};
        return {
          errno: typeof r.errno === 'number' ? r.errno : (r.code || 0),
          stdout: r.stdout || '',
          stderr: r.stderr || ''
        };
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
    exec: exec,
    toast: toast,
    listPackages: listPackages,
    moduleInfo: moduleInfo
  };
})(window);
