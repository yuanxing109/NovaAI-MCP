#!/usr/bin/env node
/*
 * scripts/verify_config_write.cjs —— 验证 WebUI 写 config.json 的脚本是否正确。
 *
 * 为什么需要它：lib/config.js 的 writeAll 要把任意 JSON 塞进一个单引号
 * shell 字符串里。这条路径的失败模式是"config.json 被写坏 → daemon 下次
 * 起不来"，而 WebView 里出问题时人很难看出是转义错了还是桥错了。
 *
 * 做法是**驱动仓库里真实的 lib/config.js**，而不是照抄一份脚本：
 *   - 在 vm 里加载真文件，桩掉 NovaKsu.exec 捕获它实际会执行的那条脚本；
 *   - 把脚本写成文件，交给真 shell 执行（**不要**用 `sh -c "<script>"` ——
 *     Windows 上 node→MSYS sh 的 argv 往返会吃掉反斜杠，那是脚手架损耗，
 *     会假装成"脚本有 bug"）；
 *   - 比对落盘字节与 JSON.stringify 的结果。
 *
 * 用法：node scripts/verify_config_write.cjs [--shell <sh 路径>]
 * 退出码：0 = 通过。任一项不符即非 0。
 *
 * 注意：本脚本**没有接进 CI**（CI 的闸门全是 PowerShell 的 .ps1）。
 * 改动 lib/config.js 的写入路径后请手工跑一次。
 */
'use strict';

const fs = require('fs');
const os = require('os');
const path = require('path');
const vm = require('vm');
const { execFileSync } = require('child_process');

const REPO = path.resolve(__dirname, '..');
const CONFIG_JS = path.join(REPO, 'webroot', 'lib', 'config.js');

let failed = 0;
function check(name, ok, detail) {
  console.log((ok ? 'PASS  ' : 'FAIL  ') + name + (detail ? '  —— ' + detail : ''));
  if (!ok) { failed++; }
}

// ---------------------------------------------------------------- shell 定位
//
// POSIX 直接 /bin/sh。Windows 上 node 找不到 /bin/sh，按候选列表找一个 ——
// 允许用环境变量 NOVA_SH 或 --shell 覆盖。
function findShell() {
  const i = process.argv.indexOf('--shell');
  if (i >= 0 && process.argv[i + 1]) { return process.argv[i + 1]; }
  if (process.env.NOVA_SH) { return process.env.NOVA_SH; }
  if (process.platform !== 'win32') { return '/bin/sh'; }

  const cands = [
    'C:/Program Files/Git/usr/bin/sh.exe',
    'C:/Program Files (x86)/Git/usr/bin/sh.exe',
  ];
  // 本机开发环境：WorkBuddy 自带的 PortableGit。注意它在 %USERPROFILE%\.workbuddy
  // 下（不是 %LOCALAPPDATA%），且版本目录带版本号，所以要列举。
  // 这个目录不在 PATH 上，只能按已知位置找。
  const bases = [
    (process.env.USERPROFILE || os.homedir()) + '/.workbuddy/binaries/PortableGit/versions',
    (process.env.LOCALAPPDATA || '') + '/.workbuddy/binaries/PortableGit/versions',
  ];
  for (const base of bases) {
    try {
      for (const v of fs.readdirSync(base)) {
        cands.push(base + '/' + v + '/usr/bin/sh.exe');
      }
    } catch (e) { /* 没有就算了 */ }
  }

  for (const c of cands) { if (fs.existsSync(c)) { return c; } }
  return 'sh.exe'; // 交给 PATH
}

// -------------------------------------------------------- 加载真实的 config.js
function loadConfigModule(captured) {
  // config.js 是经典脚本（IIFE + 挂 window），所以桩一个 window 就够了。
  const sandbox = {
    window: {
      NovaKsu: {
        exec(script) {
          captured.push(script);
          return Promise.resolve({ errno: 0, stdout: '', stderr: '' });
        },
      },
    },
    console, Promise, JSON, Date,
  };
  vm.createContext(sandbox);
  vm.runInContext(fs.readFileSync(CONFIG_JS, 'utf8'), sandbox, { filename: CONFIG_JS });
  return sandbox.window.NovaConfig;
}

