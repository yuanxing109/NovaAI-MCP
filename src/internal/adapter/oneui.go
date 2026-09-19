package adapter

type OneUIAdapter struct{ AOSPAdapter }

func NewOneUIAdapter(props map[string]string) *OneUIAdapter {
	if props == nil {
		props = map[string]string{}
	}
	return &OneUIAdapter{AOSPAdapter{props: props}}
}

func (a *OneUIAdapter) Name() string { return "oneui" }

func (a *OneUIAdapter) Preprocess(tool, name string, args []string) []string {
	if tool == "novaai_shell" || tool == "novaai_script" {
		return args
	}
	if name == "pm" && len(args) > 0 && args[0] == "list" {
		if !hasFlag(args, "--user") {
			return append(args, "--user", "0")
		}
	}
	if name == "logcat" && !hasFlag(args, "-b") {
		return append([]string{"-b", "all"}, args...)
	}
	return args
}

func (a *OneUIAdapter) FallbackChain(cmd string, args []string) FallbackChain {
	return FallbackChain{
		{Command: cmd, Args: args, Note: "oneui-primary"},
	}
}
