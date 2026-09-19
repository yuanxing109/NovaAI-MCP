package antibrick

import "testing"

func TestInterceptDeniesBypassForms(t *testing.T) {
	// 这些都是旧的子串匹配会放过的写法
	deny := []string{
		"rm -rf /",
		"rm -fr /",         // 参数顺序不同
		"rm -rf  /",        // 双空格
		`rm -rf "/"`,       // 带引号
		`rm -rf '/'`,       // 单引号
		"busybox rm -rf /", // busybox 前缀
		"toybox rm -rf /",  // toybox 前缀
		"su -c 'rm -rf /'", // novaai_shell 就是这样包装的
		"sh -c 'rm -rf /'",
		"cd / && rm -rf data", // 相对路径 + cd 跟踪
		"rm -rf /data",
		"rm -rf /data/*",
		"rm -rf /sdcard",
		"rm -rf /data/adb/modules/foo",
		"rm -f /system/build.prop",
		"echo pwned > /system/build.prop",
		"echo pwned >> /system/build.prop",
		"dd if=/dev/zero of=/dev/block/by-name/boot",
		"mkfs.ext4 /dev/block/by-name/userdata",
		"mke2fs /dev/block/x",
		"wipefs -a /dev/block/x",
		"find / -delete",
		"tar -xzf evil.tar.gz -C /",
		"chmod -R 777 /system",
	}
	for _, c := range deny {
		if r := InterceptCommand(c); r.Allowed {
			t.Errorf("InterceptCommand(%q) = allowed, want denied", c)
		}
	}
}

func TestInterceptAllowsLegitimateCommands(t *testing.T) {
	allow := []string{
		"id",
		"getprop ro.build.version.release",
		"pm list packages",
		"ls -la /data/local/tmp",
		"rm -rf /data/local/tmp/mydir",
		"rm -rf /data/local/tmp/*",
		"rm -rf /sdcard/DCIM/tmp",
		"mkdir -p /data/local/tmp/x && cd /data/local/tmp/x && ls",
		"dd if=/dev/zero of=/data/local/tmp/test.img bs=1M count=1",
		"cat /proc/cpuinfo",
		"tar -czf /data/adb/novaai-mcp/backups/b.tar.gz /data/adb/novaai-mcp",
		"tar -xzf /data/adb/novaai-mcp/backups/b.tar.gz -C /data/local/tmp/restore",
		"chmod 644 /data/local/tmp/x.txt",
		"cp /sdcard/a.txt /sdcard/b.txt",
		"find /sdcard/DCIM -name '*.jpg'",
		"logcat -d -t 100",
		"dumpsys battery",
		"echo hello > /data/local/tmp/hello.txt",
		"mount -o bind /data/local/tmp/x /system/etc/y",
	}
	for _, c := range allow {
		if r := InterceptCommand(c); !r.Allowed {
			t.Errorf("InterceptCommand(%q) = denied (%s), want allowed", c, r.Reason)
		}
	}
}

func TestInterceptRespectsCwd(t *testing.T) {
	// cwd 未知时按 "/" 处理，与 exec.Command 未设 Dir 的行为一致
	if r := InterceptCommand("rm -rf data"); r.Allowed {
		t.Error("rm -rf data with unknown cwd = allowed, want denied")
	}
	// 已知 cwd 在 tmp 下时是安全的
	if r := InterceptCommandIn("rm -rf data", "/data/local/tmp"); !r.Allowed {
		t.Errorf("rm -rf data in /data/local/tmp = denied (%s), want allowed", r.Reason)
	}
}

func TestInterceptUnwrapsNestedShell(t *testing.T) {
	// exec.go 会生成 cd <cwd> && su -c '<cmd>'，必须一路展开到真实命令
	cases := []string{
		"su -c 'rm -rf /'",
		"su 2000 -c 'rm -rf /'",
		"cd /data && su -c 'rm -rf /system'",
		"sh -c \"sh -c 'rm -rf /'\"",
	}
	for _, c := range cases {
		if r := InterceptCommand(c); r.Allowed {
			t.Errorf("InterceptCommand(%q) = allowed, want denied", c)
		}
	}
}

func TestTokenizeKeepsQuotedText(t *testing.T) {
	toks := tokenize(`echo "a b" 'c d' e`)
	var texts []string
	for _, tk := range toks {
		if !tk.op {
			texts = append(texts, tk.text)
		}
	}
	want := []string{"echo", "a b", "c d", "e"}
	if len(texts) != len(want) {
		t.Fatalf("got %v, want %v", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Errorf("token %d = %q, want %q", i, texts[i], want[i])
		}
	}
}
