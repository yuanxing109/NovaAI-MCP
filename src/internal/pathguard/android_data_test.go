package pathguard

import "testing"

// 本文件锁住 androidDataRoots：/sdcard/Android/{data,obb} 是
// "可读、变更需确认"的一档，既不是硬拒绝也不是随便动。
//
// 写这组用例的理由：这条规则只覆盖了别名的一部分就会被符号链接绕过
// （/sdcard/Android/data -> /storage/emulated/0/Android/data），
// 而绕过是静默的 —— 没有任何报错会提示你漏写了一个前缀。

var androidDataPaths = []string{
	"/sdcard/Android/data",
	"/sdcard/Android/data/com.example.app",
	"/sdcard/Android/data/com.example.app/files/x.bin",
	"/sdcard/Android/obb",
	"/sdcard/Android/obb/com.example.app/main.1.obb",
	"/storage/emulated/0/Android/data/com.example.app/cache/a",
	"/storage/emulated/0/Android/obb/com.example.app/x.obb",
	"/data/media/0/Android/data/com.example.app/x",
	"/mnt/sdcard/Android/data/com.example.app/x",
}

func TestAndroidDataDeniedByDefault(t *testing.T) {
	for _, p := range androidDataPaths {
		d := Check(p, false)
		if d.Allowed {
			t.Errorf("Check(%q) = allowed，期望默认拒绝", p)
			continue
		}
		if !d.Confirmable {
			t.Errorf("Check(%q) 被拒绝但 Confirmable=false —— 它应当是可以确认放行的",
				p)
		}
	}
}

func TestAndroidDataAllowedAfterConfirm(t *testing.T) {
	for _, p := range androidDataPaths {
		if d := ConfirmCheck(p, false); !d.Allowed {
			t.Errorf("ConfirmCheck(%q) = denied (%s)，期望确认后放行", p, d.Reason)
		}
	}
}

// 硬拒绝位置不受确认影响 —— 否则 confirmation 就成了万能钥匙。
func TestConfirmDoesNotUnlockFixedDeny(t *testing.T) {
	hard := []string{
		"/system/build.prop",
		"/data/adb/modules/x/system.prop",
		"/data/adb/magisk/x",
		"/dev/block/by-name/boot",
		"/proc/sys/vm/drop_caches",
	}
	for _, p := range hard {
		if d := ConfirmCheck(p, false); d.Allowed {
			t.Errorf("ConfirmCheck(%q) = allowed —— 确认不得解锁硬拒绝位置", p)
		}
		if d := ConfirmCheck(p, false); d.Confirmable {
			t.Errorf("ConfirmCheck(%q) 被标为 Confirmable —— 硬拒绝必须不可协商", p)
		}
	}
}

// 递归删除的根限制同样不受确认影响。
func TestConfirmDoesNotUnlockCriticalRoots(t *testing.T) {
	for _, p := range []string{"/sdcard", "/data", "/"} {
		if d := ConfirmCheck(p, true); d.Allowed {
			t.Errorf("ConfirmCheck(%q, recursive) = allowed，期望仍然拒绝", p)
		}
	}
}

// 邻接目录不得被误伤：按路径分量比较的意义就在这里。
func TestAndroidDataDoesNotOverreach(t *testing.T) {
	allowed := []string{
		"/sdcard/Android/media/com.example.app/x", // media 是公开的
		"/sdcard/Android/database",                // 不是 data
		"/sdcard/Android/databases",               // 前缀像但分量不同
		"/sdcard/Android",                         // 父目录本身
		"/sdcard/DCIM/a.jpg",
		"/sdcard/Download/x.apk",
	}
	for _, p := range allowed {
		d := Check(p, false)
		if !d.Allowed {
			t.Errorf("Check(%q) = denied (%s)，期望放行", p, d.Rule)
		}
	}
}

// /sdcard/Android 本身可写，但删掉它等于删掉所有应用的私有数据 ——
// 目前不在 criticalRoots 里。这条用例记录现状，避免被误以为已保护。
func TestAndroidDirItselfIsWritable(t *testing.T) {
	if d := Check("/sdcard/Android", true); !d.Allowed {
		t.Log("/sdcard/Android 已被保护（行为已收紧）")
		return
	}
	t.Log("已知残留：rm -rf /sdcard/Android 会连带清空 data 与 obb")
}

// ---- 读取永远放行（除块设备） ----

func TestCheckReadAllowsEverythingOrdinary(t *testing.T) {
	readable := append([]string{}, androidDataPaths...)
	readable = append(readable,
		"/sdcard/DCIM/a.jpg",
		"/data/adb/novaai-mcp/config.json",
		"/data/adb/modules/x/module.prop", // 排查模块问题必须能读
		"/data/data/com.example.app/databases/db",
		"/system/build.prop", // 读系统属性是诊断常态
	)
	for _, p := range readable {
		if d := CheckRead(p); !d.Allowed {
			t.Errorf("CheckRead(%q) = denied (%s)，期望读取放行", p, d.Rule)
		}
	}
}

func TestCheckReadStillDeniesBlockDevices(t *testing.T) {
	for _, p := range []string{"/dev/block/by-name/boot", "/proc/sys/vm/drop_caches"} {
		if d := CheckRead(p); d.Allowed {
			t.Errorf("CheckRead(%q) = allowed，块设备/内核参数不应可读", p)
		}
	}
}

// CheckRead 不应用 criticalRoots：那限制的是递归删除，与读无关。
func TestCheckReadIgnoresCriticalRoots(t *testing.T) {
	if d := CheckRead("/data"); !d.Allowed {
		t.Error("CheckRead(/data) 被拒绝 —— criticalRoots 不该参与读取判定")
	}
}

func TestReadOnlyPathsExposed(t *testing.T) {
	got := ReadOnlyPaths()
	if len(got) == 0 {
		t.Fatal("ReadOnlyPaths() 为空，status 无法展示这一档")
	}
	// 硬拒绝与只读必须不相交，否则展示会自相矛盾。
	for _, ro := range got {
		for _, hard := range ProtectedPaths() {
			if ro == hard {
				t.Errorf("%q 同时出现在 ProtectedPaths 与 ReadOnlyPaths", ro)
			}
		}
	}
}
