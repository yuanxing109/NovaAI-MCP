/*
 * lib/config.js —— config.json 的读写。
 *
 * WebUI 与 daemon 之间**没有**私有的配置接口：管理动作就是
 * "原子改写 config.json + 调 novaai_config reload_upstreams"。
 * 这样 daemon 不需要为 WebUI 开任何特权通道，WebUI 也不需要一个
 * 只属于它的鉴权模型。
 *
 * 写入的两条硬约束：
 *   - **原子**：先写 .tmp 再 mv。半截的 config.json 会让 daemon 下次
 *     启动直接失败 —— 一次"加个上游"变成"服务起不来"。
 *   - **不产生第二个 owner**：只改 upstreams 一个键，其余字段原样写回。
 *     所以本文件先读全文、改一个键、再整体写回，而不是拼一份新配置。
 *
 * 不做自动备份：早期实现每次改动前 cp 一份 config.json.webui-<时间>.bak，
 * 在 /data/adb/novaai-mcp 下越积越多。现在没有这一层，剩下的保护是
 * "写入原子"（改坏也不会留下半截文件）。要回退就手工改回 —— 配置只有
 * 9 个键，且未知键会被 daemon 静默忽略。
 *
 * 写入用 printf 不用 heredoc：KSU 桥的 shell 对多行 heredoc 会报
 * "unclosed"（设备上实测）。lib/mcp-client.js 的传输层同理。
 */
(function (global) {
  'use strict';

  var STATE_DIR = '/data/adb/novaai-mcp';
  var CONFIG_PATH = STATE_DIR + '/config.json';
  var TMP_PATH = CONFIG_PATH + '.tmp';

  // 与 daemon 侧 config.Default() 的 stateDir 一致。允许覆盖是为了在
  // 非标准安装路径下仍能工作（例如开发时改过 --state）。
  function setStateDir(dir) {
    if (!dir) { return; }
    STATE_DIR = dir;
    CONFIG_PATH = STATE_DIR + '/config.json';
    TMP_PATH = CONFIG_PATH + '.tmp';
  }

  function paths() {
    return { stateDir: STATE_DIR, config: CONFIG_PATH, tmp: TMP_PATH };
  }

  /**
   * 读取并解析 config.json。
   *
   * 失败时必须把三种不同的原因分开 —— 初版把"桥收到了命令但什么都没执行"
   * 也报成"文件不存在"，让人对着一个明明存在的文件排查了半天：
   *   1. cat 真的失败了（errno 非 0 且有 stderr）—— 才是"文件不存在"等；
   *   2. errno 非 0 但没有 stderr —— 只能报退出码；
   *   3. errno 0 但输出为空 —— 桥没有真正执行命令，是调用约定问题。
   */
  function read() {
    return global.NovaKsu.exec('cat ' + CONFIG_PATH).then(function (r) {
      var out = (r.stdout || '').trim();
      if (out) {
        try {
          return JSON.parse(out);
        } catch (e) {
          throw new Error('config.json 不是合法 JSON：' + e.message);
        }
      }

      var errText = (r.stderr || '').trim();
      if (r.errno !== 0 && errText) {
        throw new Error('读不到 ' + CONFIG_PATH + '：' + errText);
      }
      if (r.errno !== 0) {
        throw new Error('读不到 ' + CONFIG_PATH + '（cat 退出码 ' + r.errno +
          '，无错误输出）。daemon 尚未启动过？');
      }
      throw new Error('读 ' + CONFIG_PATH + ' 没有任何输出，也没有报错 —— ' +
        'KernelSU 桥收到了命令但没有执行它（exec 调用约定不匹配？）。' +
        '请更新 WebUI 或回报这个问题。');
    });
  }

  /** 只改 upstreams，其余字段原样保留。 */
  function setUpstreams(list) {
    return read().then(function (cfg) {
      cfg.upstreams = list;
      return writeAll(cfg).then(function () { return cfg; });
    });
  }

  /**
   * 把一份完整的配置原子写回磁盘。
   *
   * 单引号字符串里只有 ' 需要转义（'\''）；$、反引号、反斜杠都是字面量，
   * JSON.stringify 的产物原样落盘。序列化结果必须不含真实换行 ——
   * 那会让"一行 JSON"的前提失效，且脚本无法安全嵌入。
   */
  function writeAll(cfg) {
    var json = JSON.stringify(cfg);
    if (json.indexOf('\n') >= 0 || json.indexOf('\r') >= 0) {
      return Promise.reject(new Error('拒绝写入：序列化结果含换行，无法安全嵌入脚本'));
    }
    var safeJson = json.replace(/'/g, "'\\''");

    var script =
      "printf '%s' '" + safeJson + "' > " + TMP_PATH + '; ' +
      'chmod 0600 ' + TMP_PATH + ' && ' +
      'mv ' + TMP_PATH + ' ' + CONFIG_PATH;

    return global.NovaKsu.exec(script).then(function (r) {
      if (r.errno !== 0) {
        throw new Error('写 config.json 失败：' + ((r.stderr || '').trim() || ('errno=' + r.errno)));
      }
      return true;
    });
  }

  global.NovaConfig = {
    setStateDir: setStateDir,
    paths: paths,
    read: read,
    writeAll: writeAll,
    setUpstreams: setUpstreams
  };
})(window);