// 把 shell 会解释的东西全塞进来。
function hostileConfig() {
  return {
    stateDir: '/data/adb/novaai-mcp',
    listen: '0.0.0.0:5322',
    profile: 'default',
    upstreams: [{
      name: 'tricky',
      type: 'stdio',
      command: "/data/local/tmp/a'b\"c$d`e\\f",
      args: ['-x', '$HOME', '`id`', 'a  b', '$(whoami)', "it's"],
      denyTools: ["x'y", 'z"z'],
      riskCeiling: 2,
    }],
  };
}

function main() {
  const SH = findShell();
  console.log('shell = ' + SH);
  console.log('config.js = ' + CONFIG_JS);

  // --- 1. 自动备份必须已经移除（本轮的用户诉求，留下会静默回潮）---
  const captured = [];
  const NovaConfig = loadConfigModule(captured);
  check('NovaConfig.backup 不存在（自动备份已移除）',
    typeof NovaConfig.backup === 'undefined',
    '导出面 = ' + Object.keys(NovaConfig).join(', '));

  // --- 2. 驱动真实 writeAll，拿到它实际会执行的脚本 ---
  const cfg = hostileConfig();
  const tmpDir = fs.mkdtempSync(os.tmpdir().replace(/\\/g, '/') + '/novacfg-');
  // 不要用 path.join：它在 Windows 上把分隔符规范成 `\`，插进脚本会被
  // shell 当转义符吃掉（第一版就栽在这里）。
  const out = tmpDir + '/config.json';

  const pending = NovaConfig.writeAll(cfg);
  const script = captured.pop();
  if (!script) {
    check('writeAll 调用了 NovaKsu.exec', false, '没捕获到脚本');
    return finish(tmpDir);
  }

  // 只把目标路径改到临时目录，其余一个字节不动（要验的就是转义部分）。
  const patched = script.split('/data/adb/novaai-mcp/config.json').join(out);
  if (patched.indexOf('\n') >= 0) {
    check('写入脚本是单行（可安全嵌入桥调用）', false, '含真实换行');
    return finish(tmpDir);
  }

  // --- 3. 交给真 shell 执行 ---
  const scriptPath = tmpDir + '/write.sh';
  fs.writeFileSync(scriptPath, patched);
  try {
    execFileSync(SH, [scriptPath], { stdio: 'pipe' });
  } catch (e) {
    check('脚本在真 shell 中执行成功', false, String(e.message).split('\n')[0]);
    return finish(tmpDir);
  }

  // --- 4. 落盘字节必须与序列化结果完全一致 ---
  const want = JSON.stringify(cfg);
  const got = fs.readFileSync(out, 'utf8');
  check('落盘字节与 JSON.stringify 完全一致（引号 / $ / 反引号 / 反斜杠均未被解释）',
    want === got,
    want === got ? '' : '\n        want=' + want + '\n        got =' + got);

  // --- 5. 再读回来必须能还原成同一个对象（读路径的往返）---
  const readBack = JSON.parse(got);
  check('读回的 JSON 与原对象深等', JSON.stringify(readBack) === want);

  // 收尾：确认 writeAll 的 Promise 正常 resolve（没有未捕获异常）
  return pending.then(() => {
    check('writeAll 的 Promise 正常 resolve', true);
    finish(tmpDir);
  });
}

function finish(tmpDir) {
  fs.rmSync(tmpDir, { recursive: true, force: true });
  console.log('\n' + (failed === 0 ? '全部通过' : '存在 ' + failed + ' 项失败'));
  process.exit(failed === 0 ? 0 : 1);
}

main();
