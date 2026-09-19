package adapter

type OriginOSAdapter struct{ AOSPAdapter }

func NewOriginOSAdapter(props map[string]string) *OriginOSAdapter {
	if props == nil {
		props = map[string]string{}
	}
	return &OriginOSAdapter{AOSPAdapter{props: props}}
}

func (a *OriginOSAdapter) Name() string { return "originos" }

func (a *OriginOSAdapter) Preprocess(tool, name string, args []string) []string {
	if tool == "novaai_shell" || tool == "novaai_script" {
		return args
	}
	return args
}

func (a *OriginOSAdapter) FallbackChain(cmd string, args []string) FallbackChain {
	return FallbackChain{
		{Command: cmd, Args: args, Note: "originos-primary"},
	}
}
