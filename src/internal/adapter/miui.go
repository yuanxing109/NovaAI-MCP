package adapter

import "bytes"

type MIUIAdapter struct{ AOSPAdapter }

func NewMIUIAdapter(props map[string]string) *MIUIAdapter {
	if props == nil {
		props = map[string]string{}
	}
	return &MIUIAdapter{AOSPAdapter{props: props}}
}

func (a *MIUIAdapter) Name() string { return "miui" }

func (a *MIUIAdapter) Preprocess(tool, name string, args []string) []string {
	if tool == "novaai_shell" || tool == "novaai_script" {
		return args
	}
	if name == "pm" && len(args) > 0 {
		switch args[0] {
		case "list", "install", "uninstall":
			if !hasFlag(args, "--user") {
				return append(args, "--user", "0")
			}
		}
	}
	if name == "am" && len(args) > 0 && args[0] == "start" {
		if !hasFlag(args, "--user") {
			return append(args, "--user", "0")
		}
	}
	return args
}

func (a *MIUIAdapter) Postprocess(tool, name string, stdout []byte) ([]byte, error) {
	if name == "dumpsys" {
		stdout = stripMIUIBanner(stdout)
	}
	return stdout, nil
}

func (a *MIUIAdapter) FallbackChain(cmd string, args []string) FallbackChain {
	return FallbackChain{
		{Command: cmd, Args: args, Note: "miui-primary"},
		{Command: "su", Args: append([]string{"-c", cmd}, args...), Note: "miui-su-fallback"},
	}
}

func stripMIUIBanner(b []byte) []byte {
	lines := bytes.Split(b, []byte("\n"))
	out := lines[:0]
	for _, l := range lines {
		if bytes.Contains(l, []byte("MIUI Security")) {
			continue
		}
		out = append(out, l)
	}
	return bytes.Join(out, []byte("\n"))
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}
