package adapter

type ColorOSAdapter struct{ AOSPAdapter }

func NewColorOSAdapter(props map[string]string) *ColorOSAdapter {
	if props == nil {
		props = map[string]string{}
	}
	return &ColorOSAdapter{AOSPAdapter{props: props}}
}

func (a *ColorOSAdapter) Name() string { return "coloros" }

func (a *ColorOSAdapter) Preprocess(tool, name string, args []string) []string {
	if tool == "novaai_shell" || tool == "novaai_script" {
		return args
	}
	if name == "am" && len(args) > 0 && args[0] == "start" {
		if !hasFlag(args, "--user") {
			return append(args, "--user", "0")
		}
	}
	if name == "settings" && len(args) > 1 {
		if args[0] == "put" || args[0] == "delete" {
			if !hasFlag(args, "--user") {
				return append(args, "--user", "0")
			}
		}
	}
	return args
}
