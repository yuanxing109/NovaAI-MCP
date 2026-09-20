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

  /** 读取并解析 config.json。文件不存在时返回 daemon 默认值的等价物。 */
  function read() {
    return global.NovaKsu.exec('cat ' + CONFIG_PATH + ' 2>/dev/null').then(function (r) {
      if (r.errno !== 0 || !r.stdout.trim()) {
        throw new Error('读不到 ' + CONFIG_PATH +
          '（' + (r.stderr.trim() || '文件不存在') + '）。daemon 尚未启动过？');
      }
      try {
        return JSON.parse(r.stdout);
      } catch (e) {
        throw new Error('config.json 不是合法 JSON：' + e.message);
      }
    });
  }

  /**
   * 把一份完整的配置原子写回磁盘。
   *
   * 用单引号 heredoc 而不是 echo：JSON 压缩成一行后不会包含真实换行，
   * 因此不可能撞上结束标记；而 <<'EOF' 不做任何变量展开与转义处理，
   * 内容里的 ' " $ \ 都原样落盘。用 echo '...' 拼接则会被 shell 解析。
   */
  function writeAll(cfg) {
    var json = JSON.stringify(cfg);
    if (json.indexOf('\n') >= 0) {
      // JSON.stringify 不会产出真实换行；真出现了说明上游有东西在骗我们，
      // 这时 heredoc 的终止条件就不再可靠，必须拒绝而不是硬写。
      return Promise.reject(new Error('拒绝写入：序列化结果含换行，无法安全嵌入 heredoc'));
    }
    var script =
      'cat > ' + TMP_PATH + " << 'NOVA_CFG_EOF'\n" +
      json + '\n' +
      'NOVA_CFG_EOF\n' +
      'chmod 0600 ' + TMP_PATH + ' && ' +
      'mv ' + TMP_PATH + ' ' + CONFIG_PATH + '\n';

    return global.NovaKsu.exec(script).then(function (r) {
      if (r.errno !== 0) {
        throw new Error('写 config.json 失败：' + (r.stderr.trim() || ('errno=' + r.errno)));
      }
      return true;
    });
  }

  /** 只改 upstreams，其余字段原样保留。 */
  function setUpstreams(list) {
    return read().then(function (cfg) {
      cfg.upstreams = list;
      return writeAll(cfg).then(function () { return cfg; });
    });
  }

  /** 备份当前 config.json（改动前的保险），返回备份路径。 */
  function backup() {
    var stamp = new Date().toISOString().replace(/[:.]/g, '-');
    var dst = CONFIG_PATH + '.webui-' + stamp + '.bak';
    return global.NovaKsu.exec('cp ' + CONFIG_PATH + ' ' + dst + ' 2>/dev/null').then(function (r) {
      return r.errno === 0 ? dst : null;
    });
  }

  global.NovaConfig = {
    setStateDir: setStateDir,
    paths: paths,
    read: read,
    writeAll: writeAll,
    setUpstreams: setUpstreams,
    backup: backup
  };
})(window);
