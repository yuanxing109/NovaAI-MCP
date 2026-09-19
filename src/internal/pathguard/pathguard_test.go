package pathguard

import "testing"

const testState = "/data/adb/novaai-mcp"

func TestCheckDeniesProtectedPaths(t *testing.T) {
	deny := []string{
		"/system",
		"/system/build.prop",
		"/system_ext/etc/permissions/x.xml",
		"/vendor/lib64/libc.so",
		"/product/etc/x",
		"/odm/etc/x",
		"/boot",
		"/recovery",
		"/persist/x",
		"/metadata/x",
		"/dev/block/by-name/boot",
		"/proc/sys/kernel/panic",
		"/data/adb/modules/foo/disable",
		"/data/adb/modules/foo/remove",
		"/data/adb/modules_update/foo",
		"/data/adb/magisk/db",
		"/data/adb/ksu/bin/ksud",
		"/data/adb/lspd/x",
		"/data/adb/post-fs-data.d/x.sh",
		"/data/adb/service.d/y.sh",
		// stateDir 里的敏感文件：通用载体不得改写
		testState + "/config.json",
		testState + "/token",
		testState + "/audit/2026-01.log",
		testState + "/mcp.sock",
	}
	for _, p := range deny {
		if d := Check(p, false); d.Allowed {
			t.Errorf("Check(%q) = allowed, want denied", p)
		}
	}
}

func TestCheckAllowsNormalPaths(t *testing.T) {
	allow := []string{
		"/systemfoo", // 前缀必须按路径分量比较
		"/data/local/tmp/x",
		"/sdcard/DCIM/a.jpg",
		"/storage/emulated/0/Download/b.zip",
		"/boot.img", // 不是 /boot 目录
		testState + "/workspace/a.txt",
		testState + "/tmp/b",
		testState + "/downloads/c",
		testState + "/uploads/d",
		testState + "/artifacts/e",
		testState + "/backups/f.tar.gz",
		testState + "/schedules/g.sh",
		testState + "/skills/h.md",
	}
	for _, p := range allow {
		if d := Check(p, false); !d.Allowed {
			t.Errorf("Check(%q) = denied (%s), want allowed", p, d.Reason)
		}
	}
}

func TestRecursiveDeleteOfCriticalRoots(t *testing.T) {
	// 这些根可以被写入，但不能被整体递归删除
	for _, p := range []string{"/", "/data", "/data/adb", "/sdcard", "/storage/emulated/0", "/mnt"} {
		if d := Check(p, true); d.Allowed {
			t.Errorf("Check(%q, recursive) = allowed, want denied", p)
		}
		if d := Check(p, false); !d.Allowed {
			t.Errorf("Check(%q, non-recursive) = denied, want allowed", p)
		}
	}

	// /data/local/tmp 本身不能删，但里面可以
	if d := Check("/data/local/tmp", true); d.Allowed {
		t.Error("Check(/data/local/tmp, recursive) = allowed, want denied")
	}
	if d := Check("/data/local/tmp/mydir", true); !d.Allowed {
		t.Error("Check(/data/local/tmp/mydir, recursive) = denied, want allowed")
	}
}

func TestCheckGlob(t *testing.T) {
	deny := []string{
		"/data/*",
		"/data/adb/*",
		"/system/*",
		"/data/adb/modules/*",
		"/sdcard/*",
		"/*",
		"/data/foo*", // 部分匹配，条目同样在 /data 里
	}
	for _, p := range deny {
		if d := CheckGlob(p); d.Allowed {
			t.Errorf("CheckGlob(%q) = allowed, want denied", p)
		}
	}

	allow := []string{
		"/data/local/tmp/*",
		"/sdcard/DCIM/*",
		"/data/local/tmp/work/*",
	}
	for _, p := range allow {
		if d := CheckGlob(p); !d.Allowed {
			t.Errorf("CheckGlob(%q) = denied (%s), want allowed", p, d.Reason)
		}
	}
}

func TestCheckSystemPathIgnoresStateDir(t *testing.T) {
	// 归档恢复要回到 stateDir，所以 SystemPath 只看格机级位置
	if d := CheckSystemPath(testState+"/config.json", false); !d.Allowed {
		t.Error("CheckSystemPath(stateDir/config.json) = denied, want allowed")
	}
	if d := CheckSystemPath("/system/build.prop", false); d.Allowed {
		t.Error("CheckSystemPath(/system/build.prop) = allowed, want denied")
	}
	if d := CheckSystemPath("/data/adb/modules/x/disable", false); d.Allowed {
		t.Error("CheckSystemPath(module dir) = allowed, want denied")
	}
}

func TestNormalizeCollapsesDotDot(t *testing.T) {
	cases := map[string]string{
		"/data/local/tmp/../tmp/x":              "/data/local/tmp/x",
		"/data/adb/modules/../../adb/modules/a": "/data/adb/modules/a",
		"/system/./build.prop":                  "/system/build.prop",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetStateDirMovesProtection(t *testing.T) {
	orig := StateDir()
	t.Cleanup(func() { SetStateDir(orig) })

	SetStateDir("/data/local/tmp/state")
	if d := Check("/data/local/tmp/state/config.json", false); d.Allowed {
		t.Error("new stateDir config.json = allowed, want denied")
	}
	if d := Check("/data/local/tmp/state/workspace/a", false); !d.Allowed {
		t.Error("new stateDir workspace = denied, want allowed")
	}
	// 旧 stateDir 不再是受保护位置
	if d := Check("/data/adb/novaai-mcp/config.json", false); !d.Allowed {
		t.Error("old stateDir config.json = denied, want allowed after move")
	}
}

func TestUnderIsComponentAware(t *testing.T) {
	if under("/system_ext/x", "/system") {
		t.Error("under(/system_ext/x, /system) = true, want false")
	}
	if !under("/system/x", "/system") {
		t.Error("under(/system/x, /system) = false, want true")
	}
	if !under("/system", "/system") {
		t.Error("under(/system, /system) = false, want true")
	}
	if under("/systematic", "/system") {
		t.Error("under(/systematic, /system) = true, want false")
	}
}
