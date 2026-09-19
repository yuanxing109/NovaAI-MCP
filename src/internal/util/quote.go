package util

import "strings"

// ShQuote 把任意字符串安全地包成 shell 单引号字面量。
//
// 为什么不能用 fmt 的 %q：%q 做的是 Go 字符串转义，产出的是双引号字符串。
// 在 /system/bin/sh 的双引号内，$ 和反引号依然会被展开，所以
//
//	fmt.Sprintf("screencap -p %q", "/tmp/$(reboot)")
//
// 会执行命令替换 —— 参数里只要出现 $ 或反引号就是命令注入。
// 单引号内除单引号本身外一切都是字面量，这是 shell 里唯一可靠的转义方式。
//
// 只要一个值是外部可控的（JSON 参数、文件名、配置里的路径），并且要被拼进
// shell 命令，就必须经过本函数；未加引号的 %s 同样是注入点。
func ShQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
