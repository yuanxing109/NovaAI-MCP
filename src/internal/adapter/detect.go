package adapter

import (
	"context"
	"os/exec"
	"strings"
)

func Detect(ctx context.Context) (Adapter, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	props := readProps(ctx, []string{
		"ro.build.version.release",
		"ro.build.version.sdk",
		"ro.miui.ui.version.name",
		"ro.build.version.opporom",
		"ro.vivo.os.version",
		"ro.build.flavor",
	})

	switch {
	case strings.HasPrefix(props["ro.miui.ui.version.name"], "V"):
		return NewMIUIAdapter(props), nil
	case strings.HasPrefix(props["ro.build.version.opporom"], "V"):
		return NewColorOSAdapter(props), nil
	case strings.HasPrefix(props["ro.vivo.os.version"], "V"):
		return NewOriginOSAdapter(props), nil
	case strings.Contains(strings.ToLower(props["ro.build.flavor"]), "samsung"):
		return NewOneUIAdapter(props), nil
	default:
		return NewAOSPAdapter(props), nil
	}
}

func readProps(ctx context.Context, keys []string) map[string]string {
	out := map[string]string{}
	for _, k := range keys {
		cmd := exec.CommandContext(ctx, "getprop", k)
		b, err := cmd.Output()
		if err == nil {
			out[k] = strings.TrimSpace(string(b))
		}
	}
	return out
}
